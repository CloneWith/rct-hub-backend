package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
	"rctHubBackend/pkg/response"
)

// MatchHandler exposes match viewing and control endpoints.
type MatchHandler struct {
	svc *service.MatchService
}

func NewMatchHandler(svc *service.MatchService) *MatchHandler {
	return &MatchHandler{svc: svc}
}

// List returns a paginated list of matches.
func (h *MatchHandler) List(c *gin.Context) {
	var params paginate.Params
	if err := c.ShouldBindQuery(&params); err != nil {
		response.BadRequest(c, "invalid pagination params")
		return
	}

	var status *domain.MatchStatus
	if s := c.Query("status"); s != "" {
		parsed := domain.MatchStatus(s)
		status = &parsed
	}

	matches, err := h.svc.List(c.Request.Context(), params, status)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, matches)
}

// Get returns a single match by id.
func (h *MatchHandler) Get(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	match, err := h.svc.GetMatch(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, match)
}

// GetByCode returns a match by code.
func (h *MatchHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	match, err := h.svc.GetMatchByCode(c.Request.Context(), code)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, match)
}

// ListMoves returns paginated moves for a match.
func (h *MatchHandler) ListMoves(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	var params paginate.Params
	if err := c.ShouldBindQuery(&params); err != nil {
		response.BadRequest(c, "invalid pagination params")
		return
	}

	moves, err := h.svc.ListByMatch(c.Request.Context(), id, params)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, moves)
}

// LatestMoves returns the most recent moves for a match.
func (h *MatchHandler) LatestMoves(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	limit, _ := strconv.ParseInt(c.DefaultQuery("limit", "50"), 10, 64)
	moves, err := h.svc.LatestByMatch(c.Request.Context(), id, limit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, moves)
}

// EndMatch ends a match.
func (h *MatchHandler) EndMatch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	var req struct {
		Reason string           `json:"reason" binding:"required"`
		Winner *domain.TeamSide `json:"winner,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	if err := h.svc.EndMatch(c.Request.Context(), id, domain.WinReason(req.Reason), req.Winner); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.NoContent(c)
}

// AdvanceTurn advances the match turn.
func (h *MatchHandler) AdvanceTurn(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	if err := h.svc.AdvanceTurn(c.Request.Context(), id); err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.NoContent(c)
}

// CheckWinCondition checks for a winning team.
func (h *MatchHandler) CheckWinCondition(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid match id")
		return
	}

	winner, err := h.svc.CheckWinCondition(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, gin.H{"winner": winner})
}
