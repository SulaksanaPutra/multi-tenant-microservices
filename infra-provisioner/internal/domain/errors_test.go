package domain_test

import (
	"testing"

	"infra-provisioner/internal/domain"
)

func TestErrNotFound(t *testing.T) {
	if domain.ErrNotFound == nil {
		t.Fatal("expected ErrNotFound to be non-nil")
	}
	if domain.ErrNotFound.Error() != "domain: resource not found" {
		t.Errorf("unexpected error message: %s", domain.ErrNotFound.Error())
	}
}
