package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// AnnouncementHandler exposes announcement management endpoints (admin CRUD only).
type AnnouncementHandler struct {
	svc *service.AnnouncementService
}

func NewAnnouncementHandler(svc *service.AnnouncementService) *AnnouncementHandler {
	return &AnnouncementHandler{svc: svc}
}

// Create creates a new announcement (admin only).
func (h *AnnouncementHandler) Create(c *gin.Context) {
	var a domain.Announcement
	if err := bindJSON(c, &a); err != nil {
		_ = c.Error(err)
		return
	}

	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		response.Unauthorized(c, "missing authentication")
		return
	}
	a.AuthorID = claims.OsuID

	if err := h.svc.Create(c.Request.Context(), &a); err != nil {
		_ = c.Error(err)
		return
	}
	response.Created(c, a)
}

// Patch applies a partial update to an existing announcement (admin only).
// The author id is preserved from the existing record.
func (h *AnnouncementHandler) Patch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid announcement id")
		return
	}

	var patch service.AnnouncementPatch
	if err := bindJSON(c, &patch); err != nil {
		_ = c.Error(err)
		return
	}

	a, err := h.svc.Patch(c.Request.Context(), id, &patch)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, a)
}

// Delete removes an announcement (admin only).
func (h *AnnouncementHandler) Delete(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid announcement id")
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		_ = c.Error(err)
		return
	}
	response.NoContent(c)
}

// Publish marks an announcement as visible (admin only).
func (h *AnnouncementHandler) Publish(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid announcement id")
		return
	}

	a, err := h.svc.Publish(c.Request.Context(), id)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, a)
}
