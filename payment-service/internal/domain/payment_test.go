package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGeneratePaymentID(t *testing.T) {
	id := GeneratePaymentID()
	if !strings.HasPrefix(id, PrefixPayment) {
		t.Errorf("Expected payment ID to start with '%s', got '%s'", PrefixPayment, id)
	}
	expectedLen := len(PrefixPayment) + 16
	if len(id) != expectedLen {
		t.Errorf("Expected payment ID length to be %d, got %d ('%s')", expectedLen, len(id), id)
	}

	id2 := GeneratePaymentID()
	if id == id2 {
		t.Errorf("GeneratePaymentID produced duplicate ID: %s", id)
	}
}

func TestGenerateAttemptID(t *testing.T) {
	id := GenerateAttemptID()
	if !strings.HasPrefix(id, PrefixAttempt) {
		t.Errorf("Expected attempt ID to start with '%s', got '%s'", PrefixAttempt, id)
	}
	expectedLen := len(PrefixAttempt) + 16
	if len(id) != expectedLen {
		t.Errorf("Expected attempt ID length to be %d, got %d ('%s')", expectedLen, len(id), id)
	}

	id2 := GenerateAttemptID()
	if id == id2 {
		t.Errorf("GenerateAttemptID produced duplicate ID: %s", id)
	}
}

func TestPaymentInstructions_JSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	instructions := PaymentInstructions{
		Type:        InstructionVirtualAccount,
		VANumber:    "88012999",
		BankCode:    "BCA",
		ExpiresAt:   now,
	}

	data, err := json.Marshal(instructions)
	if err != nil {
		t.Fatalf("Failed to marshal PaymentInstructions: %v", err)
	}

	var decoded PaymentInstructions
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal PaymentInstructions: %v", err)
	}

	if decoded.Type != InstructionVirtualAccount {
		t.Errorf("Expected type '%s', got '%s'", InstructionVirtualAccount, decoded.Type)
	}
	if decoded.VANumber != "88012999" {
		t.Errorf("Expected VA number '88012999', got '%s'", decoded.VANumber)
	}
	if decoded.BankCode != "BCA" {
		t.Errorf("Expected bank code 'BCA', got '%s'", decoded.BankCode)
	}
	if decoded.RedirectURL != "" {
		t.Errorf("Expected empty RedirectURL, got '%s'", decoded.RedirectURL)
	}
}
