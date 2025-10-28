package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

const (
	PrefixUser = "usr_"
)

func GenerateUserID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixUser, raw[:16])
}
