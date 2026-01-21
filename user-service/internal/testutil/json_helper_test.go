package testutil

import (
	"testing"
)

type UserDummyStruct struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := UserDummyStruct{
		UserID: "usr_999",
		Email:  "user@example.com",
	}

	output := AssertJSONRoundtrip(t, input)

	if output.UserID != input.UserID || output.Email != input.Email {
		t.Errorf("roundtrip output mismatch: got %+v, expected %+v", output, input)
	}
}
