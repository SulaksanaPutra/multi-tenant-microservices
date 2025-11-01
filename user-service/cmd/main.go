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

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/service"
	"user-service/internal/txcontext"
)

func main() {
	loadEnv(".env")
	log.Println("Starting User Service...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "user_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	httpPort := getEnv("PORT", "8081")

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

	// Initialize Repositories & TxManager
	txManager := txcontext.NewTxManager(dbClient.DB)
	userRepo := repository.NewUserRepository(dbClient)
	inboxRepo := repository.NewInboxRepository(dbClient)
	outboxRepo := repository.NewOutboxRepository(dbClient.DB)

	// Initialize Publisher
	userPublisher, err := publisher.NewUserPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize user publisher: %v", err)
	}

	// Register & Start Background Workers
	wRunner := registerWorkers(outboxRepo, userPublisher)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	wRunner.start(workerCtx)

	// Initialize Domain Services
	userService := service.NewUserService(userRepo, outboxRepo)

	// Register & Start Inbound Queue Consumers Collection
	cRunner, err := registerConsumers(txManager, rmqClient, inboxRepo, userService)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// Register HTTP Router
	httpRouter := newRouter()
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("User Service HTTP API listening on port %s...", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Graceful Shutdown Setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down User Service gracefully...")
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
