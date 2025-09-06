package main

import (
	"context"

	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/worker"
)

// workerRunner manages and launches all background worker loops.
type workerRunner struct {
	outboxWorker *worker.OutboxWorker
}

// registerWorkers initializes all background outbox and maintenance workers.
func registerWorkers(
	outboxRepo repository.OutboxRepository,
	userPublisher publisher.UserEventPublisher,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(outboxRepo, userPublisher, "user.registered")

	return &workerRunner{
		outboxWorker: outboxWorker,
	}
}

// start launches all registered workers in background goroutines.
func (wr *workerRunner) start(ctx context.Context) {
	go wr.outboxWorker.Start(ctx)
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}
