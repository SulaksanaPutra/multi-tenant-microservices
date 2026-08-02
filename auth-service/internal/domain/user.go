package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PrefixUser is the stable prefix for user IDs provisioned by auth-service.
const PrefixUser = "usr_"

// GenerateUserID returns a new auth-side user identifier. Invited users are
// provisioned directly in auth-service with a fresh ID (their canonical
// profile record is created by user-service on first authentication).
func GenerateUserID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixUser, raw[:16])
}
