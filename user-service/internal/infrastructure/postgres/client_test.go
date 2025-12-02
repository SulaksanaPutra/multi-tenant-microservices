package postgres

import (
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
)

func TestClient_Close_Nil(t *testing.T) {
	// Test nil struct pointer
	var nilClient *Client
	nilClient.Close()

	// Test non-nil struct with nil DB
	client := &Client{DB: nil}
	client.Close()
}

func TestClient_Close_Valid(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=5432 user=user password=pass dbname=test sslmode=disable")
	if err != nil {
		t.Fatalf("unexpected error opening mock db: %v", err)
	}

	client := &Client{DB: db}
	client.Close()
}
