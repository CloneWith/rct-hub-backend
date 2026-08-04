package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// RoomService handles room lifecycle and configuration.
type RoomService struct {
	rooms   repository.RoomRepository
	matches repository.MatchRepository
}

// NewRoomService creates a new RoomService.
func NewRoomService(rooms repository.RoomRepository, matches repository.MatchRepository) *RoomService {
	return &RoomService{rooms: rooms, matches: matches}
}

// CreateRoom creates a new room for the given owner.
func (s *RoomService) CreateRoom(ctx context.Context, ownerID int64, roomType domain.RoomType, name string) (*domain.Room, error) {
	if name == "" {
		name = "RCT Room"
	}
	code, err := generateRoomCode()
	if err != nil {
		return nil, fmt.Errorf("generate room code: %w", err)
	}
	room := &domain.Room{
		Code:     code,
		Name:     name,
		Type:     roomType,
		OwnerID:  ownerID,
		Settings: domain.RoomSettings{Mappool: domain.NewMappool()},
	}
	if err := s.rooms.Create(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// GetRooms returns a paginated list of rooms filtered by optional type.
func (s *RoomService) GetRooms(ctx context.Context, params paginate.Params, roomType *domain.RoomType) (paginate.Result[domain.Room], error) {
	return s.rooms.List(ctx, params, roomType)
}

// GetRoom fetches a room by id.
func (s *RoomService) GetRoom(ctx context.Context, id bson.ObjectID) (*domain.Room, error) {
	return s.rooms.ByID(ctx, id)
}

// GetRoomByCode fetches a room by invite code.
func (s *RoomService) GetRoomByCode(ctx context.Context, code string) (*domain.Room, error) {
	return s.rooms.ByCode(ctx, code)
}

// SetStrategists assigns the red and blue strategists for a room.
func (s *RoomService) SetStrategists(ctx context.Context, roomID bson.ObjectID, redUID, blueUID *int64) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.RedStrategistUserID = redUID
	room.Settings.BlueStrategistUserID = blueUID
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetStreamer assigns the streamer user id for a room.
func (s *RoomService) SetStreamer(ctx context.Context, roomID bson.ObjectID, uid *int64) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.StreamerUserID = uid
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetMappool replaces the room mappool.
func (s *RoomService) SetMappool(ctx context.Context, roomID bson.ObjectID, pool domain.Mappool) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.Mappool = pool
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetBPOrder sets the pick/ban order for a room.
func (s *RoomService) SetBPOrder(ctx context.Context, roomID bson.ObjectID, order domain.BPOrder) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.FirstPick = &order.FirstPick
	room.Settings.FirstBan = &order.FirstBan
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetPlayers sets the team rosters for a room.
func (s *RoomService) SetPlayers(ctx context.Context, roomID bson.ObjectID, redLeader, blueLeader *int64, redPlayers, bluePlayers []int64) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.RedLeader = redLeader
	room.Settings.BlueLeader = blueLeader
	room.Settings.RedPlayers = redPlayers
	room.Settings.BluePlayers = bluePlayers
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetMPLink sets the multiplayer link for a room.
func (s *RoomService) SetMPLink(ctx context.Context, roomID bson.ObjectID, link string) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.MPLink = &link
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// SetStreamLink sets the stream link for a room.
func (s *RoomService) SetStreamLink(ctx context.Context, roomID bson.ObjectID, link string) (*domain.Room, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	room.Settings.StreamLink = &link
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return room, nil
}

// StartMatch creates a match from the room settings and transitions the room.
func (s *RoomService) StartMatch(ctx context.Context, roomID bson.ObjectID) (*domain.Match, error) {
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if room.MatchID != nil {
		return nil, errs.ErrAlreadyExists
	}
	if room.Type == domain.RoomTypeMatch {
		return nil, formalLegacyWriteError()
	}
	if !room.Settings.CanStart(room.Type) {
		return nil, fmt.Errorf("%w: room settings do not satisfy start requirements", errs.ErrInvalidInput)
	}

	redTeam := domain.Team{
		Side:         domain.TeamSideRed,
		Name:         "Red",
		Color:        "#ef4444",
		LeaderID:     derefInt64(room.Settings.RedLeader),
		StrategistID: derefInt64(room.Settings.RedStrategistUserID),
		Players:      room.Settings.RedPlayers,
	}
	blueTeam := domain.Team{
		Side:         domain.TeamSideBlue,
		Name:         "Blue",
		Color:        "#3b82f6",
		LeaderID:     derefInt64(room.Settings.BlueLeader),
		StrategistID: derefInt64(room.Settings.BlueStrategistUserID),
		Players:      room.Settings.BluePlayers,
	}

	match := domain.NewMatch(*room, redTeam, blueTeam)
	match.BPOrder = domain.BPOrder{
		FirstPick: *room.Settings.FirstPick,
		FirstBan:  *room.Settings.FirstBan,
	}
	match.Status = domain.MatchStatusActive
	now := time.Now().UTC()
	match.StartedAt = &now
	match.TurnState.StartBan(match.BPOrder)
	match.Timer = domain.NewTimerState(domain.BanTimeLimit, domain.BanBonusTime)

	if err := s.matches.Create(ctx, &match); err != nil {
		return nil, err
	}
	room.MatchID = &match.ID
	if err := s.rooms.Update(ctx, room); err != nil {
		return nil, err
	}
	return &match, nil
}

func generateRoomCode() (string, error) {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b), nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
