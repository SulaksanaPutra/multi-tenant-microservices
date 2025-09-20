package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	PrefixUser = "usr_"
)

func GenerateUserID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixUser, raw[:16])
}
