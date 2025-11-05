package domain

type Order struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     string
	Amount     float64
}
