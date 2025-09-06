package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"tenant-service/internal/infrastructure/rabbitmq"
	"tenant-service/internal/publisher"
	"tenant-service/internal/service"
)

const (
	QueueTenantUserRegistered = "tenant_service_user_registered"
)

type UserRegisteredMessage struct {
	UserID     string `json:"user_id"`
	TenantID   string `json:"tenant_id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

type UserRegisteredConsumer struct {
	client             *rabbitmq.Client
	provisionerService service.ProvisionerService
}

func NewUserRegisteredConsumer(client *rabbitmq.Client, provisionerSvc service.ProvisionerService) (*UserRegisteredConsumer, error) {
	if err := client.DeclareExchange(publisher.ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}

	routingKey := "user.registered"
	if err := client.DeclareAndBindQueue(QueueTenantUserRegistered, publisher.ExchangeCompanyEvents, routingKey); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	return &UserRegisteredConsumer{
		client:             client,
		provisionerService: provisionerSvc,
	}, nil
}

func (c *UserRegisteredConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueTenantUserRegistered, // queue
		"tenant-service-worker",    // consumer tag
		false,                      // auto-ack
		false,                      // exclusive
		false,                      // no-local
		false,                      // no-wait
		nil,                        // args
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue %s: %w", QueueTenantUserRegistered, err)
	}

	log.Printf("UserRegisteredConsumer: Listening for incoming messages on queue '%s'...", QueueTenantUserRegistered)

	go func() {
		for d := range msgs {
			log.Printf("UserRegisteredConsumer: Received message from queue '%s'", QueueTenantUserRegistered)

			var msg UserRegisteredMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				log.Printf("UserRegisteredConsumer Error: Failed to unmarshal message payload: %v", err)
				d.Nack(false, false)
				continue
			}

			log.Printf("UserRegisteredConsumer: Handling user_id='%s', tenant_slug='%s'", msg.UserID, msg.TenantSlug)

			input := service.ProvisionTenantInput{
				TenantSlug: msg.TenantSlug,
				UserID:     msg.UserID,
				Name:       msg.Name,
				Email:      msg.Email,
			}

			// Delegate to ProvisionerService business layer
			_, err := c.provisionerService.ProvisionTenant(ctx, input)
			if err != nil {
				log.Printf("UserRegisteredConsumer Error: Failed to provision tenant schema: %v", err)
				d.Nack(false, true)
				continue
			}

			d.Ack(false)
		}
	}()

	return nil
}
