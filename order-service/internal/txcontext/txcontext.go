package txcontext

import (
	"context"
	"database/sql"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type DBExecutor = txcontext.DBExecutor
type SQLTxManager = txcontext.SQLTxManager

func NewTxManager(db *sql.DB) *SQLTxManager {
	return txcontext.NewTxManager(db)
}

func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return txcontext.WithTx(ctx, tx)
}

func WithExecutor(ctx context.Context, exec DBExecutor) context.Context {
	return txcontext.WithExecutor(ctx, exec)
}

func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
	return txcontext.GetExecutor(ctx, fallback)
}
