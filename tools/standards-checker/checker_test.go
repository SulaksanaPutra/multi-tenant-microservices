package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFile_Rule2_1_ConstructorConcretePointer(t *testing.T) {
	src := `package service

type UserService struct{}

func NewUserService() UserService {
	return UserService{}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for returning non-pointer struct in constructor, got %d", len(violations))
	}
	if violations[0].ID != "constructor-returns-concrete" {
		t.Errorf("Expected violation ID 'constructor-returns-concrete', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule3_1_GetReturnsCollection(t *testing.T) {
	src := `package repository

type OrderRepository struct{}

func (orderRepository *OrderRepository) GetOrders() ([]string, error) {
	return nil, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "order-service", "internal/repository/order_repository.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for Get* returning slice, got %d", len(violations))
	}
	if violations[0].ID != "get-returns-collection" {
		t.Errorf("Expected violation ID 'get-returns-collection', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule4_4_SQLInService(t *testing.T) {
	src := `package service

import "database/sql"

type UserService struct{
	db *sql.DB
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for database/sql import in service layer, got %d", len(violations))
	}
	if violations[0].ID != "sql-in-service" {
		t.Errorf("Expected violation ID 'sql-in-service', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule6_1_JSONTagInService(t *testing.T) {
	src := `package service

type CreateUserDTO struct {
	Name string ` + "`json:\"name\"`" + `
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for json tag in service layer, got %d", len(violations))
	}
	if violations[0].ID != "json-tag-in-service" {
		t.Errorf("Expected violation ID 'json-tag-in-service', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule6_1_JSONTagInRepository(t *testing.T) {
	src := `package repository

type UserRoleBrief struct {
	UserID string ` + "`json:\"user_id\"`" + `
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "role_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "auth-service", "internal/repository/role_repository.go", false, false)
	hasJSONViolation := false
	for _, v := range violations {
		if v.ID == "json-tag-in-repository" {
			hasJSONViolation = true
			break
		}
	}
	if !hasJSONViolation {
		t.Fatalf("Expected violation for json-tag-in-repository, got violations: %+v", violations)
	}
}

func TestCheckFile_Rule6_1_RepoInvalidStructNaming(t *testing.T) {
	src := `package repository

type UserRoleBrief struct {
	UserID string
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "role_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "auth-service", "internal/repository/role_repository.go", false, false)
	hasNamingViolation := false
	for _, v := range violations {
		if v.ID == "repo-invalid-struct-naming" {
			hasNamingViolation = true
			break
		}
	}
	if !hasNamingViolation {
		t.Fatalf("Expected violation for repo-invalid-struct-naming, got violations: %+v", violations)
	}
}

func TestCheckFile_Rule2_2_WorkerImportsRepository(t *testing.T) {
	src := `package worker

import "payment-service/internal/repository"

type OutboxRepository interface {
	FetchPending() ([]*repository.OutboxMessage, error)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "interfaces.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "internal/worker/interfaces.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "layer-imports-repository" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Fatalf("Expected violation for layer-imports-repository in worker, got violations: %+v", violations)
	}
}

func TestCheckFile_Rule4_1_SentinelOutsideDomain(t *testing.T) {
	src := `package service

import "errors"

var ErrUserNotFound = errors.New("user not found")
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for sentinel error outside domain, got %d", len(violations))
	}
	if violations[0].ID != "sentinel-outside-domain" {
		t.Errorf("Expected violation ID 'sentinel-outside-domain', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule2_2_StructConcreteDependency(t *testing.T) {
	src := `package handler

import "user-service/internal/service"

type UserHandler struct {
	userService *service.UserService
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_handler.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/handler/user_handler.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for struct concrete dependency, got %d", len(violations))
	}
	if violations[0].ID != "struct-concrete-dependency" {
		t.Errorf("Expected violation ID 'struct-concrete-dependency', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule7_1_HardcodedCryptoFallback(t *testing.T) {
	src := `package main

const pubKey = "-----BEGIN PUBLIC KEY-----\nsomekey\n-----END PUBLIC KEY-----"
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for hardcoded crypto fallback, got %d", len(violations))
	}
	if violations[0].ID != "hardcoded-crypto-fallback" {
		t.Errorf("Expected violation ID 'hardcoded-crypto-fallback', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule1_3_EnvNamingConvention(t *testing.T) {
	src := `package main

import "os"

func main() {
	port := os.Getenv("PAYMENT_SERVICE_PORT")
	_ = port
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "payment-service/cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for env naming convention, got %d", len(violations))
	}
	if violations[0].ID != "env-naming-convention" {
		t.Errorf("Expected violation ID 'env-naming-convention', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule3_4_AbbrevMgr(t *testing.T) {
	src := `package main

type TestStruct struct {
	txMgr string
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for abbrev-mgr, got %d", len(violations))
	}
	if violations[0].ID != "abbrev-mgr" {
		t.Errorf("Expected violation ID 'abbrev-mgr', got '%s'", violations[0].ID)
	}
}

func TestCheckDockerStandards_Rule9_HardcodedPort(t *testing.T) {
	tmpDir := t.TempDir()
	// write .dockerignore
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".env\n.git\n"), 0644)
	// write Dockerfile with cache mounts
	dfContent := `FROM golang:alpine AS builder
RUN --mount=type=cache,target=/go/pkg/mod go mod download
RUN --mount=type=cache,target=/root/.cache/go-build go build -o app
`
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dfContent), 0644)
	// write docker-compose with hardcoded port
	composeContent := `services:
  test-service:
    environment:
      PORT: 8085
      DB_HOST: ${AUTH_DB_HOST:-postgres}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte(composeContent), 0644)

	violations := checkDockerStandards(tmpDir, "auth-service", tmpDir)
	hasHardcodedPort := false
	for _, v := range violations {
		if v.ID == "hardcoded-compose-port" {
			hasHardcodedPort = true
			break
		}
	}
	if !hasHardcodedPort {
		t.Errorf("Expected violation 'hardcoded-compose-port', got violations: %+v", violations)
	}
}

func TestCheckDockerStandards_Rule9_MissingDockerignoreEnv(t *testing.T) {
	tmpDir := t.TempDir()
	// write .dockerignore without .env
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".git\n.idea\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("--mount=type=cache,target=/go/pkg/mod\n--mount=type=cache,target=/root/.cache/go-build\n"), 0644)

	violations := checkDockerStandards(tmpDir, "infra-provisioner", tmpDir)
	hasMissingEnv := false
	for _, v := range violations {
		if v.ID == "dockerignore-missing-env" {
			hasMissingEnv = true
			break
		}
	}
	if !hasMissingEnv {
		t.Errorf("Expected violation 'dockerignore-missing-env', got violations: %+v", violations)
	}
}

func TestCheckDockerStandards_Rule9_ArchetypeCStandaloneComposeProhibited(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".env\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("--mount=type=cache,target=/go/pkg/mod\n--mount=type=cache,target=/root/.cache/go-build\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte("services:\n  infra:\n"), 0644)

	violations := checkDockerStandards(tmpDir, "infra-provisioner", tmpDir)
	hasProhibitedCompose := false
	for _, v := range violations {
		if v.ID == "archetype-c-standalone-compose" {
			hasProhibitedCompose = true
			break
		}
	}
	if !hasProhibitedCompose {
		t.Errorf("Expected violation 'archetype-c-standalone-compose', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule5_8_ConsumerConstructorIO(t *testing.T) {
	src := `package consumer

type OrderConsumer struct{}

func NewOrderConsumer() (*OrderConsumer, error) {
	c := &OrderConsumer{}
	if err := c.setupTopology(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *OrderConsumer) setupTopology() error {
	return nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_consumer.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "notification-service", "internal/consumer/order_consumer.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "consumer-constructor-io" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'consumer-constructor-io', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule2_2_ConsumerConcreteRabbitMQDependency(t *testing.T) {
	src := `package consumer

import "notification-service/internal/infrastructure/rabbitmq"

type OrderConsumer struct {
	client *rabbitmq.Client
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_consumer.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "notification-service", "internal/consumer/order_consumer.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "struct-concrete-dependency" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'struct-concrete-dependency', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule5_7_ConsumerUnboundedRequeue(t *testing.T) {
	src := `package consumer

type Delivery struct{}

func (d Delivery) Nack(multiple, requeue bool) error {
	return nil
}

type OrderConsumer struct{}

func (c *OrderConsumer) handleDelivery(d Delivery) {
	_ = d.Nack(false, true)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_consumer.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "internal/consumer/order_consumer.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "consumer-unbounded-requeue" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'consumer-unbounded-requeue', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule5_6_ConsumerMissingInboxGuard(t *testing.T) {
	src := `package consumer

import "payment-service/internal/domain"

type OrderConsumer struct{}

func (c *OrderConsumer) handleDelivery(evt domain.OrderCreatedEvent) {
	// missing Inbox claiming
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_consumer.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "internal/consumer/order_consumer.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "consumer-missing-inbox-guard" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'consumer-missing-inbox-guard', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule5_9_ConsumerDriverImport(t *testing.T) {
	src := `package consumer

import amqp "github.com/rabbitmq/amqp091-go"

type OrderConsumer struct{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_consumer.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "notification-service", "internal/consumer/order_consumer.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "consumer-driver-import" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'consumer-driver-import', got violations: %+v", violations)
	}
}

func TestScanService_Rule5_10_MissingConsumerInterfacesFile(t *testing.T) {
	tmpDir := t.TempDir()
	consumerDir := filepath.Join(tmpDir, "internal", "consumer")
	_ = os.MkdirAll(consumerDir, 0755)
	_ = os.WriteFile(filepath.Join(consumerDir, "order_consumer.go"), []byte("package consumer\n"), 0644)
	_ = os.WriteFile(filepath.Join(consumerDir, "order_consumer_test.go"), []byte("package consumer\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-consumer-interfaces-file" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-consumer-interfaces-file', got violations: %+v", violations)
	}
}

func TestScanService_Rule5_9_MissingRabbitMQConsumeMethod(t *testing.T) {
	tmpDir := t.TempDir()
	rmqDir := filepath.Join(tmpDir, "internal", "infrastructure", "rabbitmq")
	_ = os.MkdirAll(rmqDir, 0755)
	_ = os.WriteFile(filepath.Join(rmqDir, "client.go"), []byte("package rabbitmq\ntype Client struct{}\n"), 0644)
	_ = os.WriteFile(filepath.Join(rmqDir, "client_test.go"), []byte("package rabbitmq\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-rabbitmq-consume-method" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-rabbitmq-consume-method', got violations: %+v", violations)
	}
}

func TestScanService_Rule2_3_MissingHandlerInterfacesFile(t *testing.T) {
	tmpDir := t.TempDir()
	handlerDir := filepath.Join(tmpDir, "internal", "handler")
	_ = os.MkdirAll(handlerDir, 0755)
	_ = os.WriteFile(filepath.Join(handlerDir, "order_handler.go"), []byte("package handler\n"), 0644)
	_ = os.WriteFile(filepath.Join(handlerDir, "order_handler_test.go"), []byte("package handler\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-handler-interfaces-file" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-handler-interfaces-file', got violations: %+v", violations)
	}
}

func TestScanService_Rule2_3_MissingWorkerInterfacesFile(t *testing.T) {
	tmpDir := t.TempDir()
	workerDir := filepath.Join(tmpDir, "internal", "worker")
	_ = os.MkdirAll(workerDir, 0755)
	_ = os.WriteFile(filepath.Join(workerDir, "outbox_worker.go"), []byte("package worker\n"), 0644)
	_ = os.WriteFile(filepath.Join(workerDir, "outbox_worker_test.go"), []byte("package worker\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-worker-interfaces-file" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-worker-interfaces-file', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule2_4_ServiceImportsDrivingLayer(t *testing.T) {
	src := `package service

import (
	"context"
	"order-service/internal/handler"
)

type DummyService struct{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dummy_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "order-service", "internal/service/dummy_service.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "service-imports-driving-layer" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'service-imports-driving-layer', got violations: %+v", violations)
	}
}

func TestCheckFile_Rule2_3_InlineLayer1Interface(t *testing.T) {
	src := `package handler

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}

type UserHandler struct{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_handler.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/handler/user_handler.go", false, false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "inline-layer1-interface" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'inline-layer1-interface', got violations: %+v", violations)
	}
}

func TestScanService_Rule1_4_MissingCmdConsumer(t *testing.T) {
	tmpDir := t.TempDir()
	consumerDir := filepath.Join(tmpDir, "internal", "consumer")
	_ = os.MkdirAll(consumerDir, 0755)
	_ = os.WriteFile(filepath.Join(consumerDir, "order_consumer.go"), []byte("package consumer\n"), 0644)
	_ = os.WriteFile(filepath.Join(consumerDir, "order_consumer_test.go"), []byte("package consumer\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tmpDir, "cmd"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "cmd", "main.go"), []byte("package main\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-cmd-consumer" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-cmd-consumer', got violations: %+v", violations)
	}
}

func TestScanService_Rule1_5_MissingCmdWorker(t *testing.T) {
	tmpDir := t.TempDir()
	workerDir := filepath.Join(tmpDir, "internal", "worker")
	_ = os.MkdirAll(workerDir, 0755)
	_ = os.WriteFile(filepath.Join(workerDir, "outbox_worker.go"), []byte("package worker\n"), 0644)
	_ = os.WriteFile(filepath.Join(workerDir, "outbox_worker_test.go"), []byte("package worker\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tmpDir, "cmd"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "cmd", "main.go"), []byte("package main\n"), 0644)

	violations := scanService(filepath.Dir(tmpDir), filepath.Base(tmpDir), false)
	hasViolation := false
	for _, v := range violations {
		if v.ID == "missing-cmd-worker" {
			hasViolation = true
			break
		}
	}
	if !hasViolation {
		t.Errorf("Expected violation 'missing-cmd-worker', got violations: %+v", violations)
	}
}
func TestCheckFile_Rule3_4_AbbrevTxmAndNotif(t *testing.T) {
	src := `package service

func process() {
	txm := "tx_manager"
	notifRepo := "notif_repo"
	_ = txm
	_ = notifRepo
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "auth-service", "internal/service/service.go", false, false)
	var hasTxm, hasNotif bool
	for _, v := range violations {
		if v.ID == "abbrev-txm" {
			hasTxm = true
		}
		if v.ID == "abbrev-notif" {
			hasNotif = true
		}
	}
	if !hasTxm {
		t.Errorf("Expected violation 'abbrev-txm', got: %+v", violations)
	}
	if !hasNotif {
		t.Errorf("Expected violation 'abbrev-notif', got: %+v", violations)
	}
}

func TestCheckFile_Rule3_4_AbbrevReceiver(t *testing.T) {
	src := `package repository

type UserRepository struct{}

func (r *UserRepository) FindUser() {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/repository/user_repository.go", false, false)
	hasReceiverViolation := false
	for _, v := range violations {
		if v.ID == "abbrev-receiver" {
			hasReceiverViolation = true
			break
		}
	}
	if !hasReceiverViolation {
		t.Errorf("Expected violation 'abbrev-receiver', got: %+v", violations)
	}
}

func TestCheckFile_Rule3_3_RepoSingleRowNaming(t *testing.T) {
	src := `package repository

type UserRepository struct{}

func (userRepository *UserRepository) GetUserByID(ctx context.Context, id string) (*User, error) {
	return nil, nil
}

func (userRepository *UserRepository) FindRoleByID(ctx context.Context, id string) (*Role, error) {
	return nil, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/repository/user_repository.go", false, false)
	var hasGet, hasStutter bool
	for _, v := range violations {
		if v.ID == "repo-get-method" {
			hasGet = true
		}
		if v.ID == "repo-entity-stutter" {
			hasStutter = true
		}
	}
	if !hasGet {
		t.Errorf("Expected violation 'repo-get-method', got: %+v", violations)
	}
	if !hasStutter {
		t.Errorf("Expected violation 'repo-entity-stutter', got: %+v", violations)
	}
}

func TestCheckFile_Rule3_5_ServiceSingleRowNaming(t *testing.T) {
	src := `package service

type RoleService struct{}

func (roleService *RoleService) FindByID(ctx context.Context, id string) (*RoleOutput, error) {
	return nil, nil
}

func (roleService *RoleService) GetRole(ctx context.Context, id string) (*RoleOutput, error) {
	return nil, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "role_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "auth-service", "internal/service/role_service.go", false, false)
	var hasFind, hasMissingCriteria bool
	for _, v := range violations {
		if v.ID == "service-find-method" {
			hasFind = true
		}
		if v.ID == "service-query-missing-criteria" {
			hasMissingCriteria = true
		}
	}
	if !hasFind {
		t.Errorf("Expected violation 'service-find-method', got: %+v", violations)
	}
	if !hasMissingCriteria {
		t.Errorf("Expected violation 'service-query-missing-criteria', got: %+v", violations)
	}
}

func TestCheckFile_Rule3_4_GinContextParamNaming(t *testing.T) {
	src := `package handler

import "github.com/gin-gonic/gin"

type OrderHandler struct{}

func (orderHandler *OrderHandler) CreateOrder(ginContext *gin.Context) {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_handler.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "order-service", "internal/handler/order_handler.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for *gin.Context param not named 'c', got %d: %+v", len(violations), violations)
	}
	if violations[0].ID != "handler-gin-context-naming" {
		t.Errorf("Expected violation ID 'handler-gin-context-naming', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule3_4_StdContextParamNaming(t *testing.T) {
	src := `package service

import "context"

type OrderService struct{}

func (orderService *OrderService) GetOrderByID(c context.Context, id string) (*OrderOutput, error) {
	return nil, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "order-service", "internal/service/order_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for context.Context param named 'c', got %d: %+v", len(violations), violations)
	}
	if violations[0].ID != "context-param-named-c" {
		t.Errorf("Expected violation ID 'context-param-named-c', got '%s'", violations[0].ID)
	}
}





