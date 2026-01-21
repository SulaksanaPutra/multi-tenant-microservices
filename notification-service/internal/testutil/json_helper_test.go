package testutil

import (
	"testing"
)

type NotificationDummyStruct struct {
	Event string `json:"event"`
	Count int    `json:"count"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := NotificationDummyStruct{
		Event: "user_registered",
		Count: 5,
	}

	output := AssertJSONRoundtrip(t, input)

	if output.Event != input.Event || output.Count != input.Count {
		t.Errorf("roundtrip output mismatch: got %+v, expected %+v", output, input)
	}
}
