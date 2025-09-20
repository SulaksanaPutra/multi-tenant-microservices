package main

import (
	"net/http"

	"user-service/internal/utils"

	"github.com/gin-gonic/gin"
)

// newRouter initializes all HTTP routes, middleware, and handlers for User Service.
func newRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	r.GET("/health", func(c *gin.Context) {
		utils.WriteSuccess[any](c, http.StatusOK, "OK", nil)
	})

	return r
}
