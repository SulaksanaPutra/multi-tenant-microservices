package main

import (
	"context"

	"tenant-service/internal/middleware"
	"tenant-service/internal/publisher"
	"tenant-service/internal/repository"
	"tenant-service/internal/worker"
)

// workerRunner manages and launches all background worker loops.
type workerRunner struct {
	outboxWorker *worker.OutboxWorker
}

// registerWorkers initializes all background outbox and maintenance workers.
func registerWorkers(
	outboxRepo repository.OutboxRepository,
	tenantPublisher publisher.TenantEventPublisher,
	tenantMiddleware *middleware.TenantMiddleware,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(outboxRepo, tenantPublisher, "tenant.provisioned")
	outboxWorker.SetConnectionRegistry(tenantMiddleware)

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
