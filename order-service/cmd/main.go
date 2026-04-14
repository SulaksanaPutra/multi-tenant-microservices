package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/authclient"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/registry"
	"order-service/internal/service"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Order Service...")

	sharedSecret := getEnv("SHARED_DB_SECRET", "default_shared_db_secret_key")
	sharedDBPass := getEnv("SHARED_DB_PASSWORD", getEnv("DB_PASSWORD", "postgres"))
	internalToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	httpPort := getEnv("PORT", "8084")
	tenantServiceURL := getEnv("TENANT_SERVICE_URL", "http://tenant-service:8082")
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8085")

	// Register domain permissions with auth-service (non-blocking)
	permRegistrar := authclient.NewPermissionRegistrar(authServiceURL, internalToken)
	permissions := make([]authclient.PermissionItem, len(domain.OrderServicePermissions))
	for i, p := range domain.OrderServicePermissions {
		permissions[i] = authclient.PermissionItem{Name: p.Name, Description: p.Description}
	}
	go func() {
		if err := permRegistrar.Register(context.Background(), "order-service", permissions); err != nil {
			log.Printf("Order Service: Warning — startup permission registration deferred: %v", err)
		}
	}()

	// 1. Connect RabbitMQ Driver
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rmqClient.Close()

	// 2. Initialize Dynamic Pool & Routing Registries
	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	defer reaperCancel()
	poolRegistry.StartReaper(reaperCtx)

	tenantDBResolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:         poolRegistry,
		RoutingRegistry:      routingRegistry,
		TenantServiceURL:     tenantServiceURL,
		InternalServiceToken: internalToken,
		SharedSecret:         sharedSecret,
		SharedDBPass:         sharedDBPass,
	})

	// 3. Initialize Migration Service
	migrationSvc, err := service.NewMigrationService("migrations/001_create_orders.sql")
	if err != nil {
		log.Fatalf("Failed to initialize MigrationService: %v", err)
	}

	// 4. Register & Start Inbound Consumers
	cRunner, err := registerConsumers(rmqClient, migrationSvc, poolRegistry, routingRegistry, sharedSecret, sharedDBPass)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// 5. Register HTTP Router
	httpRouter := newRouter(tenantDBResolver)
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
