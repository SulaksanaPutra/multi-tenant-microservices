package domain

import "errors"

var ErrNotFound = errors.New("domain: resource not found")
var ErrTenantIDRequired = errors.New("order service: tenant_id is required")
var ErrCustomerIDRequired = errors.New("order service: customer_id is required")
var ErrInvalidAmount = errors.New("order service: amount must be greater than 0")
var ErrInvalidQuantity = errors.New("order service: quantity must be greater than 0")
var ErrInvalidPrice = errors.New("order service: price must be greater than 0")
var ErrCurrencyRequired = errors.New("order service: currency is required")
var ErrTenantMigrating = errors.New("order service: tenant database is locked for migration")
