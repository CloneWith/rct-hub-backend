package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
	"rctHubBackend/pkg/response"
)

// UserHandler exposes user management endpoints.
type UserHandler struct {
	svc *service.UserService
}

func NewUserHandler(svc *service.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// List returns a paginated list of users.
func (h *UserHandler) List(c *gin.Context) {
	var params paginate.Params
	if err := c.ShouldBindQuery(&params); err != nil {
		response.BadRequest(c, "invalid pagination params")
		return
	}

	result, err := h.svc.List(c.Request.Context(), params)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, result)
}

// Get returns a single user by id.
func (h *UserHandler) Get(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}

	user, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, user)
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
		response.Error(c, http.StatusNotFound, err.Error())
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

	var req struct {
		Banned bool `json:"banned" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	user, err := h.svc.SetBanned(c.Request.Context(), id, req.Banned)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
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
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, user)
}

// Me returns the currently authenticated user.
func (h *UserHandler) Me(c *gin.Context) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		response.Unauthorized(c, "missing authentication")
		return
	}

	id, err := bson.ObjectIDFromHex(claims.UserID)
	if err != nil {
		response.BadRequest(c, "invalid user id in token")
		return
	}

	user, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, user)
}
