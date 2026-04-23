package service

import (
	"context"
	"fmt"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
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
	CreateOrder(ctx context.Context, input repository.CreateOrderInput) error
}

type OrderService struct {
	orderRepository OrderRepository
}

func NewOrderService(orderRepository OrderRepository) *OrderService {
	return &OrderService{
		orderRepository: orderRepository,
	}
}

func (s *OrderService) ListOrders(ctx context.Context) ([]domain.Order, error) {
	return s.orderRepository.ListOrders(ctx)
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
		return nil, domain.ErrTenantIDRequired
	}

	if input.CustomerID == "" {
		return nil, domain.ErrCustomerIDRequired
	}
	if input.Amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	status := input.Status
	if status == "" {
		status = "pending"
	}

	order := domain.Order{
		ID:         domain.GenerateOrderID(),
		TenantID:   tenantID,
		CustomerID: input.CustomerID,
		Status:     status,
		Amount:     input.Amount,
	}

	repoInput := repository.CreateOrderInput{
		ID:         order.ID,
		TenantID:   order.TenantID,
		CustomerID: order.CustomerID,
		Status:     order.Status,
		Amount:     order.Amount,
	}

	if err := s.orderRepository.CreateOrder(ctx, repoInput); err != nil {
		return nil, fmt.Errorf("order service: failed to create order: %w", err)
	}

	return &order, nil
}
