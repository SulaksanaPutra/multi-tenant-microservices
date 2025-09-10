package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"user-service/internal/utils"

	"user-service/internal/handler"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/service"
)

func main() {
	utils.LoadEnv(".env")
	log.Println("Starting User Service...")

	// Environment variables
	dbHost := utils.GetEnv("DB_HOST", "localhost")
	dbPort := utils.GetEnv("DB_PORT", "5432")
	dbUser := utils.GetEnv("DB_USER", "postgres")
	dbPassword := utils.GetEnv("DB_PASSWORD", "postgres")
	dbName := utils.GetEnv("DB_NAME", "broker_db")
	amqpURL := utils.GetEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	httpPort := utils.GetEnv("PORT", "8081")

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

	// Initialize Repositories
	userRepo := repository.NewUserRepository(dbClient)
	tenantRepo := repository.NewTenantRepository(dbClient)
	outboxRepo := repository.NewOutboxRepository(dbClient.DB)
	inboxRepo := repository.NewInboxRepository(dbClient)

	// Register & Start Background Workers Collection
	userPublisher, err := publisher.NewUserPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize user publisher: %v", err)
	}

	wRunner := registerWorkers(outboxRepo, userPublisher)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	wRunner.start(workerCtx)
	outboxWorker := wRunner.OutboxWorker()

	// Initialize Domain Services
	userService := service.NewUserService(service.UserServiceParams{
		UserRepo:     userRepo,
		TenantRepo:   tenantRepo,
		OutboxRepo:   outboxRepo,
		InboxRepo:    inboxRepo,
		OutboxWorker: outboxWorker,
	})

	// Register & Start Inbound Queue Consumers Collection
	cRunner, err := registerConsumers(dbClient, rmqClient, userService)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// Register HTTP Router
	userHandler := handler.NewUserHandler(handler.UserHandlerParams{
		DB:          dbClient.DB,
		UserService: userService,
	})
	httpRouter := newRouter(userHandler)
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
