package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
	"rctHubBackend/pkg/response"
)

// RoomHandler exposes room management endpoints.
type RoomHandler struct {
	svc *service.RoomService
}

func NewRoomHandler(svc *service.RoomService) *RoomHandler {
	return &RoomHandler{svc: svc}
}

// List returns a paginated list of rooms.
func (h *RoomHandler) List(c *gin.Context) {
	var params paginate.Params
	if err := c.ShouldBindQuery(&params); err != nil {
		response.BadRequest(c, "invalid pagination params")
		return
	}

	var roomType *domain.RoomType
	if t := c.Query("type"); t != "" {
		parsed := domain.RoomType(t)
		roomType = &parsed
	}

	rooms, err := h.svc.GetRooms(c.Request.Context(), params, roomType)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.JSON(c, rooms)
}

// Get returns a single room by id.
func (h *RoomHandler) Get(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}

	room, err := h.svc.GetRoom(c.Request.Context(), id)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
}

// GetByCode returns a room by its invite code.
func (h *RoomHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	room, err := h.svc.GetRoomByCode(c.Request.Context(), code)
	if err != nil {
		response.Error(c, http.StatusNotFound, err.Error())
		return
	}
	response.JSON(c, room)
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

// GetRooms is a helper to satisfy the handler interface by calling List.
func (h *RoomHandler) GetRooms(c *gin.Context) {
	h.List(c)
}

func parseInt64Ptr(s string) *int64 {
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
