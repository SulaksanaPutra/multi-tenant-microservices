package consumer

import (
	"testing"
)

func TestGetDeliveryCount(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]any
		want    int
	}{
		{
			name:    "nil headers",
			headers: nil,
			want:    0,
		},
		{
			name:    "empty headers",
			headers: map[string]any{},
			want:    0,
		},
		{
			name:    "quorum queue x-delivery-count int",
			headers: map[string]any{"x-delivery-count": 2},
			want:    2,
		},
		{
			name:    "quorum queue x-delivery-count int32",
			headers: map[string]any{"x-delivery-count": int32(4)},
			want:    4,
		},
		{
			name:    "quorum queue x-delivery-count int64",
			headers: map[string]any{"x-delivery-count": int64(5)},
			want:    5,
		},
		{
			name: "classic queue x-death array fallback",
			headers: map[string]any{
				"x-death": []any{
					map[string]any{"count": int64(3)},
				},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDeliveryCount(tt.headers)
			if got != tt.want {
				t.Errorf("expected delivery count %d, got %d", tt.want, got)
			}
		})
	}
}
