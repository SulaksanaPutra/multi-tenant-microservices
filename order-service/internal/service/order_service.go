package service

import (
	"context"
	"fmt"
	"time"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
)

type CreateOrderInput struct {
	TenantID   string
	CustomerID string
	Quantity   int
	Price      float64
	Currency   string
}

type OrderOutput struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     string
	Quantity   int
	Price      float64
	Amount     float64
	Currency   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func toOrderOutput(o domain.Order) OrderOutput {
	return OrderOutput{
		ID:         o.ID,
		TenantID:   o.TenantID,
		CustomerID: o.CustomerID,
		Status:     string(o.Status),
		Quantity:   o.Quantity,
		Price:      o.Price,
		Amount:     o.Amount,
		Currency:   o.Currency,
		CreatedAt:  o.CreatedAt,
		UpdatedAt:  o.UpdatedAt,
	}
}

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

func (orderService *OrderService) ListOrders(ctx context.Context) ([]OrderOutput, error) {
	orders, err := orderService.orderRepository.ListOrders(ctx)
	if err != nil {
		return nil, err
	}
	outputs := make([]OrderOutput, len(orders))
	for i, o := range orders {
		outputs[i] = toOrderOutput(o)
	}
	return outputs, nil
}

func (orderService *OrderService) CreateOrder(ctx context.Context, input CreateOrderInput) (*OrderOutput, error) {
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
	if input.Quantity <= 0 {
		return nil, domain.ErrInvalidQuantity
	}
	if input.Price <= 0 {
		return nil, domain.ErrInvalidPrice
	}
	if input.Currency == "" {
		return nil, domain.ErrCurrencyRequired
	}

	amount := float64(input.Quantity) * input.Price

	order := domain.Order{
		ID:         domain.GenerateOrderID(),
		TenantID:   tenantID,
		CustomerID: input.CustomerID,
		Status:     domain.StatusPending,
		Quantity:   input.Quantity,
		Price:      input.Price,
		Amount:     amount,
		Currency:   input.Currency,
	}

	repoInput := repository.CreateOrderInput{
		ID:         order.ID,
		TenantID:   order.TenantID,
		CustomerID: order.CustomerID,
		Status:     string(order.Status),
		Quantity:   order.Quantity,
		Price:      order.Price,
		Amount:     order.Amount,
		Currency:   order.Currency,
	}

	if err := orderService.orderRepository.CreateOrder(ctx, repoInput); err != nil {
		return nil, fmt.Errorf("order service: failed to create order: %w", err)
	}

	output := toOrderOutput(order)
	return &output, nil
}
