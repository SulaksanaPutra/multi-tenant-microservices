package testutil

import (
	"testing"
)

type ProvisionerDummyStruct struct {
	ResourceID string `json:"resource_id"`
	Active     bool   `json:"active"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := ProvisionerDummyStruct{
		ResourceID: "container-123",
		Active:     true,
	}

	output := AssertJSONRoundtrip(t, input)

	if output.ResourceID != input.ResourceID || output.Active != input.Active {
		t.Errorf("roundtrip output mismatch: got %+v, expected %+v", output, input)
	}
}
