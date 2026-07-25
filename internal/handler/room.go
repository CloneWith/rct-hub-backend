package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/response"
)

// RoomHandler exposes room configuration endpoints (pre-game setup).
type RoomHandler struct {
	svc *service.RoomService
}

func NewRoomHandler(svc *service.RoomService) *RoomHandler {
	return &RoomHandler{svc: svc}
}

// Create creates a new room.
func (h *RoomHandler) Create(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		response.Unauthorized(c, "missing authentication")
		return
	}

	room, err := h.svc.CreateRoom(c.Request.Context(), claims.OsuID, domain.RoomType(req.Type), req.Name)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Created(c, room)
}

// SetStrategists assigns red and blue strategists.
func (h *RoomHandler) SetStrategists(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		RedUID  *int64 `json:"red_strategist_user_id"`
		BlueUID *int64 `json:"blue_strategist_user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetStrategists(c.Request.Context(), id, req.RedUID, req.BlueUID)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// SetStreamer assigns the streamer.
func (h *RoomHandler) SetStreamer(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		UID *int64 `json:"streamer_user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetStreamer(c.Request.Context(), id, req.UID)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// SetBPOrder sets pick/ban order.
func (h *RoomHandler) SetBPOrder(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		FirstPick domain.TeamSide `json:"first_pick" binding:"required"`
		FirstBan  domain.TeamSide `json:"first_ban" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetBPOrder(c.Request.Context(), id, domain.BPOrder{
		FirstPick: req.FirstPick,
		FirstBan:  req.FirstBan,
	})
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// SetPlayers sets team rosters.
func (h *RoomHandler) SetPlayers(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		RedLeader   *int64  `json:"red_leader"`
		BlueLeader  *int64  `json:"blue_leader"`
		RedPlayers  []int64 `json:"red_players"`
		BluePlayers []int64 `json:"blue_players"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetPlayers(c.Request.Context(), id, req.RedLeader, req.BlueLeader, req.RedPlayers, req.BluePlayers)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// SetMPLink sets the multiplayer link.
func (h *RoomHandler) SetMPLink(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		Link string `json:"mp_link" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetMPLink(c.Request.Context(), id, req.Link)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// SetStreamLink sets the stream link.
func (h *RoomHandler) SetStreamLink(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	var req struct {
		Link string `json:"stream_link" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid request body")
		return
	}

	room, err := h.svc.SetStreamLink(c.Request.Context(), id, req.Link)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// StartMatch starts a match from the room.
func (h *RoomHandler) StartMatch(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	match, err := h.svc.StartMatch(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	response.Created(c, match)
}
