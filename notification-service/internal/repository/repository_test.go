package repository

import (
	"testing"
	"notification-service/internal/infrastructure/postgres"
)

func TestRepositories_Constructors(t *testing.T) {
	client := &postgres.Client{}

	notifRepo := NewNotificationRepository(client)
	if notifRepo == nil {
		t.Fatal("expected NewNotificationRepository to return non-nil struct pointer")
	}

	inboxRepo := NewInboxRepository(client)
	if inboxRepo == nil {
		t.Fatal("expected NewInboxRepository to return non-nil struct pointer")
	}
}
