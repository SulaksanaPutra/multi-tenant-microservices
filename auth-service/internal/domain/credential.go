package domain

import "time"

type Credential struct {
	UserID       string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
