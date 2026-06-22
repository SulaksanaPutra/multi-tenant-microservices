package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/infrastructure/rabbitmq"
)

type mockInfrastructureEventPublisher struct {
	publishInfrastructureProvisionedFunc func(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error
}

func (m *mockInfrastructureEventPublisher) PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
	if m.publishInfrastructureProvisionedFunc != nil {
		return m.publishInfrastructureProvisionedFunc(ctx, evt)
	}
	return nil
}

type mockProvisioner struct {
	provisionDedicatedContainerFunc func(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error)
}

func (m *mockProvisioner) ProvisionDedicatedContainer(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error) {
	if m.provisionDedicatedContainerFunc != nil {
		return m.provisionDedicatedContainerFunc(ctx, tenantID, infraMasterSecret, domainSecrets)
	}
	return "172.20.0.10", 5432, "tenant_db", "order_user", nil
}

type mockAcknowledger struct {
	ackCalled   bool
	nackCalled  bool
	requeueVal  bool
	multipleVal bool
}

func (m *mockAcknowledger) Ack(tag uint64, multiple bool) error {
	m.ackCalled = true
	m.multipleVal = multiple
	return nil
}

func (m *mockAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	m.nackCalled = true
	m.multipleVal = multiple
	m.requeueVal = requeue
	return nil
}

func (m *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

func TestWorkspaceInitiatedConsumer_HandleDelivery(t *testing.T) {
	evtShared := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-shared",
		TenantID: "tenant-acme-corp",
		Plan:     "shared",
	}
	bodyShared, _ := json.Marshal(evtShared)

	evtDedicated := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-dedicated",
		TenantID: "tenant-enterprise-inc",
		Plan:     "dedicated",
	}
	bodyDedicated, _ := json.Marshal(evtDedicated)

	t.Run("success_shared_plan", func(t *testing.T) {
		var publishedEvt domain.InfrastructureProvisionedEvent
		pub := &mockInfrastructureEventPublisher{
			publishInfrastructureProvisionedFunc: func(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
				publishedEvt = evt
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: pub,
			sharedDBHost:                 "postgres-shared-host",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
		}

		err := c.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}

		if publishedEvt.Plan != "shared" || publishedEvt.DBHost != "postgres-shared-host" || publishedEvt.SchemaName != "tenant_acme_corp_order_db" {
			t.Errorf("unexpected published infrastructure provisioned event: %+v", publishedEvt)
		}
	})

	t.Run("success_dedicated_plan", func(t *testing.T) {
		var capturedTenantID string
		prov := &mockProvisioner{
			provisionDedicatedContainerFunc: func(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (string, int, string, string, error) {
				capturedTenantID = tenantID
				return "172.20.0.22", 5432, "tenant_enterprise_inc_db", "order_user", nil
			},
		}

		var publishedEvt domain.InfrastructureProvisionedEvent
		pub := &mockInfrastructureEventPublisher{
			publishInfrastructureProvisionedFunc: func(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
				publishedEvt = evt
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: pub,
			provisioner:                  prov,
			infraMasterSecret:            "master_secret",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyDedicated,
		}

		err := c.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}

		if capturedTenantID != "tenant-enterprise-inc" {
			t.Errorf("expected provisioner to be called for tenant-enterprise-inc, got %s", capturedTenantID)
		}
		if publishedEvt.Plan != "dedicated" || publishedEvt.DBHost != "172.20.0.22" || publishedEvt.SchemaName != "public" {
			t.Errorf("unexpected published infrastructure event: %+v", publishedEvt)
		}
	})

	t.Run("unknown_plan_nacks_with_requeue", func(t *testing.T) {
		evtUnknown := domain.WorkspaceInitiatedEvent{
			EventID:  "evt-unknown",
			TenantID: "tenant-unknown",
			Plan:     "invalid_plan_type",
		}
		bodyUnknown, _ := json.Marshal(evtUnknown)

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyUnknown,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected unknown plan error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for unknown plan error")
		}
	})

	t.Run("provisioner_error_nacks_with_requeue", func(t *testing.T) {
		provErr := errors.New("docker daemon socket error")
		prov := &mockProvisioner{
			provisionDedicatedContainerFunc: func(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (string, int, string, string, error) {
				return "", 0, "", "", provErr
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
			provisioner:                  prov,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyDedicated,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected provisioner error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on provisioner error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient docker provisioner error")
		}
	})

	t.Run("publisher_error_nacks_with_requeue", func(t *testing.T) {
		pubErr := errors.New("failed to publish to RabbitMQ")
		pub := &mockInfrastructureEventPublisher{
			publishInfrastructureProvisionedFunc: func(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
				return pubErr
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: pub,
			sharedDBHost:                 "postgres-shared-host",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected publisher error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on publisher error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient publisher error")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &WorkspaceInitiatedConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json"),
		}

		err := c.handleDelivery(context.Background(), d)
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
}
