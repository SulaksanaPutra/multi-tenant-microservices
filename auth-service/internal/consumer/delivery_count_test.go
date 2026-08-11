package consumer

import (
	"testing"
)

func TestGetDeliveryCount(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]interface{}
		expected int
	}{
		{
			name:     "nil headers",
			headers:  nil,
			expected: 0,
		},
		{
			name:     "empty headers",
			headers:  map[string]interface{}{},
			expected: 0,
		},
		{
			name: "x-delivery-count int",
			headers: map[string]interface{}{
				"x-delivery-count": 3,
			},
			expected: 3,
		},
		{
			name: "x-delivery-count int32",
			headers: map[string]interface{}{
				"x-delivery-count": int32(5),
			},
			expected: 5,
		},
		{
			name: "x-delivery-count int64",
			headers: map[string]interface{}{
				"x-delivery-count": int64(7),
			},
			expected: 7,
		},
		{
			name: "x-death fallback int64",
			headers: map[string]interface{}{
				"x-death": []interface{}{
					map[string]interface{}{
						"count": int64(2),
					},
				},
			},
			expected: 2,
		},
		{
			name: "unsupported header type",
			headers: map[string]interface{}{
				"x-delivery-count": "invalid",
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDeliveryCount(tt.headers)
			if got != tt.expected {
				t.Errorf("getDeliveryCount() = %d, want %d", got, tt.expected)
			}
		})
	}
}
