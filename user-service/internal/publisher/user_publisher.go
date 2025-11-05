package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"

	amqp "github.com/rabbitmq/amqp091-go"
)

type UserEventPublisher interface {
	PublishUserCreated(ctx context.Context, evt domain.UserCreatedEvent) error
}

type RabbitMQUserPublisher struct {
	client *rabbitmq.Client
}

func NewUserPublisher(client *rabbitmq.Client) (*RabbitMQUserPublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for user publisher: %w", err)
	}
	return &RabbitMQUserPublisher{client: client}, nil
}

func (p *RabbitMQUserPublisher) PublishUserCreated(ctx context.Context, evt domain.UserCreatedEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal UserCreated event: %w", err)
	}
	err = p.client.Channel.PublishWithContext(
		ctx,
		domain.ExchangeCompanyEvents,
		domain.RoutingKeyUserCreated,
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
