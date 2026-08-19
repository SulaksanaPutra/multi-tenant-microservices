package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
)

type mockOrderDBReadyPublisher struct {
	publishFunc func(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error
}

func (m *mockOrderDBReadyPublisher) PublishTenantOrderDBReady(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, evt)
	}
	return nil
}

type mockMigrationService struct {
	migrateTenantDBFunc func(ctx context.Context, dsn, schemaName string) error
}

func (m *mockMigrationService) MigrateTenantDB(ctx context.Context, dsn, schemaName string) error {
	if m.migrateTenantDBFunc != nil {
		return m.migrateTenantDBFunc(ctx, dsn, schemaName)
	}
	return nil
}

func TestGetDeliveryCount(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]any
		want    int
	}{
		{
			name:    "nil headers",
			headers: nil,
			want:    0,
		},
		{
			name:    "empty headers",
			headers: map[string]any{},
			want:    0,
		},
		{
			name:    "quorum queue x-delivery-count int",
			headers: map[string]any{"x-delivery-count": 2},
			want:    2,
		},
		{
			name:    "quorum queue x-delivery-count int32",
			headers: map[string]any{"x-delivery-count": int32(4)},
			want:    4,
		},
		{
			name:    "quorum queue x-delivery-count int64",
			headers: map[string]any{"x-delivery-count": int64(5)},
			want:    5,
		},
		{
			name: "classic queue x-death array fallback",
			headers: map[string]any{
				"x-death": []any{
					map[string]any{"count": int64(3)},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDeliveryCount(tt.headers)
			if got != tt.want {
				t.Errorf("expected delivery count %d, got %d", tt.want, got)
			}
		})
	}
}

func TestInfrastructureProvisionedConsumer_HandleDelivery(t *testing.T) {
	evtShared := domain.InfrastructureProvisionedEvent{
		EventID:    "evt-prov-1",
		TenantID:   "tenant-shared-1",
		Plan:       "shared",
		DBHost:     "shared-postgres",
		DBPort:     5432,
		DBName:     "shared_db",
		DBUser:     "postgres",
		SchemaName: "tenant_shared_1_order_db",
	}
	bodyShared, _ := json.Marshal(evtShared)

	evtDedicated := domain.InfrastructureProvisionedEvent{
		EventID:    "evt-prov-2",
		TenantID:   "tenant-dedicated-1",
		Plan:       "dedicated",
		DBHost:     "172.20.0.5",
		DBPort:     5432,
		DBName:     "tenant_dedicated_1_db",
		DBUser:     "order_user",
		SchemaName: "public",
	}
	bodyDedicated, _ := json.Marshal(evtDedicated)

	t.Run("success_shared_plan", func(t *testing.T) {
		var capturedDSN string
		migrationService := &mockMigrationService{
			migrateTenantDBFunc: func(ctx context.Context, dsn, schemaName string) error {
				capturedDSN = dsn
				return nil
			},
		}

		var publishedEvt domain.TenantOrderDBReadyEvent
		orderDBReadyPublisher := &mockOrderDBReadyPublisher{
			publishFunc: func(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error {
				publishedEvt = evt
				return nil
			},
		}

		poolReg := registry.NewPoolRegistry()
		routingReg := registry.NewRoutingRegistry()

		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{
			orderDBReadyPublisher: orderDBReadyPublisher,
			migrationService:      migrationService,
			poolRegistry:          poolReg,
			routingRegistry:       routingReg,
			sharedSecret:          "secret123",
			sharedDBPass:          "sharedpass",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}

		if publishedEvt.TenantID != "tenant-shared-1" || publishedEvt.ServiceName != "order-service" {
			t.Errorf("unexpected published ready event: %+v", publishedEvt)
		}

		rMeta, ok := routingReg.Get("tenant-shared-1")
		if !ok || rMeta.SchemaName != "tenant_shared_1_order_db" {
			t.Errorf("unexpected routing metadata in registry: %+v", rMeta)
		}
		if capturedDSN == "" {
			t.Error("expected DSN to be passed to migration service")
		}
	})

	t.Run("success_dedicated_plan_derives_password", func(t *testing.T) {
		migrationService := &mockMigrationService{}
		orderDBReadyPublisher := &mockOrderDBReadyPublisher{}
		poolReg := registry.NewPoolRegistry()
		routingReg := registry.NewRoutingRegistry()

		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{
			orderDBReadyPublisher: orderDBReadyPublisher,
			migrationService:      migrationService,
			poolRegistry:          poolReg,
			routingRegistry:       routingReg,
			sharedSecret:          "master_secret_key",
			sharedDBPass:          "postgres",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyDedicated,
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error on dedicated plan, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected dedicated plan message to be ACKed")
		}
	})

	t.Run("poison_pill_max_retries_reached", func(t *testing.T) {
		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
			Headers:      map[string]any{"x-delivery-count": 3},
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected max delivery count error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on poison pill retry limit")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false (DLQ discard) when max retries exceeded")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected json unmarshal error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for bad JSON payload")
		}
	})

	t.Run("migration_error_nacks_with_requeue", func(t *testing.T) {
		migErr := errors.New("sql migration script failed")
		migrationService := &mockMigrationService{
			migrateTenantDBFunc: func(ctx context.Context, dsn, schemaName string) error {
				return migErr
			},
		}

		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{
			migrationService: migrationService,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when migration fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on migration error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient migration failure")
		}
	})

	t.Run("publisher_error_nacks_with_requeue", func(t *testing.T) {
		migrationService := &mockMigrationService{}
		pubErr := errors.New("rabbitmq connection dropped while publishing")
		orderDBReadyPublisher := &mockOrderDBReadyPublisher{
			publishFunc: func(ctx context.Context, evt domain.TenantOrderDBReadyEvent) error {
				return pubErr
			},
		}
		poolReg := registry.NewPoolRegistry()
		routingReg := registry.NewRoutingRegistry()

		infrastructureProvisionedConsumer := &InfrastructureProvisionedConsumer{
			orderDBReadyPublisher: orderDBReadyPublisher,
			migrationService:      migrationService,
			poolRegistry:          poolReg,
			routingRegistry:       routingReg,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
		}

		err := infrastructureProvisionedConsumer.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when publishing fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on publisher failure")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient publishing failure")
		}
	})
}

func TestNewInfrastructureProvisionedConsumer(t *testing.T) {
	infrastructureProvisionedConsumer := NewInfrastructureProvisionedConsumer(InfrastructureProvisionedConsumerParams{
		Client:           &mockAMQPInterfaceClient{},
		Publisher:        &mockOrderDBReadyPublisher{},
		MigrationService: &mockMigrationService{},
		PoolRegistry:     registry.NewPoolRegistry(),
		RoutingRegistry:  registry.NewRoutingRegistry(),
		SharedSecret:     "secret",
		SharedDBPass:     "postgres",
	})
	if infrastructureProvisionedConsumer == nil {
		t.Fatal("expected non-nil consumer")
	}
}
