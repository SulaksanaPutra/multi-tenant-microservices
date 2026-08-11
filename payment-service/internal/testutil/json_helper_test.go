package testutil

import (
	"testing"
)

type sampleStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := sampleStruct{Name: "test", Value: 42}
	output := AssertJSONRoundtrip(t, input)
	if output != input {
		t.Errorf("expected %+v, got %+v", input, output)
	}
}
