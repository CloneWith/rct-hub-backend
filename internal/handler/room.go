package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/middleware"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/response"
)

// RoomHandler exposes room configuration endpoints (pre-game setup).
type RoomHandler struct {
	svc *service.RoomService
	log *zap.Logger
}

// UpdateMetadata updates administrator-managed room metadata before match setup locks.
func (h *RoomHandler) UpdateMetadata(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}
	var req struct {
		Name           string       `json:"name" binding:"required"`
		Round          domain.Round `json:"round"`
		ScheduledAt    *time.Time   `json:"scheduled_at"`
		RefereeUserID  *int64       `json:"referee_user_id"`
		StreamerUserID *int64       `json:"streamer_user_id"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.UpdateRoomMetadata(c.Request.Context(), callerID, id, service.RoomMetadataUpdate{
		Name: req.Name, Round: req.Round, ScheduledAt: req.ScheduledAt, RefereeUserID: req.RefereeUserID,
		StreamerUserID: req.StreamerUserID,
	})
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.JSON(c, room)
}

// UpdateMetadataPartial applies an incremental room metadata update: only the
// fields present in the request body are changed, absent fields keep their
// stored values. Clearing optional fields is not supported on this endpoint.
func (h *RoomHandler) UpdateMetadataPartial(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}
	var req struct {
		Name           *string       `json:"name"`
		Round          *domain.Round `json:"round"`
		ScheduledAt    *time.Time    `json:"scheduled_at"`
		RefereeUserID  *int64        `json:"referee_user_id"`
		StreamerUserID *int64        `json:"streamer_user_id"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.UpdateRoomMetadataPartial(c.Request.Context(), callerID, id, service.RoomMetadataPatch{
		Name: req.Name, Round: req.Round, ScheduledAt: req.ScheduledAt, RefereeUserID: req.RefereeUserID,
		StreamerUserID: req.StreamerUserID,
	})
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.JSON(c, room)
}

func NewRoomHandler(svc *service.RoomService, log *zap.Logger) *RoomHandler {
	if log == nil {
		log = zap.NewNop()
	}
	return &RoomHandler{svc: svc, log: log}
}

// Create creates a new room.
func (h *RoomHandler) Create(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type" binding:"required"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	claims, ok := middleware.ClaimsFromContext(c)
	if !ok {
		response.Unauthorized(c, "missing authentication")
		return
	}

	room, err := h.svc.CreateRoom(c.Request.Context(), claims.OsuID, domain.RoomType(req.Type), req.Name)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.Created(c, room)
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
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetStreamer(c.Request.Context(), callerID, id, req.UID)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.JSON(c, room)
}

// SetReferee assigns the referee for a formal room. This is an administrator-only operation.
func (h *RoomHandler) SetReferee(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}
	var req struct {
		RefereeUserID *int64 `json:"referee_user_id"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetReferee(c.Request.Context(), callerID, id, req.RefereeUserID)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.JSON(c, room)
}

// SetMappool links the room to a mappool entity (nil clears the reference).
func (h *RoomHandler) SetMappool(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}
	var req struct {
		MappoolID *string `json:"mappool_id"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	mappoolID, err := roomObjectIDPtr(req.MappoolID, "mappool_id")
	if err != nil {
		writeRoomError(c, err)
		return
	}
	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetMappool(c.Request.Context(), callerID, id, mappoolID)
	if err != nil {
		writeRoomError(c, err)
		return
	}
	response.JSON(c, room)
}

// SetTeams links the red and blue team entities (nil clears a side). The
// request carries the full target state of both sides.
func (h *RoomHandler) SetTeams(c *gin.Context) {
	id, err := bson.ObjectIDFromHex(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "invalid room id")
		return
	}
	var req struct {
		RedTeamID  *string `json:"red_team_id"`
		BlueTeamID *string `json:"blue_team_id"`
	}
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}
	redTeamID, err := roomObjectIDPtr(req.RedTeamID, "red_team_id")
	if err != nil {
		writeRoomError(c, err)
		return
	}
	blueTeamID, err := roomObjectIDPtr(req.BlueTeamID, "blue_team_id")
	if err != nil {
		writeRoomError(c, err)
		return
	}
	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetTeams(c.Request.Context(), callerID, id, redTeamID, blueTeamID)
	if err != nil {
		writeRoomError(c, err)
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
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetBPOrder(c.Request.Context(), callerID, id, domain.BPOrder{
		FirstPick: req.FirstPick,
		FirstBan:  req.FirstBan,
	})
	if err != nil {
		writeRoomError(c, err)
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
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetMPLink(c.Request.Context(), callerID, id, req.Link)
	if err != nil {
		writeRoomError(c, err)
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
	if err := bindJSON(c, &req); err != nil {
		_ = c.Error(err)
		return
	}

	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	room, err := h.svc.SetStreamLink(c.Request.Context(), callerID, id, req.Link)
	if err != nil {
		writeRoomError(c, err)
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

	callerID, ok := roomCallerID(c)
	if !ok {
		return
	}
	match, err := h.svc.StartMatch(c.Request.Context(), callerID, id)
	if err != nil {
		h.log.Warn("room start match failed",
			zap.Int64("caller_osu_id", callerID),
			zap.String("room_id", c.Param("id")),
			zap.Error(err),
		)
		writeRoomError(c, err)
		return
	}
	h.log.Info("audit: match started",
		zap.Int64("caller_osu_id", callerID),
		zap.String("room_id", c.Param("id")),
		zap.String("match_id", match.ID.Hex()),
	)
	response.Created(c, match)
}

func roomCallerID(c *gin.Context) (int64, bool) {
	claims, ok := middleware.ClaimsFromContext(c)
	if !ok || claims == nil {
		response.Unauthorized(c, "missing authentication")
		return 0, false
	}
	return claims.OsuID, true
}

// roomObjectIDPtr parses an optional ObjectID hex string. nil or an empty
// string yield nil (cleared reference); an invalid hex is a field error.
func roomObjectIDPtr(raw *string, field string) (*bson.ObjectID, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	id, err := bson.ObjectIDFromHex(*raw)
	if err != nil {
		return nil, errs.NewValidationError(
			errs.FieldError{Field: field, Rule: "objectid", Message: "invalid " + field},
		)
	}
	return &id, nil
}

func roomErrorStatus(err error) int {
	switch {
	case errors.Is(err, errs.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, errs.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, errs.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, errs.ErrAlreadyExists), errors.Is(err, errs.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, errs.ErrInvalidInput):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeRoomError(c *gin.Context, err error) {
	if valErr, ok := errs.AsValidationError(err); ok {
		response.Error(c, http.StatusBadRequest, errs.ErrInvalidInput.Error(), valErr.Fields)
		return
	}
	status := roomErrorStatus(err)
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "internal server error"
	}
	response.Error(c, status, message)
}
