package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type Order struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     string
	Amount     float64
}

type OrderRepository interface {
	GetOrders(ctx context.Context, db *sql.DB, schemaName string) ([]Order, error)
	CreateOrder(ctx context.Context, db *sql.DB, schemaName string, order Order) error
}

type orderRepository struct{}

func NewOrderRepository() OrderRepository {
	return &orderRepository{}
}

func (r *orderRepository) GetOrders(ctx context.Context, db *sql.DB, schemaName string) ([]Order, error) {
	query := fmt.Sprintf(`
		SELECT id, tenant_id, customer_id, status, amount
		FROM %s.orders
		ORDER BY created_at DESC
		LIMIT 100;
	`, schemaName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query orders from schema '%s': %w", schemaName, err)
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.CustomerID, &o.Status, &o.Amount); err != nil {
			return nil, fmt.Errorf("failed to scan order row: %w", err)
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *orderRepository) CreateOrder(ctx context.Context, db *sql.DB, schemaName string, order Order) error {
	query := fmt.Sprintf(`
		INSERT INTO %s.orders (id, tenant_id, customer_id, status, amount, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
	`, schemaName)

	_, err := db.ExecContext(ctx, query, order.ID, order.TenantID, order.CustomerID, order.Status, order.Amount)
	if err != nil {
		return fmt.Errorf("failed to insert order into schema '%s': %w", schemaName, err)
	}
	return nil
}
