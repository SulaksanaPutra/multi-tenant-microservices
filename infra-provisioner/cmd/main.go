package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"infra-provisioner/internal/consumer"
	"infra-provisioner/internal/docker"
	"infra-provisioner/internal/infrastructure/rabbitmq"
	"infra-provisioner/internal/publisher"
)

func main() {
	loadEnv(".env")
	log.Println("Starting Infra Provisioner Microservice...")

	amqpURL := getSecret("RABBITMQ_URL", "/run/secrets/rabbitmq_url", "amqp://guest:guest@rabbitmq:5672/")
	sharedDBHost := getEnv("SHARED_DB_HOST", "postgres")
	dockerNetwork := getEnv("DOCKER_NETWORK", "microservice-network")

	infraMasterSecret := getSecret("INFRA_MASTER_SECRET", "/run/secrets/infra_master_secret", getEnv("SHARED_DB_SECRET", "default_infra_master_secret_key"))
	domainSecretsRaw := getSecret("DOMAIN_SECRETS", "/run/secrets/domain_secrets.json", `{"order_db":"default_shared_db_secret_key"}`)

	domainSecrets := make(map[string]string)
	if err := json.Unmarshal([]byte(domainSecretsRaw), &domainSecrets); err != nil {
		log.Printf("InfraProvisioner Warning: Failed to parse DOMAIN_SECRETS JSON, using default order_db mapping: %v", err)
		domainSecrets["order_db"] = getEnv("SHARED_DB_SECRET", "default_shared_db_secret_key")
	}

	// 1. Initialize RabbitMQ Client
	rmqClient, err := rabbitmq.NewClient(amqpURL)
	if err != nil {
		log.Fatalf("Failed to initialize RabbitMQ client: %v", err)
	}
	defer rmqClient.Close()

	// 2. Initialize Docker Provisioner
	dockerProv, err := docker.NewDockerProvisioner(dockerNetwork)
	if err != nil {
		log.Fatalf("Failed to initialize Docker provisioner: %v", err)
	}

	// 3. Initialize Schema Migrator
	schemaMigrator, err := docker.NewSchemaMigrator()
	if err != nil {
		log.Printf("InfraProvisioner Warning: Failed to initialize SchemaMigrator (%v); migration support disabled", err)
	}

	// 4. Initialize Publisher & Consumer
	infraPub, err := publisher.NewInfrastructurePublisher(rmqClient)
	if err != nil {
		log.Fatalf("Failed to initialize InfrastructurePublisher: %v", err)
	}

	sharedDBPass := getEnv("SHARED_DB_PASSWORD", getEnv("DB_PASSWORD", "postgres"))
	isolationMode := getEnv("DEDICATED_ISOLATION_MODE", "container")

	wiConsumer, err := consumer.NewWorkspaceInitiatedConsumer(consumer.Params{
		Client:                     rmqClient,
		InfrastructureEventHandler: infraPub,
		Provisioner:                dockerProv,
		Migrator:                   schemaMigrator,
		InfraMasterSecret:          infraMasterSecret,
		DomainSecrets:              domainSecrets,
		SharedDBHost:               sharedDBHost,
		SharedDBPass:               sharedDBPass,
		IsolationMode:              isolationMode,
	})
	if err != nil {
		log.Fatalf("Failed to initialize WorkspaceInitiated consumer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := wiConsumer.Start(ctx); err != nil {
		log.Fatalf("Failed to start consumer: %v", err)
	}

	log.Println("Infra Provisioner running smoothly.")

	// 4. Graceful Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop
	log.Println("Shutting down Infra Provisioner gracefully...")
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

// getSecret checks for file-based secret mounts first (/run/secrets/...), then environment variables, then fallback.
func getSecret(envKey, secretFilePath, fallback string) string {
	// 1. Check if explicit secret file path env var exists (e.g. INFRA_MASTER_SECRET_FILE)
	fileEnvVar := envKey + "_FILE"
	if filePath, exists := os.LookupEnv(fileEnvVar); exists && strings.TrimSpace(filePath) != "" {
		if content, err := os.ReadFile(strings.TrimSpace(filePath)); err == nil {
			return strings.TrimSpace(string(content))
		}
	}

	// 2. Check default Docker secret path (/run/secrets/...)
	if secretFilePath != "" {
		if content, err := os.ReadFile(secretFilePath); err == nil {
			return strings.TrimSpace(string(content))
		}
	}

	// 3. Fallback to standard environment variable
	if value, exists := os.LookupEnv(envKey); exists && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}

	return fallback
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
