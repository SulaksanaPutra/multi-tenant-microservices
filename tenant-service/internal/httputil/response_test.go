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

type sampleTenantData struct {
	TenantName string `json:"tenant_name"`
}

type sampleTenantPayload struct {
	Domain string `validate:"required,fqdn"`
}

func init() {
	gin.SetMode(gin.TestMode)
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteSuccess(c, http.StatusOK, "tenant created", sampleTenantData{TenantName: "Acme Corp"})

	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp StandardResponse[sampleTenantData]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "success" || resp.Message != "tenant created" || resp.Data.TenantName != "Acme Corp" {
		t.Errorf("unexpected response payload: %+v", resp)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteError(c, http.StatusNotFound, "tenant not found")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Error != "tenant not found" {
		t.Errorf("expected error 'tenant not found', got '%s'", resp.Error)
	}
}

func TestWriteValidationError(t *testing.T) {
	t.Run("generic error", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		WriteValidationError(c, errors.New("invalid JSON"))

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		var resp ErrorResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		expected := "invalid request payload: invalid JSON"
		if resp.Error != expected {
			t.Errorf("expected '%s', got '%s'", expected, resp.Error)
		}
	})

	t.Run("validator.ValidationErrors", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		validate := validator.New()
		err := validate.Struct(sampleTenantPayload{Domain: "invalid_domain!"})

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
