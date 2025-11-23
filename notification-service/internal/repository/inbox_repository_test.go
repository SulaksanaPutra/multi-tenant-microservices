package repository

import (
	"testing"

	"notification-service/internal/infrastructure/postgres"
)

func TestInboxRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewInboxRepository(client)
	if repo == nil {
		t.Fatal("expected NewInboxRepository to return a non-nil struct pointer")
	}
}
