package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
)

var (
	ErrTenantIDRequired   = errors.New("order service: tenant_id is required")
	ErrCustomerIDRequired = errors.New("order service: customer_id is required")
	ErrInvalidAmount      = errors.New("order service: amount must be greater than 0")
)

type CreateOrderInput struct {
	TenantID   string
	CustomerID string
	Amount     float64
	Status     string
}

// OrderRepository is the consumer-side interface expected by OrderService.
type OrderRepository interface {
	ListOrders(ctx context.Context) ([]domain.Order, error)
	CreateOrder(ctx context.Context, order domain.Order) error
}

type OrderService struct {
	orderRepo OrderRepository
}

func NewOrderService(repo OrderRepository) *OrderService {
	return &OrderService{
		orderRepo: repo,
	}
}

func (s *OrderService) ListOrders(ctx context.Context) ([]domain.Order, error) {
	return s.orderRepo.ListOrders(ctx)
}

func (s *OrderService) CreateOrder(ctx context.Context, input CreateOrderInput) (*domain.Order, error) {
	tenantID := input.TenantID
	if tenantID == "" {
		if tID, ok := ctx.Value("tenantID").(string); ok && tID != "" {
			tenantID = tID
		} else if cfg, ok := tenantdb.FromContext(ctx); ok && cfg.TenantID != "" {
			tenantID = cfg.TenantID
		}
	}
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}

	if input.CustomerID == "" {
		return nil, ErrCustomerIDRequired
	}
	if input.Amount <= 0 {
		return nil, ErrInvalidAmount
	}

	status := input.Status
	if status == "" {
		status = "pending"
	}

	order := domain.Order{
		ID:         uuid.New().String(),
		TenantID:   tenantID,
		CustomerID: input.CustomerID,
		Status:     status,
		Amount:     input.Amount,
	}

	if err := s.orderRepo.CreateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("order service: failed to create order: %w", err)
	}

	return &order, nil
}
