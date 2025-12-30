package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/testutil"
	"notification-service/internal/txcontext"
)

func TestNotificationRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	repo := NewNotificationRepository(client)
	if repo == nil {
		t.Fatal("expected NewNotificationRepository to return a non-nil struct pointer")
	}
}

func TestNotificationRepository_CreateNotificationLog_Error(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{}

	repo := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateNotificationLogInput{
		UserID:         "usr-1",
		TenantID:       "tenant-1",
		RecipientEmail: "user@example.com",
		Subject:        "Welcome",
		Body:           "Hello World",
		Status:         "sent",
	}

	id, err := repo.CreateNotificationLog(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if id != 0 {
		t.Errorf("expected ID 0 on error, got %d", id)
	}
	if !strings.Contains(err.Error(), "failed to insert notification log") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_HasSentNotification_Error(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{}

	repo := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	hasSent, err := repo.HasSentNotification(ctx, "tenant-1")
	if err == nil {
		t.Fatal("expected error from QueryRowContext Scan on nil Row, got nil")
	}
	if hasSent {
		t.Errorf("expected hasSent to be false on error, got true")
	}
	if !strings.Contains(err.Error(), "failed to check notification status for tenant_id='tenant-1'") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_ListNotifications_WithTenant_QueryError(t *testing.T) {
	dbErr := errors.New("query notifications by tenant error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			if !strings.Contains(query, "WHERE tenant_id = $1") {
				t.Errorf("expected tenant_id query, got %s", query)
			}
			if len(args) != 1 || args[0] != "tenant-1" {
				t.Errorf("unexpected args: %v", args)
			}
			return nil, dbErr
		},
	}

	repo := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	logs, err := repo.ListNotifications(ctx, "tenant-1")
	if err == nil {
		t.Fatal("expected error when QueryContext fails, got nil")
	}
	if logs != nil {
		t.Errorf("expected nil logs slice on query error, got %v", logs)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestNotificationRepository_ListNotifications_All_QueryError(t *testing.T) {
	dbErr := errors.New("query all notifications error")
	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			if !strings.Contains(query, "LIMIT 50") {
				t.Errorf("expected LIMIT 50 query when tenantID is empty, got %s", query)
			}
			return nil, dbErr
		},
	}

	repo := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	logs, err := repo.ListNotifications(ctx, "")
	if err == nil {
		t.Fatal("expected error when QueryContext fails, got nil")
	}
	if logs != nil {
		t.Errorf("expected nil logs slice on query error, got %v", logs)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}
