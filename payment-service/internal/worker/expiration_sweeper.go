package worker

import (
	"context"
	"log/slog"
	"time"
)

type PaymentSweeper interface {
	SweepExpiredPayments(ctx context.Context, ttl time.Duration) (int, error)
}

type ExpirationSweeper struct {
	paymentService PaymentSweeper
	interval       time.Duration
	ttlDuration    time.Duration
	logger         *slog.Logger
	stopChan       chan struct{}
}

func NewExpirationSweeper(
	paymentService PaymentSweeper,
	interval time.Duration,
	ttlDuration time.Duration,
	logger *slog.Logger,
) *ExpirationSweeper {
	if interval <= 0 {
		interval = 1 * time.Minute
	}
	if ttlDuration <= 0 {
		ttlDuration = 24 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ExpirationSweeper{
		paymentService: paymentService,
		interval:       interval,
		ttlDuration:    ttlDuration,
		logger:         logger,
		stopChan:       make(chan struct{}),
	}
}

func (w *ExpirationSweeper) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("starting payment expiration sweeper worker", "interval", w.interval, "ttl", w.ttlDuration)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("stopping payment expiration sweeper worker")
			return
		case <-w.stopChan:
			w.logger.Info("payment expiration sweeper stopped")
			return
		case <-ticker.C:
			count, err := w.paymentService.SweepExpiredPayments(ctx, w.ttlDuration)
			if err != nil {
				w.logger.Error("failed to sweep expired payments", "err", err)
			} else if count > 0 {
				w.logger.Info("swept expired payments successfully", "count", count)
			}
		}
	}
}

func (w *ExpirationSweeper) Stop() {
	close(w.stopChan)
}
