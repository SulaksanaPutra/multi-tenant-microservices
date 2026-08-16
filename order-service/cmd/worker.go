package main

import (
	"context"
	"fmt"

	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/publisher"
	"order-service/internal/registry"
	"order-service/internal/repository"
	"order-service/internal/worker"
)

type workerRunner struct {
	outboxWorker *worker.OutboxWorker
}

func registerWorkers(
	tenantDBResolver *tenantdb.Resolver,
	routingRegistry *registry.RoutingRegistry,
	rmqClient *rabbitmq.Client,
) (*workerRunner, error) {
	orderEventPub, err := publisher.NewOrderEventPublisher(rmqClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize OrderEventPublisher: %w", err)
	}

	outboxWorker := worker.NewOutboxWorker(
		tenantDBResolver,
		routingRegistry,
		routingRegistry,
		func(cfg tenantdb.Config) worker.OutboxRepository {
			return repository.NewOutboxRepository(cfg)
		},
		orderEventPub,
	)

	return &workerRunner{
		outboxWorker: outboxWorker,
	}, nil
}

func (wr *workerRunner) start(ctx context.Context) {
	if wr.outboxWorker != nil {
		go wr.outboxWorker.Start(ctx)
	}
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}
