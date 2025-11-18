package repository

import (
	"testing"
	"user-service/internal/infrastructure/postgres"
)

func TestRepositories_Constructors(t *testing.T) {
	client := &postgres.Client{}

	userRepo := NewUserRepository(client)
	if userRepo == nil {
		t.Fatal("expected NewUserRepository to return a non-nil struct pointer")
	}

	inboxRepo := NewInboxRepository(client)
	if inboxRepo == nil {
		t.Fatal("expected NewInboxRepository to return a non-nil struct pointer")
	}

	outboxRepo := NewOutboxRepository(client)
	if outboxRepo == nil {
		t.Fatal("expected NewOutboxRepository to return a non-nil struct pointer")
	}
}
