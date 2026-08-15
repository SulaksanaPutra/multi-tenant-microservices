/*
 * Package e2e_test - E2E Testing Infrastructure & Unified Configuration
 * File: config_test.go
 *
 * Architectural Scope: Centralized Single-Source-of-Truth Test Configuration.
 * Objective: Dynamically resolve URLs, DSNs, ports, and environment tier configurations
 *            with standard 12-factor environment variable overrides and automatic active tier discovery.
 */

package e2e_test

import (
	"bufio"
	"crypto/rsa"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestConfig encapsulates all runtime endpoints, credentials, and database DSNs
// required across all microservice E2E integration tests.
type TestConfig struct {
	Tier                 string
	GatewayURL           string
	AuthServiceURL       string
	RabbitMQURL          string
	RabbitMQAPIURL       string
	MailpitAPIURL        string
	InternalServiceToken string

	TenantDBDSN       string
	UserDBDSN         string
	SharedDBDSN       string
	NotificationDBDSN string
	PaymentDBDSN      string
	AuthDBDSN         string

	rsaPrivateKey     *rsa.PrivateKey
	rsaPrivateKeyOnce sync.Once
}

var (
	globalConfig     *TestConfig
	globalConfigOnce sync.Once
)

// getTestConfig resolves the immutable, singleton TestConfig instance.
func getTestConfig() *TestConfig {
	globalConfigOnce.Do(func() {
		globalConfig = loadTestConfig()
	})
	return globalConfig
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadTestConfig() *TestConfig {
	tier := os.Getenv("TIER")
	if tier == "" {
		if data, err := os.ReadFile("../.active-tier"); err == nil {
			tier = strings.TrimSpace(string(data))
		}
	}
	if tier == "" {
		tier = "standard"
	}

	pgHost := getEnvDefault("PGHOST", "localhost")
	pgUser := getEnvDefault("PGUSER", "postgres")
	pgPassword := getEnvDefault("PGPASSWORD", "postgres")
	pgPort := getEnvDefault("PGPORT", "5432")

	makeDSN := func(host, port, dbName string) string {
		return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, pgUser, pgPassword, dbName)
	}

	cfg := &TestConfig{
		Tier:                 tier,
		GatewayURL:           getEnvDefault("E2E_GATEWAY_URL", "http://localhost:8000"),
		AuthServiceURL:       getEnvDefault("E2E_AUTH_SERVICE_URL", "http://localhost:8085"),
		RabbitMQURL:          getEnvDefault("E2E_RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		RabbitMQAPIURL:       getEnvDefault("E2E_RABBITMQ_API_URL", "http://localhost:15672/api/exchanges/%2F/company.events"),
		MailpitAPIURL:        getEnvDefault("E2E_MAILPIT_API_URL", "http://localhost:8025/api/v1/messages"),
		InternalServiceToken: getEnvDefault("INTERNAL_SERVICE_TOKEN", "default_internal_service_token"),
	}

	if tier == "premium" {
		tenantPort := getEnvDefault("TENANT_DB_PORT", "5433")
		userPort := getEnvDefault("USER_DB_PORT", "5434")
		sharedPort := getEnvDefault("DATA_PLANE_DB_PORT", "5435")
		authPort := getEnvDefault("AUTH_DB_PORT", "5436")
		notifPort := getEnvDefault("NOTIFICATION_DB_PORT", "5437")
		payPort := getEnvDefault("PAYMENT_DB_PORT", "5438")

		cfg.TenantDBDSN = getEnvDefault("E2E_TENANT_DB_DSN", makeDSN(pgHost, tenantPort, "tenant_manager_db"))
		cfg.UserDBDSN = getEnvDefault("E2E_USER_DB_DSN", makeDSN(pgHost, userPort, "user_db"))
		cfg.SharedDBDSN = getEnvDefault("E2E_SHARED_DB_DSN", makeDSN(pgHost, sharedPort, "shared_db"))
		cfg.AuthDBDSN = getEnvDefault("E2E_AUTH_DB_DSN", makeDSN(pgHost, authPort, "auth_db"))
		cfg.NotificationDBDSN = getEnvDefault("E2E_NOTIFICATION_DB_DSN", makeDSN(pgHost, notifPort, "notification_db"))
		cfg.PaymentDBDSN = getEnvDefault("E2E_PAYMENT_DB_DSN", makeDSN(pgHost, payPort, "payment_db"))
	} else {
		cfg.TenantDBDSN = getEnvDefault("E2E_TENANT_DB_DSN", makeDSN(pgHost, pgPort, "tenant_manager_db"))
		cfg.UserDBDSN = getEnvDefault("E2E_USER_DB_DSN", makeDSN(pgHost, pgPort, "user_db"))
		cfg.SharedDBDSN = getEnvDefault("E2E_SHARED_DB_DSN", makeDSN(pgHost, pgPort, "shared_db"))
		cfg.AuthDBDSN = getEnvDefault("E2E_AUTH_DB_DSN", makeDSN(pgHost, pgPort, "auth_db"))
		cfg.NotificationDBDSN = getEnvDefault("E2E_NOTIFICATION_DB_DSN", makeDSN(pgHost, pgPort, "notification_db"))
		cfg.PaymentDBDSN = getEnvDefault("E2E_PAYMENT_DB_DSN", makeDSN(pgHost, pgPort, "payment_db"))
	}

	return cfg
}

// RSAPrivateKey returns the parsed RSA private key cached in the config.
func (c *TestConfig) RSAPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	c.rsaPrivateKeyOnce.Do(func() {
		pemStr := os.Getenv("AUTH_JWT_PRIVATE_KEY_PEM")
		if pemStr == "" {
			paths := []string{
				"../auth-service/.env",
				"../.env",
				"./.env",
			}
			for _, p := range paths {
				absPath, err := filepath.Abs(p)
				if err != nil {
					continue
				}
				f, err := os.Open(absPath)
				if err != nil {
					continue
				}
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					line := strings.TrimSpace(scanner.Text())
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					parts := strings.SplitN(line, "=", 2)
					if len(parts) == 2 && strings.TrimSpace(parts[0]) == "AUTH_JWT_PRIVATE_KEY_PEM" {
						val := strings.TrimSpace(parts[1])
						val = strings.Trim(val, `"'`)
						val = strings.ReplaceAll(val, `\n`, "\n")
						pemStr = val
						break
					}
				}
				_ = f.Close()
				if pemStr != "" {
					break
				}
			}
		}

		if pemStr == "" {
			t.Fatalf("[TestConfig] AUTH_JWT_PRIVATE_KEY_PEM is not set and could not be loaded from .env")
		}

		key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemStr))
		if err != nil {
			t.Fatalf("[TestConfig] Failed to parse RSA private key PEM: %v", err)
		}
		c.rsaPrivateKey = key
	})

	return c.rsaPrivateKey
}

func TestTestConfig_Resolution(t *testing.T) {
	cfg := getTestConfig()
	if cfg == nil {
		t.Fatal("expected non-nil TestConfig")
	}

	if cfg.GatewayURL == "" {
		t.Error("expected non-empty GatewayURL")
	}
	if cfg.AuthServiceURL == "" {
		t.Error("expected non-empty AuthServiceURL")
	}
	if cfg.RabbitMQURL == "" {
		t.Error("expected non-empty RabbitMQURL")
	}
	if cfg.TenantDBDSN == "" {
		t.Error("expected non-empty TenantDBDSN")
	}
	if cfg.UserDBDSN == "" {
		t.Error("expected non-empty UserDBDSN")
	}
	if cfg.SharedDBDSN == "" {
		t.Error("expected non-empty SharedDBDSN")
	}

	// Verify RSA key resolution works without crashing
	key := cfg.RSAPrivateKey(t)
	if key == nil {
		t.Error("expected non-nil RSA private key")
	}
}

