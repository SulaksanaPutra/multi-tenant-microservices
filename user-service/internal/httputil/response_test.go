package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestWriteSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	type testData struct {
		Name string `json:"name"`
	}

	WriteSuccess(c, http.StatusOK, "user retrieved", testData{Name: "John Doe"})

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	var resp StandardResponse[testData]
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Status != "success" || resp.Message != "user retrieved" || resp.Data.Name != "John Doe" {
		t.Errorf("unexpected response body content: %+v", resp)
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WriteError(c, http.StatusBadRequest, "invalid payload")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal error response: %v", err)
	}

	if resp.Error != "invalid payload" {
		t.Errorf("expected error 'invalid payload', got '%s'", resp.Error)
	}
}

func TestWriteValidationError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	err := errors.New("raw payload error")
	WriteValidationError(c, err)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	expectedMsg := "invalid request payload: raw payload error"
	if resp.Error != expectedMsg {
		t.Errorf("expected error '%s', got '%s'", expectedMsg, resp.Error)
	}
}
