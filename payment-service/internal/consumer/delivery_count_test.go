package consumer

import "testing"

func TestGetDeliveryCount(t *testing.T) {
	t.Run("nil headers returns 0", func(t *testing.T) {
		if got := getDeliveryCount(nil); got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})

	t.Run("x-delivery-count quorum queue int", func(t *testing.T) {
		headers := map[string]interface{}{
			"x-delivery-count": 2,
		}
		if got := getDeliveryCount(headers); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})

	t.Run("x-delivery-count quorum queue int64", func(t *testing.T) {
		headers := map[string]interface{}{
			"x-delivery-count": int64(5),
		}
		if got := getDeliveryCount(headers); got != 5 {
			t.Errorf("expected 5, got %d", got)
		}
	})

	t.Run("x-death classic queue DLX array", func(t *testing.T) {
		headers := map[string]interface{}{
			"x-death": []interface{}{
				map[string]interface{}{
					"count": int64(3),
				},
			},
		}
		if got := getDeliveryCount(headers); got != 3 {
			t.Errorf("expected 3, got %d", got)
		}
	})
}
