package infrastructure

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// Client wraps *sql.DB with a convenience Close method.
type Client struct {
	*sql.DB
}

// NewClient opens a PostgreSQL connection with retry logic (10 attempts, 2s backoff).
func NewClient(host, port, user, password, dbname string) (*Client, error) {
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var database *sql.DB
	var err error

	for i := 0; i < 10; i++ {
		database, err = sql.Open("postgres", connStr)
		if err == nil {
			err = database.Ping()
			if err == nil {
				log.Println("Auth Service PostgreSQL: Connected successfully")
				return &Client{database}, nil
			}
			database.Close()
		}
		log.Printf("Auth Service PostgreSQL connection attempt %d/10 failed: %v. Retrying in 2s...", i+1, err)
		time.Sleep(2 * time.Second)
	}

	return nil, fmt.Errorf("failed to connect to PostgreSQL after retries: %w", err)
}

func (c *Client) Close() {
	if c != nil && c.DB != nil {
		c.DB.Close()
	}
}
