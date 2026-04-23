package domain

import "errors"

var ErrNotFound = errors.New("domain: resource not found")
var ErrServiceNameRequired = errors.New("tenant infrastructure service: service_name is required")
var ErrInvalidPlan = errors.New("workspace service: invalid plan, must be shared or dedicated")
var ErrTenantNotFound = errors.New("workspace service: tenant not found")
var ErrOwnerEmailRequired = errors.New("workspace service: owner_email is required")
var ErrTenantNameRequired = errors.New("workspace service: tenant_name is required")
var ErrTenantIDRequired = errors.New("workspace service: tenant_id is required")
