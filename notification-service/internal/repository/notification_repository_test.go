package repository

import (
	"testing"

	"notification-service/internal/infrastructure/postgres"
)

func TestNotificationRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewNotificationRepository(client)
	if repo == nil {
		t.Fatal("expected NewNotificationRepository to return a non-nil struct pointer")
	}
}
