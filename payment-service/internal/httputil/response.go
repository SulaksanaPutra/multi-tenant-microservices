package httputil

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type StandardResponse[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Data    T      `json:"data,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func WriteSuccess[T any](c *gin.Context, statusCode int, message string, data T) {
	c.JSON(statusCode, StandardResponse[T]{
		Status:  "success",
		Message: message,
		Data:    data,
	})
}

func WriteError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, ErrorResponse{Error: message})
}

func WriteValidationError(c *gin.Context, err error) {
	var ve validator.ValidationErrors
	if errors.As(err, &ve) {
		out := make([]string, len(ve))
		for i, fe := range ve {
			out[i] = fmt.Sprintf("field '%s' failed on '%s'", fe.Field(), fe.Tag())
		}
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("validation failed: %v", out),
		})
		return
	}
	c.JSON(http.StatusBadRequest, ErrorResponse{Error: "invalid request payload: " + err.Error()})
}
