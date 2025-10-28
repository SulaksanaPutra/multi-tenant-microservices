package domain

type Order struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	CustomerID string  `json:"customer_id"`
	Status     string  `json:"status"`
	Amount     float64 `json:"amount"`
}
