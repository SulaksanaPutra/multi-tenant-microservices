package postgres

import (
	"testing"
)

func TestClient_CloseNil(t *testing.T) {
	var c *Client
	c.Close() // Should not panic
}
