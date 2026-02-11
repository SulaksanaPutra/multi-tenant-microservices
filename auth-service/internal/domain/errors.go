package domain

import "errors"

var (
	ErrInvalidCredentials = errors.New("auth service: invalid email or password")
	ErrCredentialNotFound = errors.New("auth service: credential not found")
	ErrTokenExpired       = errors.New("auth service: token has expired")
	ErrTokenRevoked       = errors.New("auth service: token has been revoked")
	ErrTokenNotFound      = errors.New("auth service: token not found")
	ErrEmailRequired      = errors.New("auth service: email is required")
	ErrPasswordRequired   = errors.New("auth service: password is required")
	ErrUserIDRequired     = errors.New("auth service: user_id is required")
)
