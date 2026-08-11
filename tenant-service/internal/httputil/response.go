package httputil

import (
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/gin-gonic/gin"
)

type StandardResponse[T any] = httputil.StandardResponse[T]
type ErrorResponse = httputil.ErrorResponse

func WriteSuccess[T any](c *gin.Context, statusCode int, message string, data T) {
	httputil.WriteSuccess(c, statusCode, message, data)
}

func WriteError(c *gin.Context, statusCode int, message string) {
	httputil.WriteError(c, statusCode, message)
}

func WriteValidationError(c *gin.Context, err error) {
	httputil.WriteValidationError(c, err)
}
