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
)

func main() {
	loadEnv(".env")
	log.Println("Starting Infra Provisioner Microservice...")

	amqpURL := getEnv("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/")
	sharedDBHost := getEnv("SHARED_DB_HOST", "postgres")
	infraMasterSecret := getEnv("INFRA_MASTER_SECRET", getEnv("SHARED_DB_SECRET", "default_infra_master_secret_key"))
	dockerNetwork := getEnv("DOCKER_NETWORK", "microservice-network")

	domainSecretsRaw := getEnv("DOMAIN_SECRETS", `{"order_db":"default_shared_db_secret_key"}`)
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

	// 3. Initialize & Start Consumer
	wiConsumer, err := consumer.NewWorkspaceInitiatedConsumer(consumer.WorkspaceInitiatedConsumerParams{
		Client:            rmqClient,
		Provisioner:       dockerProv,
		InfraMasterSecret: infraMasterSecret,
		DomainSecrets:     domainSecrets,
		SharedDBHost:      sharedDBHost,
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
