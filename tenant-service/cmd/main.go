package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tenant-service/internal/consumer"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/service"
	"tenant-service/internal/worker"
)

func main() {
	log.Println("Starting Tenant Service Worker...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "broker_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")

	// 1. Connect to PostgreSQL Infrastructure Driver
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	// 2. Connect to RabbitMQ Infrastructure Driver
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	// 3. Initialize Repositories (Data Access Layer) & Connection Registry
	provisionerRepo := repository.NewProvisionerRepository(dbClient)
	outboxRepo := repository.NewOutboxRepository(dbClient.DB)
	registry := postgres.NewConnectionRegistry(dbClient.DB)
	defer registry.CloseAll()

	// 4. Initialize Outbound Event Publisher & Outbox Worker
	tenantPublisher, err := publisher.NewTenantPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize tenant publisher: %v", err)
	}

	outboxWorker := worker.NewOutboxWorker(outboxRepo, tenantPublisher, "tenant.provisioned")
	outboxWorker.SetConnectionRegistry(registry)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	go outboxWorker.Start(workerCtx)

	// 5. Initialize Business Service (Business Logic Layer)
	tenantService := service.NewTenantService(dbClient, provisionerRepo, outboxRepo, outboxWorker)
	tenantService.SetConnectionRegistry(registry)

	// 6. Initialize & Start Worker Consumer (Inbound Transport Layer)
	userConsumer, err := consumer.NewUserRegisteredConsumer(rmqClient, tenantService)
	if err != nil {
		log.Fatalf("Failed to initialize user consumer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := userConsumer.Start(ctx); err != nil {
		log.Fatalf("Failed to start user consumer: %v", err)
	}

	// Graceful shutdown setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Tenant Service Worker gracefully...")
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
