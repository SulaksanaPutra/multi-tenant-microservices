package consumer

import "testing"

func TestGetDeliveryCount(t *testing.T) {
	if got := getDeliveryCount(map[string]interface{}{"x-delivery-count": int64(3)}); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}

	if got := getDeliveryCount(map[string]interface{}{"x-delivery-count": int32(4)}); got != 4 {
		t.Errorf("expected 4, got %d", got)
	}

	if got := getDeliveryCount(map[string]interface{}{"x-delivery-count": 5}); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}

	classicHeaders := map[string]interface{}{
		"x-death": []interface{}{
			map[string]interface{}{
				"count": int64(2),
			},
		},
	}
	if got := getDeliveryCount(classicHeaders); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}

	if got := getDeliveryCount(nil); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}
