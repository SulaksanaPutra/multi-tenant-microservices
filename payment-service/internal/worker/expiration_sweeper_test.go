package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testSweeper struct {
	sweepFn func(ctx context.Context, ttl time.Duration) (int, error)
}

func (t *testSweeper) SweepExpiredPayments(ctx context.Context, ttl time.Duration) (int, error) {
	if t.sweepFn != nil {
		return t.sweepFn(ctx, ttl)
	}
	return 0, nil
}

func TestExpirationSweeper_ConstructorDefaults(t *testing.T) {
	s := &testSweeper{}
	sweeper := NewExpirationSweeper(s, 0, 0, nil)

	if sweeper == nil {
		t.Fatal("expected NewExpirationSweeper to return non-nil struct pointer")
	}
	if sweeper.interval != 1*time.Minute {
		t.Errorf("expected default interval of 1m, got %v", sweeper.interval)
	}
	if sweeper.ttlDuration != 24*time.Hour {
		t.Errorf("expected default ttlDuration of 24h, got %v", sweeper.ttlDuration)
	}
	if sweeper.logger == nil {
		t.Error("expected logger to be initialized to default")
	}
}

func TestExpirationSweeper_Lifecycle(t *testing.T) {
	swept := false
	s := &testSweeper{
		sweepFn: func(ctx context.Context, ttl time.Duration) (int, error) {
			swept = true
			return 2, nil
		},
	}

	sweeper := NewExpirationSweeper(s, 10*time.Millisecond, 1*time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())

	go sweeper.Start(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()
	sweeper.Stop()

	if !swept {
		t.Errorf("expected SweepExpiredPayments to be called during execution loop")
	}
}

func TestExpirationSweeper_ErrorHandling(t *testing.T) {
	s := &testSweeper{
		sweepFn: func(ctx context.Context, ttl time.Duration) (int, error) {
			return 0, errors.New("db connection failure")
		},
	}

	sweeper := NewExpirationSweeper(s, 10*time.Millisecond, 1*time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())

	go sweeper.Start(ctx)
	time.Sleep(25 * time.Millisecond)
	cancel()
}
