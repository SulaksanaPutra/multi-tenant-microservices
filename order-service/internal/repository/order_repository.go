package repository

import (
	"context"
	"errors"
	"fmt"
	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/txcontext"

	"github.com/lib/pq"
)

type OrderRepository struct {
	cfg tenantdb.Config
}

func NewOrderRepository(cfg tenantdb.Config) *OrderRepository {
	return &OrderRepository{cfg: cfg}
}

func (r *OrderRepository) ListOrders(ctx context.Context) ([]domain.Order, error) {
	if r.cfg.DB == nil {
		return nil, errors.New("order repository: database handle is nil")
	}

	schemaName := r.cfg.SchemaName
	if schemaName == "" {
		schemaName = "public"
	}

	exec := txcontext.GetExecutor(ctx, r.cfg.DB)

	query := fmt.Sprintf(`
		SELECT id, tenant_id, customer_id, status, amount
		FROM %s.orders
		ORDER BY created_at DESC
		LIMIT 100;
	`, pq.QuoteIdentifier(schemaName))

	rows, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query orders from schema '%s': %w", schemaName, err)
	}
	defer rows.Close()

	var orders []domain.Order
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.CustomerID, &o.Status, &o.Amount); err != nil {
			return nil, fmt.Errorf("failed to scan order row: %w", err)
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *OrderRepository) CreateOrder(ctx context.Context, order domain.Order) error {
	if r.cfg.DB == nil {
		return errors.New("order repository: database handle is nil")
	}

	schemaName := r.cfg.SchemaName
	if schemaName == "" {
		schemaName = "public"
	}

	exec := txcontext.GetExecutor(ctx, r.cfg.DB)

	query := fmt.Sprintf(`
		INSERT INTO %s.orders (id, tenant_id, customer_id, status, amount, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
	`, pq.QuoteIdentifier(schemaName))

	_, err := exec.ExecContext(ctx, query, order.ID, order.TenantID, order.CustomerID, order.Status, order.Amount)
	if err != nil {
		return fmt.Errorf("failed to insert order into schema '%s': %w", schemaName, err)
	}
	return nil
}

