package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type sampleData struct {
	Token string `json:"token"`
}

type sampleRequest struct {
	Email string `validate:"required,email"`
}

func init() {
	gin.SetMode(gin.TestMode)
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteSuccess(c, http.StatusOK, "Login successful", sampleData{Token: "abc_token"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp StandardResponse[sampleData]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "success" || resp.Message != "Login successful" || resp.Data.Token != "abc_token" {
		t.Errorf("unexpected response payload: %+v", resp)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteError(c, http.StatusUnauthorized, "invalid email or password")

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error != "invalid email or password" {
		t.Errorf("expected error 'invalid email or password', got '%s'", resp.Error)
	}
}

func TestWriteValidationError(t *testing.T) {
	t.Run("generic error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		WriteValidationError(c, errors.New("cannot decode payload"))

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		var resp ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		expected := "invalid request payload: cannot decode payload"
		if resp.Error != expected {
			t.Errorf("expected '%s', got '%s'", expected, resp.Error)
		}
	})

	t.Run("validator.ValidationErrors", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		validate := validator.New()
		err := validate.Struct(sampleRequest{Email: "invalid-email"})

		WriteValidationError(c, err)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		var resp ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Error == "" || len(resp.Error) < 18 || resp.Error[:18] != "validation failed:" {
			t.Errorf("expected validation failed message, got '%s'", resp.Error)
		}
	})
}
