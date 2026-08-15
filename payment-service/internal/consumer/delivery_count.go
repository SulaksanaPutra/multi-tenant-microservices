package consumer

// getDeliveryCount extracts the broker delivery count from AMQP headers.
// Supports both quorum queues (x-delivery-count) and classic queues with DLX
// (x-death array).
func getDeliveryCount(headers map[string]interface{}) int {
	if headers == nil {
		return 0
	}

	// 1. Quorum Queues (x-delivery-count header)
	if count, ok := headers["x-delivery-count"]; ok {
		switch v := count.(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		}
	}

	// 2. Classic Queues DLX (x-death array header fallback)
	if xDeath, ok := headers["x-death"].([]interface{}); ok && len(xDeath) > 0 {
		if deathMap, ok := xDeath[0].(map[string]interface{}); ok {
			if count, ok := deathMap["count"]; ok {
				switch v := count.(type) {
				case int:
					return v
				case int32:
					return int(v)
				case int64:
					return int(v)
				}
			}
		}
	}

	return 0
}
