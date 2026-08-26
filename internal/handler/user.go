package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// UserHandler exposes user management endpoints (admin only).
type UserHandler struct {
	svc *service.UserService
	log *zap.Logger
}

func NewUserHandler(svc *service.UserService, log *zap.Logger) *UserHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &UserHandler{svc: svc, log: log}
}

// Patch applies a partial update to a user (admin only).
func (h *UserHandler) Patch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}

	var patch service.UserPatch
	if err := bindJSON(c, &patch); err != nil {
		h.log.Debug("invalid request body", zap.String("path", c.Request.URL.Path), zap.Error(err))
		_ = c.Error(err)
		return
	}

	user, err := h.svc.Patch(c.Request.Context(), id, &patch)
	if err != nil {
		h.log.Warn("admin operation failed", zap.String("op", "patch_user"), zap.String("target_user_id", c.Param("id")), zap.Error(err))
		_ = c.Error(err)
		return
	}
	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: user patched", zap.Int64("caller_osu_id", claims.OsuID), zap.String("target_user_id", c.Param("id")))
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
	if err := bindJSON(c, &req); err != nil {
		h.log.Debug("invalid request body", zap.String("path", c.Request.URL.Path), zap.Error(err))
		_ = c.Error(err)
		return
	}

	user, err := h.svc.UpdateRoles(c.Request.Context(), id, req.Roles)
	if err != nil {
		h.log.Warn("admin operation failed", zap.String("op", "update_roles"), zap.String("target_user_id", c.Param("id")), zap.Error(err))
		_ = c.Error(err)
		return
	}
	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: user roles updated", zap.Int64("caller_osu_id", claims.OsuID), zap.String("target_user_id", c.Param("id")), zap.Any("roles", req.Roles))
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
	if err := bindJSON(c, &req); err != nil {
		h.log.Debug("invalid request body", zap.String("path", c.Request.URL.Path), zap.Error(err))
		_ = c.Error(err)
		return
	}

	user, err := h.svc.SetBanned(c.Request.Context(), id, *req.Banned)
	if err != nil {
		h.log.Warn("admin operation failed", zap.String("op", "set_banned"), zap.String("target_user_id", c.Param("id")), zap.Error(err))
		_ = c.Error(err)
		return
	}
	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: user ban status changed", zap.Int64("caller_osu_id", claims.OsuID), zap.String("target_user_id", c.Param("id")), zap.Bool("banned", *req.Banned))
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
	if err := bindJSON(c, &req); err != nil {
		h.log.Debug("invalid request body", zap.String("path", c.Request.URL.Path), zap.Error(err))
		_ = c.Error(err)
		return
	}

	user, err := h.svc.SetVerifyStatus(c.Request.Context(), id, req.Status)
	if err != nil {
		h.log.Warn("admin operation failed", zap.String("op", "set_verify_status"), zap.String("target_user_id", c.Param("id")), zap.Error(err))
		_ = c.Error(err)
		return
	}
	claims, _ := middleware.ClaimsFromContext(c)
	h.log.Info("audit: user verify status changed", zap.Int64("caller_osu_id", claims.OsuID), zap.String("target_user_id", c.Param("id")), zap.String("status", string(req.Status)))
	response.JSON(c, user)
}
