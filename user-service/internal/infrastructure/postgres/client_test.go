package postgres

import (
	"testing"
)

func TestClient_Close_Nil(t *testing.T) {
	client := &Client{DB: nil}
	// Calling Close on a client with nil DB should safe-noop without panic
	client.Close()
}
