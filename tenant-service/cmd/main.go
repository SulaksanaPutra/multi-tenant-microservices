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

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/middleware"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/service"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Tenant Service...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "broker_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	httpPort := getEnv("PORT", "8082")

	// 1. Connect Infrastructure Drivers
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	// 2. Initialize Repositories & Tenant Middleware (Pool Registry & Resolution)
	provisionerRepo := repository.NewProvisionerRepository(dbClient)
	controlPlaneRepo := repository.NewControlPlaneRepository(dbClient)
	outboxRepo := repository.NewOutboxRepository(dbClient.DB)
	tenantMiddleware := middleware.NewTenantMiddleware(controlPlaneRepo, dbClient.DB)
	defer tenantMiddleware.CloseAll()

	// 3. Register & Start Background Workers Collection
	tenantPublisher, err := publisher.NewTenantPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize tenant publisher: %v", err)
	}

	wRunner := registerWorkers(outboxRepo, tenantPublisher, tenantMiddleware)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	wRunner.start(workerCtx)

	// 4. Initialize Domain Services
	provisionerService := service.NewProvisionerService(dbClient, provisionerRepo, outboxRepo, wRunner.OutboxWorker(), tenantMiddleware)

	// 5. Register & Start Inbound Queue Consumers Collection
	cRunner, err := registerConsumers(rmqClient, provisionerService)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// 6. Register HTTP Router
	httpRouter := newRouter(tenantMiddleware)
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("Tenant Service HTTP API listening on port %s...", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 7. Graceful Shutdown Setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Tenant Service gracefully...")
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
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
