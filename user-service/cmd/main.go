package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"user-service/internal/handler"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/service"
	"user-service/internal/worker"
)

func main() {
	log.Println("Starting User Service...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "broker_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	serverPort := getEnv("PORT", "8081")

	// 1. Initialize PostgreSQL Infrastructure Driver
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize postgres client: %v", err)
	}
	defer dbClient.Close()

	// 2. Initialize RabbitMQ Infrastructure Driver & Publisher
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	userPublisher, err := publisher.NewUserPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize user publisher: %v", err)
	}

	// 3. Initialize Repositories & Worker (Data Access & Outbox Layer)
	userRepo := repository.NewUserRepository()
	tenantRepo := repository.NewTenantRepository()
	outboxRepo := repository.NewOutboxRepository(dbClient.DB)

	outboxWorker := worker.NewOutboxWorker(outboxRepo, userPublisher, "user.registered")

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	go outboxWorker.Start(workerCtx)

	// 4. Initialize Business Service (Business Logic Layer)
	userService := service.NewUserService(dbClient, userRepo, tenantRepo, outboxRepo, outboxWorker)

	// 5. Initialize HTTP Handler (Transport Layer)
	userHandler := handler.NewUserHandler(userService)

	// 6. Register HTTP Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/register", userHandler.RegisterUser)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":" + serverPort,
		Handler: mux,
	}

	// Graceful shutdown setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("User Service REST API listening on port %s...", serverPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down User Service gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced shutdown error: %v", err)
	}

	log.Println("User Service stopped.")
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
