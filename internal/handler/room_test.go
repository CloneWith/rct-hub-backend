package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUpdateMetadataRejectsUnauthenticatedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRoomHandler(nil, nil)
	r := httptest.NewRequest(http.MethodPatch, "/rooms/507f1f77bcf86cd799439011/metadata", strings.NewReader(`{"name":"updated"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	c.Params = gin.Params{{Key: "id", Value: "507f1f77bcf86cd799439011"}}

	h.UpdateMetadata(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestUpdateMetadataRejectsInvalidRoomIDBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRoomHandler(nil, nil)
	r := httptest.NewRequest(http.MethodPatch, "/rooms/not-an-id/metadata", strings.NewReader(`{"name":"updated"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	c.Params = gin.Params{{Key: "id", Value: "not-an-id"}}

	h.UpdateMetadata(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateMetadataPartialRejectsUnauthenticatedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRoomHandler(nil, nil)
	r := httptest.NewRequest(http.MethodPut, "/rooms/507f1f77bcf86cd799439011/metadata", strings.NewReader(`{"name":"updated"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	c.Params = gin.Params{{Key: "id", Value: "507f1f77bcf86cd799439011"}}

	h.UpdateMetadataPartial(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestUpdateMetadataPartialRejectsInvalidRoomIDBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRoomHandler(nil, nil)
	r := httptest.NewRequest(http.MethodPut, "/rooms/not-an-id/metadata", strings.NewReader(`{"name":"updated"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = r
	c.Params = gin.Params{{Key: "id", Value: "not-an-id"}}

	h.UpdateMetadataPartial(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
