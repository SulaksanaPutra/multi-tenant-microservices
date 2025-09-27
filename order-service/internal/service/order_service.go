package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
)

type CreateOrderInput struct {
	TenantID   string
	CustomerID string
	Amount     float64
	Status     string
}

type OrderService interface {
	GetOrders(ctx context.Context, tenantID string) ([]repository.Order, error)
	CreateOrder(ctx context.Context, input CreateOrderInput) (*repository.Order, error)
}

type orderService struct {
	dbResolver tenantdb.Resolver
	orderRepo  repository.OrderRepository
}

type OrderServiceParams struct {
	DBResolver tenantdb.Resolver
	OrderRepo  repository.OrderRepository
}

func NewOrderService(params OrderServiceParams) OrderService {
	return &orderService{
		dbResolver: params.DBResolver,
		orderRepo:  params.OrderRepo,
	}
}

func (s *orderService) GetOrders(ctx context.Context, tenantID string) ([]repository.Order, error) {
	db, schemaName, err := s.dbResolver.GetTenantDB(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	return s.orderRepo.GetOrders(ctx, db, schemaName)
}

func (s *orderService) CreateOrder(ctx context.Context, input CreateOrderInput) (*repository.Order, error) {
	if input.TenantID == "" {
		return nil, fmt.Errorf("order service: tenant_id is required")
	}
	if input.CustomerID == "" {
		return nil, fmt.Errorf("order service: customer_id is required")
	}
	if input.Amount <= 0 {
		return nil, fmt.Errorf("order service: amount must be greater than 0")
	}

	status := input.Status
	if status == "" {
		status = "pending"
	}

	db, schemaName, err := s.dbResolver.GetTenantDB(ctx, input.TenantID)
	if err != nil {
		return nil, err
	}

	order := repository.Order{
		ID:         uuid.New().String(),
		TenantID:   input.TenantID,
		CustomerID: input.CustomerID,
		Status:     status,
		Amount:     input.Amount,
	}

	if err := s.orderRepo.CreateOrder(ctx, db, schemaName, order); err != nil {
		return nil, fmt.Errorf("order service: failed to create order: %w", err)
	}

	return &order, nil
}
