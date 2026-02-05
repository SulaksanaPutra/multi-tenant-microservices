package domain

import "errors"

var (
	// ErrInvalidCredentials is returned when the email/password combination is incorrect.
	ErrInvalidCredentials = errors.New("auth: invalid email or password")
	// ErrCredentialNotFound is returned when no credential record exists for an email.
	ErrCredentialNotFound = errors.New("auth: credential not found")
	// ErrTokenExpired is returned when a refresh token has passed its expiry.
	ErrTokenExpired = errors.New("auth: token has expired")
	// ErrTokenRevoked is returned when a refresh token was explicitly revoked.
	ErrTokenRevoked = errors.New("auth: token has been revoked")
	// ErrTokenNotFound is returned when the refresh token does not exist in the store.
	ErrTokenNotFound = errors.New("auth: token not found")
	// ErrEmailRequired is returned when the email field is blank.
	ErrEmailRequired = errors.New("auth: email is required")
	// ErrPasswordRequired is returned when the password field is blank.
	ErrPasswordRequired = errors.New("auth: password is required")
	// ErrUserIDRequired is returned when the user_id field is blank.
	ErrUserIDRequired = errors.New("auth: user_id is required")
)
