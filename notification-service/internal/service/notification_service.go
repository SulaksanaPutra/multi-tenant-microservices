package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"notification-service/internal/mailer"
	"notification-service/internal/repository"
)

type SendWelcomeNotificationInput struct {
	// EventID is the unique outbox row ID carried inside every RabbitMQ message.
	// It is used as the Inbox Pattern deduplication key. The same outbox row always
	// carries the same EventID, even when re-published after a crash recovery.
	EventID  string
	UserID   string
	TenantID string
}

type NotificationService interface {
	SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error
}

type notificationService struct {
	db        *sql.DB
	repo      repository.NotificationRepository
	inboxRepo repository.InboxRepository
	mailer    *mailer.Mailer
}

func NewNotificationService(
	db *sql.DB,
	repo repository.NotificationRepository,
	inboxRepo repository.InboxRepository,
	mailer *mailer.Mailer,
) NotificationService {
	return &notificationService{
		db:        db,
		repo:      repo,
		inboxRepo: inboxRepo,
		mailer:    mailer,
	}
}

func (s *notificationService) SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error {
	// ─── Inbox Pattern Guard ────────────────────────────────────────────────────
	// Open a DB transaction. The very first operation is an INSERT of the event_id
	// into the inbox deduplication table.
	//
	// If the INSERT throws unique_violation (23505), we have already processed this
	// exact event before — it is a duplicate delivery. We roll back, return nil so
	// the consumer sends an ACK to RabbitMQ, and skip all side-effects.
	//
	// If the INSERT succeeds, this is a brand-new event. We write the audit log
	// inside the same transaction so both commits atomically. The email is sent
	// only AFTER a successful commit.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to open inbox transaction: %w", err)
	}
	defer tx.Rollback()

	isDuplicate, err := s.inboxRepo.TryInsert(ctx, tx, input.EventID)
	if err != nil {
		return fmt.Errorf("inbox guard failed: %w", err)
	}
	if isDuplicate {
		// This event_id is already committed in the inbox table.
		// Roll back (via defer), return nil → consumer ACKs the duplicate message.
		log.Printf("NotificationService: Duplicate event_id='%s' detected by Inbox guard. Skipping.", input.EventID)
		return nil
	}
	// ─────────────────────────────────────────────────────────────────────────────

	// 1. Fetch the recipient email (outside the TX — read-only query, no deadlock risk).
	userEmail, err := s.repo.GetUserEmailByID(ctx, input.UserID)
	if err != nil {
		return fmt.Errorf("failed to fetch user email for notification: %w", err)
	}

	// 2. Pre-build the email content so we can persist the audit log BEFORE sending.
	//    The audit log INSERT shares the inbox transaction, so they commit atomically.
	subject := "Welcome! Your Tenant Workspace is Ready"
	bodyText := fmt.Sprintf(
		"Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!",
		input.TenantID,
	)

	auditLog := repository.NotificationLog{
		UserID:         input.UserID,
		TenantID:       input.TenantID,
		RecipientEmail: userEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         "sent", // optimistic: we expect the send to succeed
	}
	logID, dbErr := s.repo.CreateNotificationLogTx(ctx, tx, auditLog)
	if dbErr != nil {
		return fmt.Errorf("failed to persist notification audit log: %w", dbErr)
	}

	// 3. Commit the transaction (inbox INSERT + audit log INSERT).
	//    After this line, the event_id is permanently recorded. Any future delivery
	//    of the same message will hit the Inbox guard and be silently discarded.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit notification transaction: %w", err)
	}

	log.Printf("NotificationService: Inbox committed for event_id='%s', audit log id=%d", input.EventID, logID)

	// 4. Send the email AFTER committing the transaction.
	//
	//    Crash Analysis:
	//    - If we crash HERE (after commit, before send): the inbox has the event_id.
	//      On re-delivery, the Inbox guard fires → duplicate skipped → user misses email.
	//      Trade-off: one missed email vs. infinite duplicate emails. Acceptable.
	//    - If the send fails (SMTP error): we return the error so the consumer NACKs
	//      and RabbitMQ re-queues. The NEXT delivery will hit the Inbox guard (already
	//      committed) → duplicate skipped → user still misses the email.
	//
	//    If you need a stronger "at-least-once email" guarantee, move the mailer call
	//    BEFORE the commit and accept a small duplicate-email risk on crash.
	_, _, mailErr := s.mailer.SendWelcomeEmail(userEmail, input.TenantID)
	if mailErr != nil {
		log.Printf("NotificationService: Failed to send welcome email to '%s': %v", userEmail, mailErr)
		return mailErr
	}

	log.Printf("NotificationService: Welcome email dispatched to '%s' for tenant='%s'", userEmail, input.TenantID)
	return nil
}
