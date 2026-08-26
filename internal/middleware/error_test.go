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

func TestErrorHandlerExposesValidationErrorDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(nil))
	router.POST("/", func(c *gin.Context) {
		_ = c.Error(errs.NewValidationError(
			errs.FieldError{Field: "name", Rule: "required", Message: "name is required"},
			errs.FieldError{Field: "first_pick", Rule: "oneof", Message: "first_pick must be one of: red, blue"},
		))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
	var body struct {
		Success bool              `json:"success"`
		Error   string            `json:"error"`
		Details []errs.FieldError `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Success {
		t.Fatal("expected success=false")
	}
	if body.Error != errs.ErrInvalidInput.Error() {
		t.Fatalf("expected error %q, got %q", errs.ErrInvalidInput.Error(), body.Error)
	}
	if len(body.Details) != 2 {
		t.Fatalf("expected 2 field errors, got %d", len(body.Details))
	}
	if body.Details[0].Field != "name" || body.Details[0].Rule != "required" || body.Details[0].Message != "name is required" {
		t.Fatalf("unexpected first detail: %+v", body.Details[0])
	}
	if body.Details[1].Field != "first_pick" {
		t.Fatalf("unexpected second detail: %+v", body.Details[1])
	}
}

func TestErrorHandlerKeepsSentinelStatusForValidationError(t *testing.T) {
	// ValidationError must keep mapping to 400 through the sentinel branch
	// ordering when not caught by the specific branch (defense in depth).
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(nil))
	router.GET("/", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("wrapped: %w", errs.NewValidationError(
			errs.FieldError{Field: "body", Rule: "json", Message: "malformed JSON body"},
		)))
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}
