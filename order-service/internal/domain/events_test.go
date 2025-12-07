package domain_test

import (
	"testing"

	"order-service/internal/domain"
	"order-service/internal/testutil"
)

func TestInfrastructureProvisionedEvent_JSON(t *testing.T) {
	evt := domain.InfrastructureProvisionedEvent{
		EventID:    "evt_1",
		TenantID:   "tnt_1",
		Plan:       "dedicated",
		DBHost:     "10.0.0.1",
		DBPort:     5432,
		DBName:     "orderdb",
		DBUser:     "postgres",
		SchemaName: "public",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("InfrastructureProvisionedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestTenantOrderDBReadyEvent_JSON(t *testing.T) {
	evt := domain.TenantOrderDBReadyEvent{
		EventID:     "evt_2",
		TenantID:    "tnt_1",
		ServiceName: "order-service",
		DBHost:      "10.0.0.1",
		DBPort:      5432,
		DBName:      "orderdb",
		DBUser:      "postgres",
		SchemaName:  "public",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("TenantOrderDBReadyEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestInfraChangedEvent_JSON(t *testing.T) {
	evt := domain.InfraChangedEvent{
		EventID:  "evt_3",
		TenantID: "tnt_1",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("InfraChangedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}
