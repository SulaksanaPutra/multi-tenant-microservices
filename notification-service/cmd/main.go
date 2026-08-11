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

	"notification-service/internal/domain"
	"notification-service/internal/handler"
	"notification-service/internal/infrastructure/authclient"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/mailer"
	"notification-service/internal/migration"
	"notification-service/internal/repository"
	"notification-service/internal/service"
	"notification-service/internal/txcontext"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Notification Service Worker...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "notification_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	smtpHost := getEnv("SMTP_HOST", "mailpit")
	smtpPort := getEnv("SMTP_PORT", "1025")
	httpPort := getEnv("PORT", "8083")
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8085")
	internalServiceToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")

	// Register domain permissions with auth-service (non-blocking)
	permRegistrar := authclient.NewPermissionRegistrar(authServiceURL, internalServiceToken)
	permissions := make([]authclient.PermissionItem, len(domain.NotificationServicePermissions))
	for i, p := range domain.NotificationServicePermissions {
		permissions[i] = authclient.PermissionItem{Name: p.Name, Description: p.Description}
	}
	go func() {
		if err := permRegistrar.Register(context.Background(), "notification-service", permissions); err != nil {
			log.Printf("Notification Service: Warning — startup permission registration deferred: %v", err)
		}
	}()

	// 1. Connect Infrastructure Drivers
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	// Run schema migrations (goose, library-mode, embedded; advisory-locked)
	if err := migration.Run(context.Background(), dbClient.DB); err != nil {
		log.Fatalf("Failed to run notification DB migrations: %v", err)
	}

	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	// 2. Initialize Infrastructure Mailer & Auth Client
	m := mailer.NewMailer(smtpHost, smtpPort, "no-reply@company.com")
	authClient := authclient.NewAuthClient(authServiceURL, internalServiceToken)

	// 3. Initialize Repositories (Data Access Layer & Inbox Pattern) & TxManager
	txManager := txcontext.NewTxManager(dbClient.DB)
	notifRepository := repository.NewNotificationRepository(dbClient)
	inboxRepository := repository.NewInboxRepository(dbClient)

	// 4. Initialize Domain Services
	inboxService := service.NewInboxService(inboxRepository)
	notifService := service.NewNotificationService(notifRepository)

	// 5. Register & Start Inbound Queue Consumers Collection
	cRunner, err := registerConsumers(txManager, rmqClient, inboxService, notifService, authClient, m)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// 6. Register HTTP Router & Handlers
	notifHandler := handler.NewNotificationHandler(notifService)
	httpRouter := newRouter(notifHandler)
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("Notification Service Health HTTP listening on port %s...", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// 7. Graceful Shutdown Setup
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Notification Service Worker gracefully...")
	_ = httpServer.Shutdown(context.Background())
}

func loadEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()

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
