package rabbitmq

import (
	"testing"
)

func TestClient_Close_Nil(t *testing.T) {
	client := &Client{Conn: nil, Channel: nil}
	client.Close()
}
