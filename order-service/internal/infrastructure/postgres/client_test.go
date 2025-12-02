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

	// Test non-nil struct with nil DB (prevents regression of nil dereference bug)
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

func TestNewClientFromDSN_PingFailure(t *testing.T) {
	// Invalid host will fail ping and return error cleanly
	dsn := "host=127.0.0.1 port=1 user=invalid password=invalid dbname=invalid sslmode=disable connect_timeout=1"
	db, err := NewClientFromDSN(dsn)
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatalf("expected error from NewClientFromDSN with unreachable DSN, got nil error")
	}
}
