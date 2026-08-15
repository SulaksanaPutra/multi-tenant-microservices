package handler

import (
	"context"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/service"
)

// =============================================================================
// Domain Service Contracts
// =============================================================================

// OrderService is the handler-side interface for order processing use cases.
type OrderService interface {
	ListOrders(ctx context.Context) ([]service.OrderOutput, error)
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error)
}

// OrderServiceFactory constructs an OrderService for a given tenant configuration.
// It is supplied by the composition root so Layer 1 never constructs repositories.
type OrderServiceFactory func(cfg tenantdb.Config) OrderService
