package main

import (
	"context"
	"fmt"
	"log/slog"

	"payment-service/internal/consumer"
	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/service"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type consumerRunner struct {
	orderCreatedConsumer *consumer.OrderCreatedConsumer
}

func registerConsumers(
	rmqClient *rabbitmq.Client,
	txManager *txcontext.SQLTxManager,
	inboxService *service.InboxService,
	paymentService *service.PaymentService,
	logger *slog.Logger,
) (*consumerRunner, error) {
	c := consumer.NewOrderCreatedConsumer(consumer.OrderCreatedConsumerParams{
		Client:         rmqClient,
		TxManager:      txManager,
		InboxService:   inboxService,
		PaymentService: paymentService,
		Logger:         logger,
	})

	return &consumerRunner{
		orderCreatedConsumer: c,
	}, nil
}

func (cr *consumerRunner) start(ctx context.Context) error {
	if cr.orderCreatedConsumer != nil {
		if err := cr.orderCreatedConsumer.Start(ctx); err != nil {
			return fmt.Errorf("failed to start OrderCreatedConsumer: %w", err)
		}
	}
	return nil
}
