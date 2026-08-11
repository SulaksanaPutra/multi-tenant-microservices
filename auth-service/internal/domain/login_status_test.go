package domain_test

import (
	"testing"

	"auth-service/internal/domain"
)

func TestLoginStatus_Constants(t *testing.T) {
	if domain.LoginStatusSelectWorkspace != "SELECT_WORKSPACE" {
		t.Errorf("Expected LoginStatusSelectWorkspace to be 'SELECT_WORKSPACE', got '%s'", domain.LoginStatusSelectWorkspace)
	}
}
