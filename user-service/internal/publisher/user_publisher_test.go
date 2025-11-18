package publisher

import (
	"testing"
	"user-service/internal/infrastructure/rabbitmq"
)

func TestUserPublisher_Constructor(t *testing.T) {
	client := &rabbitmq.Client{}
	pub := &UserPublisher{client: client}
	if pub == nil {
		t.Fatal("expected UserPublisher struct pointer to be non-nil")
	}
}
