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
	publishTenantMigrationFailedFunc     func(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
}

func (m *mockInfrastructureEventPublisher) PublishInfrastructureProvisioned(ctx context.Context, evt domain.InfrastructureProvisionedEvent) error {
	if m.publishInfrastructureProvisionedFunc != nil {
		return m.publishInfrastructureProvisionedFunc(ctx, evt)
	}
	return nil
}

func (m *mockInfrastructureEventPublisher) PublishTenantMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error {
	if m.publishTenantMigrationFailedFunc != nil {
		return m.publishTenantMigrationFailedFunc(ctx, evt)
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

type mockMigrator struct {
	checkSchemaExistsFn     func(ctx context.Context, dsn, schemaName string) (bool, error)
	lockSchemaFn            func(ctx context.Context, sharedDSN, schemaName, lockedSchemaName string) error
	restoreSchemaFn         func(ctx context.Context, sharedDSN, lockedSchemaName, originalSchemaName string) error
	migrateDataFn           func(ctx context.Context, sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, sourceSchema string, targetHost string, targetPort int, targetUser, targetPass, targetDB, targetSchema string) error
	tableHasRowsFn          func(ctx context.Context, dsn, schema, table string) (bool, error)
	dropSchemaIfExistsFn    func(ctx context.Context, dsn, schemaName string) error
	provisionTenantDBFn     func(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName, rolePass string) error
	dropTenantDBFn          func(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName string) error
	destroyContainerFn      func(ctx context.Context, containerName string) error
}

func (m *mockMigrator) CheckSchemaExists(ctx context.Context, dsn, schemaName string) (bool, error) {
	if m.checkSchemaExistsFn != nil {
		return m.checkSchemaExistsFn(ctx, dsn, schemaName)
	}
	return false, nil
}

func (m *mockMigrator) LockSchema(ctx context.Context, sharedDSN, schemaName, lockedSchemaName string) error {
	if m.lockSchemaFn != nil {
		return m.lockSchemaFn(ctx, sharedDSN, schemaName, lockedSchemaName)
	}
	return nil
}

func (m *mockMigrator) RestoreSchema(ctx context.Context, sharedDSN, lockedSchemaName, originalSchemaName string) error {
	if m.restoreSchemaFn != nil {
		return m.restoreSchemaFn(ctx, sharedDSN, lockedSchemaName, originalSchemaName)
	}
	return nil
}

func (m *mockMigrator) MigrateData(ctx context.Context, sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, sourceSchema string, targetHost string, targetPort int, targetUser, targetPass, targetDB, targetSchema string) error {
	if m.migrateDataFn != nil {
		return m.migrateDataFn(ctx, sourceHost, sourcePort, sourceUser, sourcePass, sourceDB, sourceSchema, targetHost, targetPort, targetUser, targetPass, targetDB, targetSchema)
	}
	return nil
}

func (m *mockMigrator) TableHasRows(ctx context.Context, dsn, schema, table string) (bool, error) {
	if m.tableHasRowsFn != nil {
		return m.tableHasRowsFn(ctx, dsn, schema, table)
	}
	return false, nil
}

func (m *mockMigrator) DropSchemaIfExists(ctx context.Context, dsn, schemaName string) error {
	if m.dropSchemaIfExistsFn != nil {
		return m.dropSchemaIfExistsFn(ctx, dsn, schemaName)
	}
	return nil
}

func (m *mockMigrator) ProvisionTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName, rolePass string) error {
	if m.provisionTenantDBFn != nil {
		return m.provisionTenantDBFn(ctx, host, port, superUser, superPass, dbName, roleName, rolePass)
	}
	return nil
}

func (m *mockMigrator) DropTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName string) error {
	if m.dropTenantDBFn != nil {
		return m.dropTenantDBFn(ctx, host, port, superUser, superPass, dbName, roleName)
	}
	return nil
}

func (m *mockMigrator) DestroyContainer(ctx context.Context, containerName string) error {
	if m.destroyContainerFn != nil {
		return m.destroyContainerFn(ctx, containerName)
	}
	return nil
}

func TestWorkspaceInitiatedConsumer_SharedPlanDestroysDedicatedContainer(t *testing.T) {
	evtShared := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-shared-downgrade",
		TenantID: "tenant-acme-corp",
		Plan:     "shared",
	}
	bodyShared, _ := json.Marshal(evtShared)

	t.Run("migrates_back_then_destroys_container", func(t *testing.T) {
		var destroyedName string
		var migrated bool
		mig := &mockMigrator{
			tableHasRowsFn: func(_ context.Context, _, _, _ string) (bool, error) { return true, nil },
			migrateDataFn: func(_ context.Context, _ string, _ int, _, _, _, _ string, _ string, _ int, _, _, _, _ string) error {
				migrated = true
				return nil
			},
			destroyContainerFn: func(_ context.Context, name string) error {
				destroyedName = name
				return nil
			},
		}

		var publishedEvt domain.InfrastructureProvisionedEvent
		pub := &mockInfrastructureEventPublisher{
			publishInfrastructureProvisionedFunc: func(_ context.Context, evt domain.InfrastructureProvisionedEvent) error {
				publishedEvt = evt
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: pub,
			migrator:                     mig,
			sharedDBHost:                 "postgres-shared-host",
			sharedDBPass:                 "postgres",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyShared}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if !migrated {
			t.Error("expected MigrateData to copy data back to shared")
		}
		if destroyedName != "postgres-tenant-tenant_acme_corp" {
			t.Errorf("expected dedicated container to be destroyed, got name '%s'", destroyedName)
		}
		if publishedEvt.SchemaName != "tenant_acme_corp_order_db" {
			t.Errorf("unexpected published provisioned event: %+v", publishedEvt)
		}
	})

	t.Run("fresh_shared_registration_destroy_is_best_effort", func(t *testing.T) {
		var destroyedName string
		mig := &mockMigrator{
			tableHasRowsFn: func(_ context.Context, _, _, _ string) (bool, error) {
				return false, errors.New("connect: no such host")
			},
			destroyContainerFn: func(_ context.Context, name string) error {
				destroyedName = name
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
			migrator:                     mig,
			sharedDBHost:                 "postgres-shared-host",
			sharedDBPass:                 "postgres",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyShared}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error for fresh shared registration, got %v", err)
		}
		if destroyedName != "postgres-tenant-tenant_acme_corp" {
			t.Errorf("expected best-effort destroy attempt for dedicated container, got name '%s'", destroyedName)
		}
	})
}

func TestWorkspaceInitiatedConsumer_SameInstanceMode(t *testing.T) {
	evtShared := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-shared-same",
		TenantID: "tenant-acme-corp",
		Plan:     "shared",
	}
	bodyShared, _ := json.Marshal(evtShared)

	evtDedicated := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-dedicated-same",
		TenantID: "tenant-acme-corp",
		Plan:     "dedicated",
	}
	bodyDedicated, _ := json.Marshal(evtDedicated)

	t.Run("dedicated_provisions_tenant_database_in_shared_instance", func(t *testing.T) {
		var provisionedDB, provisionedRole string
		var migrated bool
		var migTargetHost, migTargetUser, migTargetDB, migTargetSchema string
		mig := &mockMigrator{
			checkSchemaExistsFn: func(_ context.Context, _, schema string) (bool, error) {
				return schema == "tenant_acme_corp_order_db", nil
			},
			provisionTenantDBFn: func(_ context.Context, _ string, _ int, _, _, dbName, roleName, _ string) error {
				provisionedDB = dbName
				provisionedRole = roleName
				return nil
			},
			migrateDataFn: func(_ context.Context, _ string, _ int, _, _, _, _ string, targetHost string, _ int, targetUser, _, targetDB, targetSchema string) error {
				migrated = true
				migTargetHost = targetHost
				migTargetUser = targetUser
				migTargetDB = targetDB
				migTargetSchema = targetSchema
				return nil
			},
		}

		var publishedEvt domain.InfrastructureProvisionedEvent
		pub := &mockInfrastructureEventPublisher{
			publishInfrastructureProvisionedFunc: func(_ context.Context, evt domain.InfrastructureProvisionedEvent) error {
				publishedEvt = evt
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: pub,
			migrator:                     mig,
			sharedDBHost:                 "postgres-shared-host",
			sharedDBPass:                 "postgres",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
			isolationMode:                "same_instance",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyDedicated}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if provisionedDB != "tenant_acme_corp_order_db" {
			t.Errorf("expected tenant database to be provisioned, got '%s'", provisionedDB)
		}
		if provisionedRole != "tenant_acme_corp_order_user" {
			t.Errorf("expected per-tenant role, got '%s'", provisionedRole)
		}
		if !migrated {
			t.Error("expected data migration to run into the same-instance database")
		}
		if migTargetHost != "postgres-shared-host" || migTargetDB != "tenant_acme_corp_order_db" ||
			migTargetUser != "tenant_acme_corp_order_user" || migTargetSchema != "public" {
			t.Errorf("expected migration to target the per-tenant database, got host='%s' db='%s' user='%s' schema='%s'",
				migTargetHost, migTargetDB, migTargetUser, migTargetSchema)
		}
		if publishedEvt.DBHost != "postgres-shared-host" || publishedEvt.DBName != "tenant_acme_corp_order_db" ||
			publishedEvt.DBUser != "tenant_acme_corp_order_user" || publishedEvt.SchemaName != "public" {
			t.Errorf("unexpected provisioned event: %+v", publishedEvt)
		}
	})

	t.Run("downgrade_migrates_back_and_drops_tenant_database", func(t *testing.T) {
		var migrated bool
		var droppedDB, droppedRole string
		mig := &mockMigrator{
			tableHasRowsFn: func(_ context.Context, _, _, _ string) (bool, error) { return true, nil },
			migrateDataFn: func(_ context.Context, _ string, _ int, _, _, _, _ string, _ string, _ int, _, _, _, _ string) error {
				migrated = true
				return nil
			},
			dropTenantDBFn: func(_ context.Context, _ string, _ int, _, _, dbName, roleName string) error {
				droppedDB = dbName
				droppedRole = roleName
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
			migrator:                     mig,
			sharedDBHost:                 "postgres-shared-host",
			sharedDBPass:                 "postgres",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
			isolationMode:                "same_instance",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyShared}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !migrated {
			t.Error("expected data to be migrated back to shared")
		}
		if droppedDB != "tenant_acme_corp_order_db" || droppedRole != "tenant_acme_corp_order_user" {
			t.Errorf("expected tenant database to be dropped, got db='%s' role='%s'", droppedDB, droppedRole)
		}
	})

	t.Run("downgrade_fresh_shared_drop_tenant_database_best_effort", func(t *testing.T) {
		var droppedDB string
		mig := &mockMigrator{
			tableHasRowsFn: func(_ context.Context, _, _, _ string) (bool, error) {
				return false, errors.New("connect: no such database")
			},
			dropTenantDBFn: func(_ context.Context, _ string, _ int, _, _, dbName, _ string) error {
				droppedDB = dbName
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
			migrator:                     mig,
			sharedDBHost:                 "postgres-shared-host",
			sharedDBPass:                 "postgres",
			domainSecrets:                map[string]string{"order_db": "secret_key"},
			isolationMode:                "same_instance",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyShared}

		if err := c.handleDelivery(context.Background(), d); err != nil {
			t.Fatalf("expected no error for fresh shared registration, got %v", err)
		}
		if droppedDB != "tenant_acme_corp_order_db" {
			t.Errorf("expected best-effort drop of tenant database, got '%s'", droppedDB)
		}
	})
}

func TestWorkspaceInitiatedConsumer_ContainerModeDoesNotProvisionTenantDatabase(t *testing.T) {
	evtDedicated := domain.WorkspaceInitiatedEvent{
		EventID:  "evt-ws-dedicated-container",
		TenantID: "tenant-acme-corp",
		Plan:     "dedicated",
	}
	bodyDedicated, _ := json.Marshal(evtDedicated)

	provisionedCalled := false
	mig := &mockMigrator{
		provisionTenantDBFn: func(_ context.Context, _ string, _ int, _, _, _, _, _ string) error {
			provisionedCalled = true
			return nil
		},
	}

	prov := &mockProvisioner{
		provisionDedicatedContainerFunc: func(_ context.Context, _, _ string, _ map[string]string) (string, int, string, string, error) {
			return "172.20.0.30", 5432, "tenant_db", "order_user", nil
		},
	}

	c := &WorkspaceInitiatedConsumer{
		infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
		provisioner:                  prov,
		migrator:                     mig,
		sharedDBHost:                 "postgres-shared-host",
		sharedDBPass:                 "postgres",
		domainSecrets:                map[string]string{"order_db": "secret_key"},
		isolationMode:                "container",
	}

	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: bodyDedicated}

	if err := c.handleDelivery(context.Background(), d); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if provisionedCalled {
		t.Error("container mode must not provision a same-instance tenant database")
	}
	if !mockAck.ackCalled {
		t.Error("expected message to be ACKed")
	}
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

	t.Run("max_delivery_count_nacks_without_requeue_for_dlq", func(t *testing.T) {
		c := &WorkspaceInitiatedConsumer{
			infrastructureEventPublisher: &mockInfrastructureEventPublisher{},
			provisioner:                  &mockProvisioner{},
			migrator:                     &mockMigrator{},
			sharedDBHost:                 "postgres",
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         bodyShared,
			Headers: map[string]interface{}{
				"x-delivery-count": 3,
			},
		}

		if err := c.handleDelivery(context.Background(), d); err == nil {
			t.Error("expected max delivery count error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for DLQ")
		}
	})
}

func TestNewWorkspaceInitiatedConsumer(t *testing.T) {
	c := NewWorkspaceInitiatedConsumer(WorkspaceInitiatedConsumerParams{
		Client:                     &mockAMQPInterfaceClient{},
		InfrastructureEventHandler: &mockInfrastructureEventPublisher{},
		Provisioner:                &mockProvisioner{},
		Migrator:                   &mockMigrator{},
		SharedDBHost:               "postgres",
		SharedDBPass:               "postgres",
		IsolationMode:              "container",
	})
	if c == nil {
		t.Fatal("expected non-nil consumer")
	}
}
