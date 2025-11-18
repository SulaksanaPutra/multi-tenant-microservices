package rabbitmq

import (
	"testing"
)

func TestClient_Close_Nil(t *testing.T) {
	client := &Client{Conn: nil, Channel: nil}
	// Calling Close on a client with nil connection and channel should safe-noop without panic
	client.Close()
}
