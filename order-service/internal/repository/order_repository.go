package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/txcontext"

	"github.com/lib/pq"
)

type CreateOrderInput struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     string
	Amount     float64
}

type OrderRepository struct {
	config tenantdb.Config
}

func NewOrderRepository(config tenantdb.Config) *OrderRepository {
	return &OrderRepository{config: config}
}

func (r *OrderRepository) ListOrders(ctx context.Context) ([]domain.Order, error) {
	if r.config.DB == nil {
		return nil, errors.New("order repository: database handle is nil")
	}

	schemaName := r.config.SchemaName
	if schemaName == "" {
		schemaName = "public"
	}

	exec := txcontext.GetExecutor(ctx, r.config.DB)

	query := fmt.Sprintf(`
		SELECT id, tenant_id, customer_id, status, amount, created_at, updated_at
		FROM %s.orders
		ORDER BY created_at DESC
		LIMIT 100;
	`, pq.QuoteIdentifier(schemaName))

	rows, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query orders from schema '%s': %w", schemaName, err)
	}
	defer func() { _ = rows.Close() }()

	var orders []domain.Order
	for rows.Next() {
		var o domain.Order
		if err := rows.Scan(&o.ID, &o.TenantID, &o.CustomerID, &o.Status, &o.Amount, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan order row: %w", err)
		}
		orders = append(orders, o)
	}
	if orders == nil {
		orders = []domain.Order{}
	}
	return orders, rows.Err()
}

func (r *OrderRepository) CreateOrder(ctx context.Context, input CreateOrderInput) error {
	if r.config.DB == nil {
		return errors.New("order repository: database handle is nil")
	}

	schemaName := r.config.SchemaName
	if schemaName == "" {
		schemaName = "public"
	}

	exec := txcontext.GetExecutor(ctx, r.config.DB)
	quotedSchema := pq.QuoteIdentifier(schemaName)

	// 1. Insert into orders table.
	orderQuery := fmt.Sprintf(`
		INSERT INTO %s.orders (id, tenant_id, customer_id, status, amount, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
	`, quotedSchema)

	if _, err := exec.ExecContext(ctx, orderQuery, input.ID, input.TenantID, input.CustomerID, input.Status, input.Amount); err != nil {
		return fmt.Errorf("failed to insert order into schema '%s': %w", schemaName, err)
	}

	// 2. Stage outbox event in the same executor (atomic dual-write).
	outboxID := domain.GenerateOutboxID()
	evt := domain.OrderCreatedEvent{
		EventID:    outboxID,
		TenantID:   input.TenantID,
		OrderID:    input.ID,
		CustomerID: input.CustomerID,
		Amount:     input.Amount,
		Status:     input.Status,
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("order repository: failed to marshal OrderCreated event payload: %w", err)
	}

	outboxQuery := fmt.Sprintf(`
		INSERT INTO %s.outbox (
			id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, retry_count
		) VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', 0);
	`, quotedSchema)

	if _, err := exec.ExecContext(ctx, outboxQuery,
		outboxID, input.TenantID, "ORDER", input.ID, domain.RoutingKeyOrderCreated, string(payload),
	); err != nil {
		return fmt.Errorf("order repository: failed to stage outbox event for order '%s': %w", input.ID, err)
	}

	return nil
}

