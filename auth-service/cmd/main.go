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

	"auth-service/internal/consumer"
	"auth-service/internal/crypto"
	"auth-service/internal/handler"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/migration"
	"auth-service/internal/repository"
	"auth-service/internal/service"
	"auth-service/internal/txcontext"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Auth Service...")

	// Environment variables
	dbHost := getEnv("DB_HOST", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "postgres")
	dbName := getEnv("DB_NAME", "auth_db")
	httpPort := getEnv("PORT", "8085")
	privateKeyPEM := getEnv("AUTH_JWT_PRIVATE_KEY_PEM", "")
	internalServiceToken := getEnv("INTERNAL_SERVICE_TOKEN", "default_internal_service_token")
	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")

	if privateKeyPEM == "" {
		log.Fatal("AUTH_JWT_PRIVATE_KEY_PEM environment variable is required")
	}

	// Initialize JWT manager with the RSA private key
	jwtManager, err := crypto.NewJWTManager(privateKeyPEM)
	if err != nil {
		log.Fatalf("Failed to initialize JWT manager: %v", err)
	}

	// Connect to auth DB
	dbClient, err := postgres.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	// Run schema migrations (goose, library-mode, embedded; advisory-locked)
	if err := migration.Run(context.Background(), dbClient.DB); err != nil {
		log.Fatalf("Failed to run auth DB migrations: %v", err)
	}

	// Initialize repositories
	txManager := txcontext.NewTxManager(dbClient.DB)
	credentialRepository := repository.NewCredentialRepository(dbClient)
	tokenRepository := repository.NewTokenRepository(dbClient)
	setupTokenRepository := repository.NewSetupTokenRepository(dbClient)
	permissionRepository := repository.NewPermissionRepository(dbClient)
	roleRepository := repository.NewRoleRepository(dbClient)
	inboxRepository := repository.NewInboxRepository(dbClient)

	// Initialize services
	internalPermissionService := service.NewInternalPermissionService(permissionRepository, roleRepository)
	roleService := service.NewRoleService(roleRepository)

	// Self-register the auth-owned RBAC permissions. As the Access control plane,
	// auth-service owns this namespace. Registered at boot so newly-seeded tenant
	// admin roles automatically inherit these capabilities.
	if err := internalPermissionService.RegisterPermissions(context.Background(), service.InternalRegisterPermissionsInput{
		Service: "auth-service",
		Permissions: []repository.RegisterPermissionItem{
			{Name: "auth:roles:manage", Description: "Create, update, delete and assign tenant roles"},
			{Name: "auth:roles:read", Description: "List roles, list permissions and read role assignments"},
		},
	}); err != nil {
		log.Printf("Auth Service: Warning — failed to self-register RBAC permissions: %v", err)
	}
	internalAuthService := service.NewInternalAuthService(setupTokenRepository, credentialRepository, internalPermissionService)
	authService := service.NewAuthService(credentialRepository, tokenRepository, setupTokenRepository, jwtManager, roleRepository, internalPermissionService)
	inboxService := service.NewInboxService(inboxRepository)

	// Initialize handlers
	authHandler := handler.NewAuthHandler(authService, jwtManager)
	internalAuthHandler := handler.NewInternalAuthHandler(internalAuthService)
	internalPermissionHandler := handler.NewInternalPermissionHandler(internalPermissionService)
	permissionHandler := handler.NewPermissionHandler(internalPermissionService)
	roleHandler := handler.NewRoleHandler(roleService)

	// Start HTTP server
	httpRouter := newRouter(authHandler, internalAuthHandler, internalPermissionHandler, permissionHandler, roleHandler, jwtManager, internalServiceToken)
	httpServer := &http.Server{
		Addr:    ":" + httpPort,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("Auth Service HTTP API listening on port %s...", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start the user.created membership-copy consumer (non-fatal on failure).
	rmqClient, rmqErr := rabbitmq.NewClient(amqpURL)
	if rmqErr != nil {
		log.Printf("Auth Service: Warning — failed to connect RabbitMQ (%v); user.created membership copy consumer disabled until restart.", rmqErr)
	} else {
		defer rmqClient.Close()
		userCreatedConsumer, consumerErr := consumer.NewUserCreatedConsumer(consumer.UserCreatedConsumerParams{
			TxManager:            txManager,
			Client:               rmqClient,
			InboxService:         inboxService,
			MembershipRepository: credentialRepository,
		})
		if consumerErr != nil {
			log.Printf("Auth Service: Warning — failed to initialize UserCreatedConsumer (%v); membership copy consumer disabled.", consumerErr)
		} else {
			if err := userCreatedConsumer.Start(context.Background()); err != nil {
				log.Printf("Auth Service: Warning — failed to start UserCreatedConsumer (%v); membership copy consumer disabled.", err)
			}
		}
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("Shutting down Auth Service gracefully...")
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
