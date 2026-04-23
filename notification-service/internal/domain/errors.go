package domain

import "errors"

var ErrNotFound = errors.New("domain: resource not found")
var ErrTenantIDRequired = errors.New("notification service: tenant_id is required")
var ErrEventIDRequired = errors.New("notification service: event_id is required")
