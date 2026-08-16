package main

import (
	"context"
	"log/slog"
	"time"

	"payment-service/internal/infrastructure/rabbitmq"
	"payment-service/internal/repository"
	"payment-service/internal/service"
	"payment-service/internal/worker"
)

type workerRunner struct {
	outboxWorker      *worker.OutboxWorker
	expirationSweeper *worker.ExpirationSweeper
}

func registerWorkers(
	outboxRepository *repository.OutboxRepository,
	rmqClient *rabbitmq.Client,
	paymentService *service.PaymentService,
	logger *slog.Logger,
) *workerRunner {
	outboxWorker := worker.NewOutboxWorker(outboxRepository, rmqClient, 2*time.Second, 50, logger)
	sweeper := worker.NewExpirationSweeper(paymentService, 1*time.Minute, 24*time.Hour, logger)

	return &workerRunner{
		outboxWorker:      outboxWorker,
		expirationSweeper: sweeper,
	}
}

func (wr *workerRunner) start(ctx context.Context) {
	if wr.outboxWorker != nil {
		go wr.outboxWorker.Start(ctx)
	}
	if wr.expirationSweeper != nil {
		go wr.expirationSweeper.Start(ctx)
	}
}

func (wr *workerRunner) stop() {
	if wr.outboxWorker != nil {
		wr.outboxWorker.Stop()
	}
	if wr.expirationSweeper != nil {
		wr.expirationSweeper.Stop()
	}
}

func (wr *workerRunner) OutboxWorker() *worker.OutboxWorker {
	return wr.outboxWorker
}

func (wr *workerRunner) ExpirationSweeper() *worker.ExpirationSweeper {
	return wr.expirationSweeper
}
