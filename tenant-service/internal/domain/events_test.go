package domain_test

import (
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/testutil"
)

func TestWorkspaceInitiatedEvent_JSON(t *testing.T) {
	evt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt_1",
		TenantID:   "tnt_1",
		Plan:       "shared",
		OwnerEmail: "owner@test.com",
		OwnerName:  "Owner",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("WorkspaceInitiatedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestWorkspaceReadyEvent_JSON(t *testing.T) {
	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt_2",
		TenantID:   "tnt_1",
		OwnerEmail: "owner@test.com",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("WorkspaceReadyEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestTenantOrderDBReadyEvent_JSON(t *testing.T) {
	evt := domain.TenantOrderDBReadyEvent{
		EventID:     "evt_3",
		TenantID:    "tnt_1",
		ServiceName: "order-service",
		DBHost:      "localhost",
		DBPort:      5432,
		DBName:      "testdb",
		DBUser:      "postgres",
		SchemaName:  "tenant_schema",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("TenantOrderDBReadyEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestInfraChangedEvent_JSON(t *testing.T) {
	evt := domain.InfraChangedEvent{
		EventID:  "evt_4",
		TenantID: "tnt_1",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("InfraChangedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}
