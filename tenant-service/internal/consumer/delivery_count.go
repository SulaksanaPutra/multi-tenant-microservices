package consumer

func getDeliveryCount(headers map[string]interface{}) int {
	if headers == nil {
		return 0
	}

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
