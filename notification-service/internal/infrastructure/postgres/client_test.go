package postgres

import (
	"testing"
)

func TestClient_Close_Nil(t *testing.T) {
	client := &Client{DB: nil}
	client.Close()
}
