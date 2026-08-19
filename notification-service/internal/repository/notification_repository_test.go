package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/testutil"
)

func TestNotificationRepository_Constructor(t *testing.T) {
	client := &postgres.Client{}
	notificationRepository := NewNotificationRepository(client)
	if notificationRepository == nil {
		t.Fatal("expected NewNotificationRepository to return a non-nil struct pointer")
	}
}

func TestNotificationRepository_CreateNotificationLog_Error(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateNotificationLogInput{
		UserID:      "usr-1",
		TenantID:    "tenant-1",
		Description: "Welcome",
		Body:        "Hello World",
		Status:      "sent",
	}

	id, err := notificationRepository.CreateNotificationLog(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if id != "" {
		t.Errorf("expected empty ID on error, got %q", id)
	}
	if !strings.Contains(err.Error(), "failed to insert notification log") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_HasSentNotification_Error(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	hasSent, err := notificationRepository.HasSentNotification(ctx, "tenant-1")
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

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	logs, err := notificationRepository.ListNotifications(ctx, "tenant-1")
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

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	logs, err := notificationRepository.ListNotifications(ctx, "")
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

func TestNotificationRepository_UpdateNotificationStatus_Success(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			if !strings.Contains(query, "UPDATE public.notifications") {
				t.Errorf("expected UPDATE query, got %s", query)
			}
			if len(args) != 2 || args[0] != "sent" || args[1] != "ntf_42" {
				t.Errorf("unexpected args: %v", args)
			}
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := notificationRepository.UpdateNotificationStatus(ctx, "ntf_42", "sent")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestNotificationRepository_UpdateNotificationStatus_ExecError(t *testing.T) {
	dbErr := errors.New("update exec error")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := notificationRepository.UpdateNotificationStatus(ctx, "ntf_42", "sent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to update notification status for id=ntf_42") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_UpdateNotificationStatus_RowsAffectedError(t *testing.T) {
	rowsErr := errors.New("rows affected error")
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedErrVal: rowsErr}, nil
		},
	}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := notificationRepository.UpdateNotificationStatus(ctx, "ntf_42", "sent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, rowsErr) {
		t.Errorf("expected underlying error to be rowsErr, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to get rows affected for notification status update id=ntf_42") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_FindPendingNotification_Error(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	pendingLog, err := notificationRepository.FindPendingNotification(ctx, "tenant-1")
	if err == nil {
		t.Fatal("expected error from QueryRowContext Scan on nil Row, got nil")
	}
	if pendingLog != nil {
		t.Errorf("expected pendingLog to be nil on error, got %v", pendingLog)
	}
	if !strings.Contains(err.Error(), "failed to query pending notification for tenant_id='tenant-1'") {
		t.Errorf("expected wrapped error message, got: %v", err)
	}
}

func TestNotificationRepository_UpdateNotificationStatus_NotFound(t *testing.T) {
	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return testutil.MockResult{RowsAffectedVal: 0}, nil
		},
	}

	notificationRepository := NewNotificationRepository(&postgres.Client{})
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	err := notificationRepository.UpdateNotificationStatus(ctx, "ntf_999", "sent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "notification log with id=ntf_999 not found for status update") {
		t.Errorf("expected not-found error message, got: %v", err)
	}
}
