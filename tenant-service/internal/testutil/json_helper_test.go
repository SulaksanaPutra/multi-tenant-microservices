package testutil

import (
	"testing"
)

type TenantDummyStruct struct {
	Slug string `json:"slug"`
	Code int    `json:"code"`
}

func TestAssertJSONRoundtrip(t *testing.T) {
	input := TenantDummyStruct{
		Slug: "acme-corp",
		Code: 200,
	}

	output := AssertJSONRoundtrip(t, input)

	if output.Slug != input.Slug || output.Code != input.Code {
		t.Errorf("roundtrip output mismatch: got %+v, expected %+v", output, input)
	}
}
