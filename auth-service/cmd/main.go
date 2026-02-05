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

	"auth-service/internal/crypto"
	"auth-service/internal/handler"
	"auth-service/internal/infrastructure"
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

	if privateKeyPEM == "" {
		log.Fatal("AUTH_JWT_PRIVATE_KEY_PEM environment variable is required")
	}

	// Initialize JWT manager with the RSA private key
	jwtManager, err := crypto.NewJWTManager(privateKeyPEM)
	if err != nil {
		log.Fatalf("Failed to initialize JWT manager: %v", err)
	}

	// Connect to auth DB
	dbClient, err := infrastructure.NewClient(dbHost, dbPort, dbUser, dbPassword, dbName)
	if err != nil {
		log.Fatalf("Failed to initialize database client: %v", err)
	}
	defer dbClient.Close()

	// Run schema migrations
	if err := runMigrations(dbClient); err != nil {
		log.Fatalf("Failed to run auth DB migrations: %v", err)
	}

	// Initialize repositories
	_ = txcontext.NewTxManager(dbClient.DB) // available for future transactional handlers
	credentialRepository := repository.NewCredentialRepository(dbClient)
	tokenRepository := repository.NewTokenRepository(dbClient)

	// Initialize service
	authService := service.NewAuthService(credentialRepository, tokenRepository, jwtManager)

	// Initialize handler
	authHandler := handler.NewAuthHandler(authService, jwtManager)

	// Start HTTP server
	httpRouter := newRouter(authHandler, jwtManager)
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
