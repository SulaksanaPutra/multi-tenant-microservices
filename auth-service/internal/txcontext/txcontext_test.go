package txcontext

import (
	"context"
	"database/sql"
	"testing"

	"auth-service/internal/testutil"
)

func TestNewTxManager(t *testing.T) {
	var db *sql.DB
	mgr := NewTxManager(db)
	if mgr == nil {
		t.Fatal("expected NewTxManager to return non-nil SQLTxManager pointer")
	}
}

func TestGetExecutor_ContextFallback(t *testing.T) {
	fallback := &testutil.MockDBExecutor{}
	ctx := context.Background()

	// 1. Without transaction in context -> returns fallback executor
	exec := GetExecutor(ctx, fallback)
	if exec != fallback {
		t.Error("expected GetExecutor to return fallback executor when no tx present in context")
	}

	// 2. With tx in context -> returns tx executor
	txExec := &testutil.MockDBExecutor{}
	ctxWithTx := WithTx(ctx, (*sql.Tx)(nil))
	_ = ctxWithTx // WithTx stores nil *sql.Tx cast as DBExecutor

	// Using mock executor directly in context
	ctxWithMock := context.WithValue(ctx, execKey{}, DBExecutor(txExec))
	execFromCtx := GetExecutor(ctxWithMock, fallback)
	if execFromCtx != txExec {
		t.Error("expected GetExecutor to return DBExecutor from context when present")
	}
}
