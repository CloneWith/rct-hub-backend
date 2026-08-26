package handler

import (
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// teamCreateRequest is the wire format for creating a team. Only the name is
// required; every other field is optional.
type teamCreateRequest struct {
	Name         string  `json:"name" binding:"required"`
	Description  *string `json:"description"`
	Seed         *string `json:"seed"`
	LeaderID     *int64  `json:"leader_id"`
	StrategistID *int64  `json:"strategist_id"`
	Players      []int64 `json:"players"`
}

// TeamHandler exposes team management endpoints (admin CRUD only).
type TeamHandler struct {
	svc *service.TeamService
}

func NewTeamHandler(svc *service.TeamService) *TeamHandler {
	return &TeamHandler{svc: svc}
}

// Create creates a new team (admin only).
func (h *TeamHandler) Create(c *gin.Context) {
	var req teamCreateRequest
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	team := &domain.Team{
		Name:         req.Name,
		Description:  req.Description,
		Seed:         req.Seed,
		LeaderID:     req.LeaderID,
		StrategistID: req.StrategistID,
		Players:      req.Players,
	}
	if err := h.svc.Create(c.Request.Context(), team); err != nil {
		_ = c.Error(err)
		return
	}
	response.Created(c, team)
}

// Patch applies a partial update to an existing team (admin only).
func (h *TeamHandler) Patch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid team id")
		return
	}

	var patch service.TeamPatch
	if err := bindJSON(c, &patch); err != nil {
		_ = c.Error(err)
		return
	}

	team, err := h.svc.Patch(c.Request.Context(), id, &patch)
	if err != nil {
		_ = c.Error(err)
		return
	}
	response.JSON(c, team)
}

// Delete removes a team (admin only). Teams referenced by rooms are rejected.
func (h *TeamHandler) Delete(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid team id")
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		_ = c.Error(err)
		return
	}
	response.NoContent(c)
}
