package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"rctHubBackend/pkg/errs"
)

func TestErrorHandlerMapsCacheSyncFailureToServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(nil))
	router.GET("/", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("%w: redis unavailable", errs.ErrCacheSync))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["error"] != errs.ErrCacheSync.Error() {
		t.Fatalf("expected stable public error, got %v", body["error"])
	}
}
