package main

import (
	"context"

	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/worker"
)

type workerRunner struct {
	outboxWorker *worker.OutboxWorker
}

func registerWorkers(
	outboxRepo repository.OutboxRepository,
	tenantPublisher publisher.TenantEventPublisher,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(outboxRepo, tenantPublisher)
	return &workerRunner{outboxWorker: outboxWorker}
}

func (wr *workerRunner) start(ctx context.Context) {
	go wr.outboxWorker.Start(ctx)
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}
