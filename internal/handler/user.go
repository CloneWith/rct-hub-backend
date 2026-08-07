package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// UserHandler exposes user management endpoints (admin only).
type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// UpdateRoles updates a user's roles (admin only).
func (h *UserHandler) UpdateRoles(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}

	var req struct {
		Roles []domain.UserRole `json:"roles" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, err := h.svc.UpdateRoles(c.Request.Context(), id, req.Roles)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, user)
}

// SetBanned bans or unbans a user (admin only).
func (h *UserHandler) SetBanned(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}

	// false won't pass on ShouldBindJSON(), so using a pointer here
	var req struct {
		Banned *bool `json:"banned" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, err := h.svc.SetBanned(c.Request.Context(), id, *req.Banned)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, user)
}

// SetVerifyStatus updates a user's verification status (admin only).
func (h *UserHandler) SetVerifyStatus(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}

	var req struct {
		Status domain.VerifyStatus `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, err := h.svc.SetVerifyStatus(c.Request.Context(), id, req.Status)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, user)
}
