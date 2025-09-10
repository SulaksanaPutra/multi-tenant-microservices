package main

import (
	"context"

	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/worker"
)

type workerRunner struct {
	outboxWorker *worker.OutboxWorker
}

func registerWorkers(
	outboxRepo repository.OutboxRepository,
	userPublisher publisher.UserEventPublisher,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(worker.OutboxWorkerParams{
		OutboxRepo: outboxRepo,
		Publisher:  userPublisher,
		EventType:  "user.registered",
	})

	return &workerRunner{
		outboxWorker: outboxWorker,
	}
}

func (wr *workerRunner) start(ctx context.Context) {
	go wr.outboxWorker.Start(ctx)
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}
