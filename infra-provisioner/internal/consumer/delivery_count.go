package consumer

func getDeliveryCount(headers map[string]interface{}) int {
	if headers == nil {
		return 0
	}

	if raw, ok := headers["x-delivery-count"]; ok {
		switch v := raw.(type) {
		case int64:
			return int(v)
		case int32:
			return int(v)
		case int:
			return v
		}
	}

	if xDeath, ok := headers["x-death"].([]interface{}); ok && len(xDeath) > 0 {
		if firstDeath, ok := xDeath[0].(map[string]interface{}); ok {
			if rawCount, ok := firstDeath["count"]; ok {
				switch v := rawCount.(type) {
				case int64:
					return int(v)
				case int32:
					return int(v)
				case int:
					return v
				}
			}
		}
	}

	return 0
}
