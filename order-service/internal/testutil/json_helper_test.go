package testutil

import (
	"testing"
)

type DummyStruct struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Valid bool   `json:"valid"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := DummyStruct{
		ID:    100,
		Name:  "Test Payload",
		Valid: true,
	}

	output := AssertJSONRoundtrip(t, input)

	if output.ID != input.ID || output.Name != input.Name || output.Valid != input.Valid {
		t.Errorf("roundtrip output mismatch: got %+v, expected %+v", output, input)
	}
}
