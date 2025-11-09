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
	outboxRepository *repository.OutboxRepository,
	userPublisher *publisher.UserPublisher,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(outboxRepository, userPublisher)
	return &workerRunner{outboxWorker: outboxWorker}
}

func (wr *workerRunner) start(ctx context.Context) {
	go wr.outboxWorker.Start(ctx)
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}
