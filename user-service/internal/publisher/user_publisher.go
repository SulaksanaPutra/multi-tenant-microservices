package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
	"user-service/internal/infrastructure/rabbitmq"
)

const (
	ExchangeCompanyEvents    = "company.events"
	RoutingKeyUserRegistered = "user.registered"
)

type UserRegisteredEvent struct {
	UserID     string `json:"user_id"`
	TenantID   string `json:"tenant_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

type UserEventPublisher interface {
	PublishUserRegistered(ctx context.Context, evt UserRegisteredEvent) error
}

type RabbitMQUserPublisher struct {
	client *rabbitmq.Client
}

func NewUserPublisher(client *rabbitmq.Client) (*RabbitMQUserPublisher, error) {
	if err := client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for user publisher: %w", err)
	}

	return &RabbitMQUserPublisher{
		client: client,
	}, nil
}

func (p *RabbitMQUserPublisher) PublishUserRegistered(ctx context.Context, evt UserRegisteredEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal UserRegistered event: %w", err)
	}

	err = p.client.Channel.PublishWithContext(
		ctx,
		ExchangeCompanyEvents,    // exchange
		RoutingKeyUserRegistered, // routing key
		false,                    // mandatory
		false,                    // immediate
		amqp.Publishing{
			ContentType: "application/json",
			Body:        body,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish UserRegistered event: %w", err)
	}

	log.Printf("Published UserRegistered event for user_id='%s', tenant_id='%s'", evt.UserID, evt.TenantID)
	return nil
}
