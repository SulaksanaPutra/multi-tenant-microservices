package domain_test

import (
	"testing"

	"infra-provisioner/internal/domain"
	"infra-provisioner/internal/testutil"
)

func TestWorkspaceInitiatedEvent_JSON(t *testing.T) {
	initEvt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt_1",
		TenantID:   "tnt_1",
		Plan:       "dedicated",
		OwnerEmail: "owner@test.com",
		OwnerName:  "Owner",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, initEvt)
	if unmarshaled != initEvt {
		t.Errorf("WorkspaceInitiatedEvent mismatch: %+v vs %+v", unmarshaled, initEvt)
	}
}

func TestInfrastructureProvisionedEvent_JSON(t *testing.T) {
	infraEvt := domain.InfrastructureProvisionedEvent{
		EventID:    "evt_2",
		TenantID:   "tnt_1",
		Plan:       "dedicated",
		DBHost:     "10.0.0.5",
		DBPort:     5432,
		DBName:     "orderdb",
		DBUser:     "postgres",
		SchemaName: "public",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, infraEvt)
	if unmarshaled != infraEvt {
		t.Errorf("InfrastructureProvisionedEvent mismatch: %+v vs %+v", unmarshaled, infraEvt)
	}
}
