package main

import (
	"bufio"
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/handler"
	"payment-service/internal/infrastructure/authclient"
	"payment-service/internal/infrastructure/postgres"
	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/migration"
	"payment-service/internal/provider"
	"payment-service/internal/provider/directbank"
	"payment-service/internal/provider/mock"
	"payment-service/internal/repository"
	"payment-service/internal/service"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

func main() {
	loadEnv(".env")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("Starting Payment Service...")

	port := getEnv("PORT", "8086")
	dbHost := getEnv("DB_HOST", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "payment_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8085")
	internalServiceToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")
	jwtPubKeyPEM := getEnv("AUTH_JWT_PUBLIC_KEY_PEM", "")
	encryptionKeyStr := getEnv("PAYMENT_ENCRYPTION_KEY", "default_32_bytes_payment_enc_key")

	if jwtPubKeyPEM == "" {
		log.Fatal("AUTH_JWT_PUBLIC_KEY_PEM environment variable is required")
	}

	if len(encryptionKeyStr) != 32 {
		log.Fatalf("PAYMENT_ENCRYPTION_KEY must be exactly 32 bytes long, got %d bytes", len(encryptionKeyStr))
	}
	masterEncryptionKey := []byte(encryptionKeyStr)

	// 1. Connect Infrastructure Drivers
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	if err := migration.Run(context.Background(), dbClient.DB); err != nil {
		log.Fatalf("Failed to run goose database migrations: %v", err)
	}

	// 2. Initialize Repositories & TxManager
	txManager := txcontext.NewTxManager(dbClient.DB)
	paymentRepository := repository.NewPaymentRepository(dbClient)
	inboxRepository := repository.NewInboxRepository(dbClient)
	outboxRepository := repository.NewOutboxRepository(dbClient)
	pspConfigRepository := repository.NewPSPConfigRepository(dbClient)

	postgresResolver := provider.NewPostgresTenantPSPResolver(
		pspConfigRepository,
		masterEncryptionKey,
		[]domain.ProviderType{domain.ProviderMock, domain.ProviderDirectBank},
	)

	registry := provider.NewProviderRegistry(postgresResolver)
	registry.RegisterProvider(mock.NewMockProvider(domain.ProviderMock, "mock_secret_key", false), 3, 30*time.Second)
	registry.RegisterProvider(directbank.NewDirectBankProvider("BCA"), 3, 30*time.Second)

	// 3. Initialize Domain Services
	pspConfigService := service.NewPSPConfigService(
		txManager,
		pspConfigRepository,
		postgresResolver,
		masterEncryptionKey,
		logger,
	)

	paymentProviderService := service.NewPaymentProviderService(
		registry,
		logger,
	)

	paymentService := service.NewPaymentService(
		txManager,
		paymentRepository,
		inboxRepository,
		outboxRepository,
		logger,
	)

	// 4. Register Domain Permissions with auth-service (non-blocking)
	registrar := authclient.NewPermissionRegistrar(authServiceURL, internalServiceToken)
	permItems := []authclient.PermissionItem{
		{Name: domain.PermissionPaymentsRead, Description: "Allows viewing payment status"},
		{Name: domain.PermissionPaymentsCreate, Description: "Allows initiating payment flows"},
		{Name: domain.PermissionPaymentsRefund, Description: "Allows issuing payment refunds"},
		{Name: domain.PermissionPaymentsWebhook, Description: "Allows receiving payment webhooks"},
		{Name: domain.PermissionPaymentsManage, Description: "Allows managing tenant PSP configurations"},
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := registrar.Register(ctx, "payment-service", permItems); err != nil {
			logger.Warn("permission registration deferred", "err", err)
		} else {
			logger.Info("registered domain permissions with auth-service")
		}
	}()

	// 5. Connect RabbitMQ Driver & Background Workers
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to connect to rabbitmq: %v", err)
	}
	defer rmqClient.Close()

	if err := rmqClient.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		log.Fatalf("Failed to declare exchange: %v", err)
	}

	workerRunner := registerWorkers(outboxRepository, rmqClient, paymentService, logger)
	workerRunner.start(context.Background())
	defer workerRunner.stop()

	inboxService := service.NewInboxService(inboxRepository)

	consumerRunner, err := registerConsumers(rmqClient, txManager, inboxService, paymentService, paymentProviderService, logger)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}
	if err := consumerRunner.start(context.Background()); err != nil {
		logger.Warn("failed to start consumers", "err", err)
	}

	// 6. Register HTTP Router & Handlers
	paymentHandler := handler.NewPaymentHandler(paymentService, paymentProviderService, pspConfigService)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: newRouter(paymentHandler, jwtPubKeyPEM),
	}

	go func() {
		logger.Info("Payment Service HTTP API listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// 7. Graceful Shutdown Setup
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("Shutting down payment-service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "err", err)
	}

	logger.Info("payment-service exited cleanly")
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
