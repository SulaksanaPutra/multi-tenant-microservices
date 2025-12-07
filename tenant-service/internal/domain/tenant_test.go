package domain_test

import (
	"testing"
	"time"

	"tenant-service/internal/domain"
)

func TestTenant_Struct(t *testing.T) {
	now := time.Now()
	tenant := domain.Tenant{
		ID:         "tnt_1",
		Name:       "Test Tenant",
		Slug:       "test-tenant",
		OwnerEmail: "owner@test.com",
		OwnerName:  "Owner",
		Plan:       "shared",
		Status:     "active",
		CreatedAt:  now,
	}

	if tenant.ID != "tnt_1" || tenant.Slug != "test-tenant" || tenant.Plan != "shared" {
		t.Errorf("unexpected Tenant struct values: %+v", tenant)
	}
}

func TestTenantInfra_Struct(t *testing.T) {
	infra := domain.TenantInfra{
		TenantID:    "tnt_1",
		ServiceName: "order-service",
		DBHost:      "localhost",
		DBPort:      5432,
		DBName:      "testdb",
		DBUser:      "postgres",
		SchemaName:  "tnt_1_schema",
	}

	if infra.TenantID != "tnt_1" || infra.SchemaName != "tnt_1_schema" {
		t.Errorf("unexpected TenantInfra struct values: %+v", infra)
	}
}
