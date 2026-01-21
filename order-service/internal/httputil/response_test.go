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
	Name string `json:"name"`
}

type dummyPayload struct {
	Email string `validate:"required,email"`
}

func init() {
	gin.SetMode(gin.TestMode)
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteSuccess(c, http.StatusOK, "operation successful", sampleData{Name: "OrderService"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp StandardResponse[sampleData]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "success" || resp.Message != "operation successful" || resp.Data.Name != "OrderService" {
		t.Errorf("unexpected response content: %+v", resp)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteError(c, http.StatusBadRequest, "invalid request")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error != "invalid request" {
		t.Errorf("expected error message 'invalid request', got '%s'", resp.Error)
	}
}

func TestWriteValidationError(t *testing.T) {
	t.Run("generic error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		WriteValidationError(c, errors.New("syntax error"))

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		var resp ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		expected := "invalid request payload: syntax error"
		if resp.Error != expected {
			t.Errorf("expected '%s', got '%s'", expected, resp.Error)
		}
	})

	t.Run("validator.ValidationErrors", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		validate := validator.New()
		err := validate.Struct(dummyPayload{Email: "invalid-email"})

		WriteValidationError(c, err)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		var resp ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Error == "" || resp.Error[:18] != "validation failed:" {
			t.Errorf("expected validation failed message, got '%s'", resp.Error)
		}
	})
}
