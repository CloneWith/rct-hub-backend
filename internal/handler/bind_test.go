package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"rctHubBackend/internal/middleware"
	"rctHubBackend/pkg/errs"
)

type bindPayload struct {
	Name      string `json:"name" binding:"required"`
	FirstPick string `json:"first_pick" binding:"required,oneof=red blue"`
}

func newBindRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(nil))
	router.POST("/bind", func(c *gin.Context) {
		var req bindPayload
		if err := bindJSON(c, &req); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})
	return router
}

func TestBindJSONReportsMissingRequiredFields(t *testing.T) {
	router := newBindRouter()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"first_pick":"red"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Success bool              `json:"success"`
		Error   string            `json:"error"`
		Details []errs.FieldError `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "invalid input" {
		t.Fatalf("error = %q", body.Error)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "name" || body.Details[0].Rule != "required" {
		t.Fatalf("details = %+v", body.Details)
	}
}

func TestBindJSONReportsOneOfViolation(t *testing.T) {
	router := newBindRouter()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":"r","first_pick":"purple"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	var body struct {
		Details []errs.FieldError `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Details) != 1 {
		t.Fatalf("expected 1 detail, got %+v", body.Details)
	}
	if body.Details[0].Field != "first_pick" || body.Details[0].Rule != "oneof" {
		t.Fatalf("details = %+v", body.Details)
	}
	if !strings.Contains(body.Details[0].Message, "red, blue") {
		t.Fatalf("message should list allowed values: %+v", body.Details[0])
	}
}

func TestBindJSONReportsTypeMistakes(t *testing.T) {
	router := newBindRouter()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":42,"first_pick":"red"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
	var body struct {
		Details []errs.FieldError `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Field != "name" || body.Details[0].Rule != "type" {
		t.Fatalf("details = %+v", body.Details)
	}
}

func TestBindJSONReportsEmptyAndMalformedBody(t *testing.T) {
	router := newBindRouter()

	empty := httptest.NewRecorder()
	router.ServeHTTP(empty, httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader("")))
	var body struct {
		Details []errs.FieldError `json:"details"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode empty-body response: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Rule != "json" || body.Details[0].Message != "request body is empty" {
		t.Fatalf("empty body details = %+v", body.Details)
	}

	malformed := httptest.NewRecorder()
	router.ServeHTTP(malformed, httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":`)))
	if err := json.Unmarshal(malformed.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode malformed response: %v", err)
	}
	if len(body.Details) != 1 || body.Details[0].Rule != "json" || body.Details[0].Message != "request body ended unexpectedly" {
		t.Fatalf("malformed body details = %+v", body.Details)
	}
}

func TestBindJSONAcceptsValidBody(t *testing.T) {
	router := newBindRouter()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(`{"name":"r","first_pick":"red"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
}
