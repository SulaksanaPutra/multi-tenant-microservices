package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"user-service/internal/infrastructure/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	ExchangeCompanyEvents = "company.events"
	RoutingKeyUserCreated = "user.created"
)

type UserCreatedEvent struct {
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type UserEventPublisher interface {
	PublishUserCreated(ctx context.Context, evt UserCreatedEvent) error
}

type RabbitMQUserPublisher struct {
	client *rabbitmq.Client
}

func NewUserPublisher(client *rabbitmq.Client) (*RabbitMQUserPublisher, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for user publisher: %w", err)
	}
	return &RabbitMQUserPublisher{client: client}, nil
}

func (p *RabbitMQUserPublisher) PublishUserCreated(ctx context.Context, evt UserCreatedEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal UserCreated event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		ExchangeCompanyEvents,
		RoutingKeyUserCreated,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish UserCreated event: %w", err)
	}
	log.Printf("UserPublisher: Published UserCreated for user_id='%s' email='%s'", evt.UserID, evt.Email)
	return nil
}
