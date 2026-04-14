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

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/authclient"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/service"
	"tenant-service/internal/txcontext"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Tenant Service (Control Plane)...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "tenant_manager_db")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	httpPort := getEnv("PORT", "8082")
	internalToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8085")

	// Register domain permissions with auth-service (non-blocking)
	permRegistrar := authclient.NewPermissionRegistrar(authServiceURL, internalToken)
	permissions := make([]authclient.PermissionItem, len(domain.TenantServicePermissions))
	for i, p := range domain.TenantServicePermissions {
		permissions[i] = authclient.PermissionItem{Name: p.Name, Description: p.Description}
	}
	go func() {
		if err := permRegistrar.Register(context.Background(), "tenant-service", permissions); err != nil {
			log.Printf("Tenant Service: Warning — startup permission registration deferred: %v", err)
		}
	}()

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

	// 2. Initialize Repositories & Transaction Manager
	txManager := txcontext.NewTxManager(dbClient.DB)
	tenantRepository := repository.NewTenantRepository(dbClient)
	tenantInfrastructureRepository := repository.NewTenantInfrastructureRepository(dbClient)
	outboxRepository := repository.NewOutboxRepository(dbClient)
	inboxRepository := repository.NewInboxRepository(dbClient)

	// 3. Initialize Publisher
	tenantPublisher, err := publisher.NewTenantPublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize tenant publisher: %v", err)
	}

	// 4. Register & Start Background Workers
	wRunner := registerWorkers(outboxRepository, tenantPublisher)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	wRunner.start(workerCtx)

	// 5. Initialize Domain Services
	inboxService := service.NewInboxService(inboxRepository)
	workspaceService := service.NewWorkspaceService(service.WorkspaceServiceParams{
		TenantRepository: tenantRepository,
		OutboxRepository: outboxRepository,
		OutboxWorker:     wRunner.OutboxWorker(),
	})
	tenantInfrastructureService := service.NewTenantInfrastructureService(service.TenantInfrastructureServiceParams{
		InfrastructureRepository: tenantInfrastructureRepository,
		WorkspaceActivator:       workspaceService,
	})

	// 6. Register & Start Inbound Queue Consumers
	cRunner, err := registerConsumers(txManager, rmqClient, tenantInfrastructureService, inboxService)
	if err != nil {
		log.Fatalf("Failed to register consumers: %v", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()
	if err := cRunner.start(consumerCtx); err != nil {
		log.Fatalf("Failed to start consumers: %v", err)
	}

	// 7. Register HTTP Router with Zero-Trust internal token check
	httpRouter := newRouter(txManager, workspaceService, tenantInfrastructureService, internalToken)
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

	// 8. Graceful Shutdown Setup
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
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			println(err.Error())
		}
	}(file)

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
