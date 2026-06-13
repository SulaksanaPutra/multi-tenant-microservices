package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID        string
	Email     string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

const (
	PrefixUser = "usr_"
)

func GenerateUserID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixUser, raw[:16])
}
