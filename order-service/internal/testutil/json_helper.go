package testutil

import (
	"encoding/json"
	"testing"
)

// AssertJSONRoundtrip marshals input to JSON, unmarshals into a new instance of T, and asserts zero errors.
func AssertJSONRoundtrip[T any](t *testing.T, input T) T {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal %T: %v", input, err)
	}

	var output T
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("failed to unmarshal %T: %v", input, err)
	}

	return output
}
