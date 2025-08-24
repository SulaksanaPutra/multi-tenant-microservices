package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"notification-service/internal/consumer"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/mailer"
	"notification-service/internal/repository"
	"notification-service/internal/service"
)

func main() {
	log.Println("Starting Notification Service Worker...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "localhost")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "broker_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	smtpHost := getEnv("SMTP_HOST", "localhost")
	smtpPort := getEnv("SMTP_PORT", "1025")

	// 1. Connect to PostgreSQL Infrastructure Driver
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	// 2. Initialize SMTP Mailer
	m := mailer.NewMailer(smtpHost, smtpPort, "no-reply@company.com")

	// 3. Connect to RabbitMQ Infrastructure Driver
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	// 4. Initialize Repository (Data Access Layer)
	notifRepo := repository.NewNotificationRepository(dbClient)

	// 5. Initialize Business Service (Business Logic Layer)
	notifService := service.NewNotificationService(notifRepo, m)

	// 6. Initialize & Start Worker Consumer (Transport Layer)
	notifConsumer, err := consumer.NewTenantProvisionedConsumer(rmqClient, notifService)
	if err != nil {
		log.Fatalf("Failed to initialize notification consumer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := notifConsumer.Start(ctx); err != nil {
		log.Fatalf("Failed to start notification consumer: %v", err)
	}

	// Graceful shutdown setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Notification Service Worker gracefully...")
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
