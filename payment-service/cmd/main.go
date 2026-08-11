package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"

	"payment-service/internal/consumer"
	"payment-service/internal/domain"
	"payment-service/internal/handler"
	"payment-service/internal/infrastructure/authclient"
	"payment-service/internal/infrastructure/rabbitmq"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"payment-service/internal/migration"
	"payment-service/internal/provider"
	"payment-service/internal/provider/directbank"
	"payment-service/internal/provider/mock"
	"payment-service/internal/repository"
	"payment-service/internal/service"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"payment-service/internal/worker"
)

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

const defaultRS256PublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAvH4L0sY19tJ6u/o9g
+2s61i1g9kY5K5XvW8wXzJ8... (fallback placeholder)
-----END PUBLIC KEY-----`

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := getEnv("PAYMENT_SERVICE_PORT", "8086")
	dbHost := getEnv("POSTGRES_HOST", "localhost")
	dbPort := getEnv("POSTGRES_PORT", "5432")
	dbUser := getEnv("POSTGRES_USER", "postgres")
	dbPassword := getEnv("POSTGRES_PASSWORD", "postgres")
	dbName := getEnv("POSTGRES_DB", "paymentDB")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://localhost:8085")
	internalServiceToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")
	jwtPubKeyPEM := getEnv("AUTH_JWT_PUBLIC_KEY_PEM", defaultRS256PublicKey)
	masterEncryptionKey := []byte(getEnv("PAYMENT_ENCRYPTION_KEY", "default_32_bytes_payment_enc_key"))

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		dbHost, dbPort, dbUser, dbPassword, dbName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("failed to connect to payment database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("failed to ping payment database: %v", err)
	}

	if err := migration.Run(context.Background(), db); err != nil {
		log.Fatalf("failed to run goose database migrations: %v", err)
	}

	txMgr := txcontext.NewTxManager(db)
	paymentRepo := repository.NewPaymentRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	pspConfigRepo := repository.NewPSPConfigRepository(db)

	postgresResolver := provider.NewPostgresTenantPSPResolver(
		pspConfigRepo,
		masterEncryptionKey,
		[]domain.ProviderType{domain.ProviderMock, domain.ProviderDirectBank},
	)

	registry := provider.NewProviderRegistry(postgresResolver)
	registry.RegisterProvider(mock.NewMockProvider(domain.ProviderMock, "mock_secret_key", false), 3, 30*time.Second)
	registry.RegisterProvider(directbank.NewDirectBankProvider("BCA"), 3, 30*time.Second)

	paymentSvc := service.NewPaymentService(
		txMgr,
		paymentRepo,
		inboxRepo,
		outboxRepo,
		pspConfigRepo,
		postgresResolver,
		registry,
		masterEncryptionKey,
		logger,
	)

	registrar := authclient.NewPermissionRegistrar(authServiceURL, internalServiceToken)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	permItems := []authclient.PermissionItem{
		{Name: domain.PermissionPaymentsRead, Description: "Allows viewing payment status"},
		{Name: domain.PermissionPaymentsCreate, Description: "Allows initiating payment flows"},
		{Name: domain.PermissionPaymentsRefund, Description: "Allows issuing payment refunds"},
		{Name: domain.PermissionPaymentsWebhook, Description: "Allows receiving payment webhooks"},
		{Name: domain.PermissionPaymentsManage, Description: "Allows managing tenant PSP configurations"},
	}

	if err := registrar.Register(ctx, "payment-service", permItems); err != nil {
		logger.Warn("permission registration deferred", "err", err)
	} else {
		logger.Info("registered domain permissions with auth-service")
	}

	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("failed to connect to rabbitmq: %v", err)
	}
	defer rmqClient.Close()

	if err := rmqClient.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		log.Fatalf("failed to declare exchange: %v", err)
	}

	outboxWorker := worker.NewOutboxWorker(outboxRepo, rmqClient, 2*time.Second, 50, logger)
	go outboxWorker.Start(context.Background())
	defer outboxWorker.Stop()

	sweeper := worker.NewExpirationSweeper(paymentSvc, 1*time.Minute, 24*time.Hour, logger)
	go sweeper.Start(context.Background())
	defer sweeper.Stop()

	orderCreatedConsumer := consumer.NewOrderCreatedConsumer(rmqClient.Channel, paymentSvc, logger)
	if err := orderCreatedConsumer.Start(context.Background()); err != nil {
		logger.Warn("failed to start order.created consumer", "err", err)
	}

	paymentHandler := handler.NewPaymentHandler(paymentSvc)

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "UP", "service": "payment-service"})
	})

	r.POST("/api/payments/webhook/:provider", paymentHandler.HandleWebhook)

	api := r.Group("/api/payments")
	api.Use(middleware.RequireJWT(jwtPubKeyPEM))
	{
		api.GET("/:id", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByID)
		api.GET("/by-order/:orderID", middleware.RequirePermission(domain.PermissionPaymentsRead), paymentHandler.GetPaymentByOrderID)
		api.PUT("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.UpdatePSPConfig)
		api.GET("/config", middleware.RequirePermission(domain.PermissionPaymentsManage), paymentHandler.GetPSPConfig)
	}


	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		logger.Info("starting payment-service HTTP server", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down payment-service...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "err", err)
	}

	logger.Info("payment-service exited cleanly")
}
