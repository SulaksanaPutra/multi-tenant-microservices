package domain

import "errors"

var ErrNotFound = errors.New("domain: resource not found")
var ErrTenantIDRequired = errors.New("user service: tenant_id is required")
var ErrEmailRequired = errors.New("user service: owner_email is required")
var ErrUserIDRequired = errors.New("user service: user_id is required")
var ErrUserNameRequired = errors.New("user service: name is required")
