package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/repository"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

// RoomService handles room lifecycle and configuration.
type RoomService struct {
	rooms         repository.RoomRepository
	matches       repository.MatchRepository
	users         repository.UserRepository
	formalMatches FormalMatchBootstrap
	now           func() time.Time
}

type FormalMatchBootstrap interface {
	Create(context.Context, bson.ObjectID, domain.Match, matchengine.State, time.Time) error
}

// NewRoomService creates a new RoomService.
func NewRoomService(rooms repository.RoomRepository, matches repository.MatchRepository, users repository.UserRepository, formalMatches FormalMatchBootstrap) *RoomService {
	return &RoomService{rooms: rooms, matches: matches, users: users, formalMatches: formalMatches, now: func() time.Time { return time.Now().UTC() }}
}

// CreateRoom creates a new room for the given owner.
func (s *RoomService) CreateRoom(ctx context.Context, ownerID int64, roomType domain.RoomType, name string) (*domain.Room, error) {
	if roomType != domain.RoomTypePrivate && roomType != domain.RoomTypeCasual && roomType != domain.RoomTypeMatch {
		return nil, errs.ErrInvalidInput
	}
	user, err := s.currentEligibleUser(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	if roomType == domain.RoomTypeMatch && !user.HasRole(domain.RoleAdmin) && !user.HasRole(domain.RoleReferee) {
		return nil, errs.ErrForbidden
	}
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
func (s *RoomService) SetStrategists(ctx context.Context, callerID int64, roomID bson.ObjectID, redUID, blueUID *int64) (*domain.Room, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{
		"settings.red_strategist_user_id":  redUID,
		"settings.blue_strategist_user_id": blueUID,
	}, true)
}

// SetStreamer assigns the streamer user id for a room.
func (s *RoomService) SetStreamer(ctx context.Context, callerID int64, roomID bson.ObjectID, uid *int64) (*domain.Room, error) {
	_, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.streamer_user_id": uid}, false)
}

// SetMappool replaces the room mappool.
func (s *RoomService) SetMappool(ctx context.Context, callerID int64, roomID bson.ObjectID, pool domain.Mappool) (*domain.Room, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.mappool": pool}, true)
}

// SetBPOrder sets the pick/ban order for a room.
func (s *RoomService) SetBPOrder(ctx context.Context, callerID int64, roomID bson.ObjectID, order domain.BPOrder) (*domain.Room, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{
		"settings.first_pick": &order.FirstPick,
		"settings.first_ban":  &order.FirstBan,
	}, true)
}

// SetPlayers sets the team rosters for a room.
func (s *RoomService) SetPlayers(ctx context.Context, callerID int64, roomID bson.ObjectID, redLeader, blueLeader *int64, redPlayers, bluePlayers []int64) (*domain.Room, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{
		"settings.red_leader":   redLeader,
		"settings.blue_leader":  blueLeader,
		"settings.red_players":  append([]int64(nil), redPlayers...),
		"settings.blue_players": append([]int64(nil), bluePlayers...),
	}, true)
}

// SetMPLink sets the multiplayer link for a room.
func (s *RoomService) SetMPLink(ctx context.Context, callerID int64, roomID bson.ObjectID, link string) (*domain.Room, error) {
	_, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.mp_link": &link}, false)
}

// SetStreamLink sets the stream link for a room.
func (s *RoomService) SetStreamLink(ctx context.Context, callerID int64, roomID bson.ObjectID, link string) (*domain.Room, error) {
	_, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.stream_link": &link}, false)
}

// StartMatch creates a match from the room settings and transitions the room.
func (s *RoomService) StartMatch(ctx context.Context, callerID int64, roomID bson.ObjectID) (*domain.Match, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if room.MatchID != nil {
		if room.Type == domain.RoomTypeMatch {
			return s.existingFormalMatch(ctx, room)
		}
		return nil, errs.ErrAlreadyExists
	}
	if room.Type == domain.RoomTypeMatch {
		if s.formalMatches == nil {
			return nil, fmt.Errorf("formal match bootstrap is not configured")
		}
		now := s.now().UTC()
		seed, seedErr := BuildFormalMatchSeed(*room, now)
		if seedErr != nil {
			return nil, seedErr
		}
		if createErr := s.formalMatches.Create(ctx, room.ID, seed.LegacyMatch, seed.State, now); createErr != nil {
			if !errors.Is(createErr, errs.ErrFormalMatchAlreadyStarted) {
				return nil, createErr
			}
			room, reloadErr := s.rooms.ByID(ctx, room.ID)
			if reloadErr != nil {
				return nil, reloadErr
			}
			return s.existingFormalMatch(ctx, room)
		}
		return &seed.LegacyMatch, nil
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

func (s *RoomService) existingFormalMatch(ctx context.Context, room *domain.Room) (*domain.Match, error) {
	if room == nil || room.Type != domain.RoomTypeMatch || room.MatchID == nil {
		return nil, errs.ErrConflict
	}
	match, err := s.matches.ByID(ctx, *room.MatchID)
	if err != nil {
		return nil, err
	}
	if match == nil || match.RoomID != room.ID || match.RoomType != domain.RoomTypeMatch {
		return nil, fmt.Errorf("%w: formal room points to a different match", errs.ErrConflict)
	}
	return match, nil
}

func (s *RoomService) currentEligibleUser(ctx context.Context, osuID int64) (*domain.User, error) {
	if s == nil || s.users == nil {
		return nil, fmt.Errorf("room authorization is not configured")
	}
	user, err := s.users.ByOsuID(ctx, osuID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return nil, errs.ErrUnauthorized
		}
		return nil, err
	}
	if user.IsBanned || user.VerifyStatus != domain.Verified {
		return nil, errs.ErrForbidden
	}
	return user, nil
}

func (s *RoomService) updateRoomFields(ctx context.Context, roomID bson.ObjectID, fields bson.M, requireSetupOpen bool) (*domain.Room, error) {
	if err := s.rooms.UpdateFields(ctx, roomID, fields, requireSetupOpen); err != nil {
		return nil, err
	}
	return s.rooms.ByID(ctx, roomID)
}

func (s *RoomService) authorizedRoom(ctx context.Context, callerID int64, roomID bson.ObjectID) (*domain.Room, error) {
	user, err := s.currentEligibleUser(ctx, callerID)
	if err != nil {
		return nil, err
	}
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if user.HasRole(domain.RoleAdmin) {
		return room, nil
	}
	if room.OwnerID != user.OnlineID {
		return nil, errs.ErrForbidden
	}
	if room.Type == domain.RoomTypeMatch && !user.HasRole(domain.RoleReferee) {
		return nil, errs.ErrForbidden
	}
	return room, nil
}

func requireRoomSetupOpen(room *domain.Room) error {
	if room != nil && room.MatchID != nil {
		return fmt.Errorf("%w: match setup is locked after bootstrap", errs.ErrConflict)
	}
	return nil
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
