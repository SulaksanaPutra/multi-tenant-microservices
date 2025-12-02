package publisher

import (
	"context"
	"fmt"
	"log"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"
)

type UserPublisher struct {
	client *rabbitmq.Client
}

func NewUserPublisher(client *rabbitmq.Client) (*UserPublisher, error) {
	if err := client.DeclareExchange(domain.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange for user publisher: %w", err)
	}
	return &UserPublisher{client: client}, nil
}

func (p *UserPublisher) PublishUserCreated(ctx context.Context, evt domain.UserCreatedEvent) error {
	if err := p.client.PublishEvent(ctx, domain.ExchangeCompanyEvents, domain.RoutingKeyUserCreated, evt); err != nil {
		return fmt.Errorf("failed to publish UserCreated event: %w", err)
	}
	log.Printf("UserPublisher: Published UserCreated for user_id='%s' email='%s'", evt.UserID, evt.Email)
	return nil
}
