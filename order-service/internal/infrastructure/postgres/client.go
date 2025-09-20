package postgres

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/lib/pq"
)

// Client wraps *sql.DB for order-service's postgres connections.
type Client struct {
	*sql.DB
}

func NewClient(host, port, user, password, dbname string) (*Client, error) {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var database *sql.DB
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		database, err = sql.Open("postgres", dsn)
		if err != nil {
			log.Printf("postgres.NewClient: attempt %d: failed to open: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}
		if pingErr := database.Ping(); pingErr != nil {
			log.Printf("postgres.NewClient: attempt %d: failed to ping: %v", attempt, pingErr)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
			continue
		}
		break
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres after retries: %w", err)
	}

	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(5 * time.Minute)

	log.Printf("postgres.NewClient: Connected to database '%s' on %s:%s", dbname, host, port)
	return &Client{database}, nil
}

// NewClientFromDSN opens a connection from a raw DSN string and applies
// tenant-scoped connection limits to prevent pool exhaustion.
func NewClientFromDSN(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres from DSN: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping postgres from DSN: %w", err)
	}
	// Strict limits per tenant pool — prevents connection exhaustion when
	// multiple replicas each hold pools for many tenants.
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)
	return db, nil
}

func (c *Client) Close() {
	if err := c.DB.Close(); err != nil {
		log.Printf("postgres.Client.Close: error closing DB: %v", err)
	}
}
