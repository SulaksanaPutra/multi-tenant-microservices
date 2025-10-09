package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/registry"
	"order-service/internal/repository"
	"order-service/internal/service"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Order Service...")

	// Config for provisioner (used dynamically on demand)
	sharedProvisionerHost := getEnv("SHARED_DB_HOST", getEnv("DB_HOST", "postgres"))
	sharedProvisionerPort := getEnv("SHARED_DB_PORT", getEnv("DB_PORT", "5432"))
	sharedProvisionerUser := getEnv("SHARED_DB_USER", getEnv("DB_USER", "postgres"))
	sharedProvisionerPassword := getEnv("SHARED_DB_PASSWORD", getEnv("DB_PASSWORD", "postgres"))
	sharedProvisionerDBName := getEnv("SHARED_DB_NAME", "shared_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	httpPort := getEnv("PORT", "8084")
	tenantServiceURL := getEnv("TENANT_SERVICE_URL", "http://tenant-service:8082")

	// DSN template used dynamically on demand to provision shared schemas and dedicated DBs
	sharedProvisionerDSN := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		sharedProvisionerHost, sharedProvisionerPort, sharedProvisionerUser, sharedProvisionerPassword, sharedProvisionerDBName)

	// 1. Connect RabbitMQ Driver
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rmqClient.Close()

	// 2. Initialize Dynamic Pool Registry (all runtime tenant connections are opened lazily on demand)
	poolRegistry := registry.NewPoolRegistry()
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	poolRegistry.StartReaper(reaperCtx)

	tenantDBResolver := tenantdb.NewResolver(tenantdb.ResolverParams{
		Registry:         poolRegistry,
		TenantServiceURL: tenantServiceURL,
	})

	// 3. Initialize Provisioner Service
	provisioner, err := service.NewProvisionerService(service.ProvisionerServiceParams{
		SharedProvisionerDSN: sharedProvisionerDSN,
		MigrationFile:        "migrations/001_create_orders.sql",
	})
	if err != nil {
		log.Fatalf("Failed to initialize ProvisionerService: %v", err)
	}

	orderRepo := repository.NewOrderRepository()
	orderService := service.NewOrderService(service.OrderServiceParams{
		DBResolver: tenantDBResolver,
		OrderRepo:  orderRepo,
	})

	// 4. Register & Start Inbound Consumers
	cRunner, err := registerConsumers(rmqClient, provisioner, poolRegistry, tenantServiceURL)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// 5. Register HTTP Router
	httpRouter := newRouter(orderService)
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("Order Service HTTP API listening on port %s...", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 6. Graceful Shutdown Setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Order Service gracefully...")
	_ = httpServer.Shutdown(context.Background())
}

func loadEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
