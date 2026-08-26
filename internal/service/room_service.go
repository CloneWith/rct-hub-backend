package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/irc"
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
	teams         repository.TeamRepository
	mappools      repository.MappoolRepository
	formalMatches FormalMatchBootstrap
	now           func() time.Time
	log           *zap.Logger
}

type FormalMatchBootstrap interface {
	Create(context.Context, bson.ObjectID, domain.Match, matchengine.State, time.Time) error
}

// RoomMetadataUpdate is the administrator-owned pre-game room configuration.
// Nil optional values clear the corresponding relationship or schedule. Team
// rosters and the pool are not part of metadata: they are managed through the
// dedicated team / mappool reference endpoints.
type RoomMetadataUpdate struct {
	Name           string
	Round          string
	ScheduledAt    *time.Time
	RefereeUserID  *int64
	StreamerUserID *int64
}

// RoomMetadataPatch is an incremental room metadata update. Nil pointer fields
// are left unchanged; only the fields present in the request are written.
// Clearing an optional field is not supported here; use the full metadata
// replace or the dedicated per-field endpoints instead.
type RoomMetadataPatch struct {
	Name           *string
	Round          *string
	ScheduledAt    *time.Time
	RefereeUserID  *int64
	StreamerUserID *int64
}

// NewRoomService creates a new RoomService.
func NewRoomService(rooms repository.RoomRepository, matches repository.MatchRepository, users repository.UserRepository, teams repository.TeamRepository, mappools repository.MappoolRepository, formalMatches FormalMatchBootstrap, log *zap.Logger) *RoomService {
	if log == nil {
		log = zap.NewNop()
	}
	return &RoomService{rooms: rooms, matches: matches, users: users, teams: teams, mappools: mappools, formalMatches: formalMatches, now: func() time.Time { return time.Now().UTC() }, log: log}
}

// CreateRoom creates a new room for the given owner.
func (s *RoomService) CreateRoom(ctx context.Context, ownerID int64, roomType domain.RoomType, name string) (*domain.Room, error) {
	if roomType != domain.RoomTypePrivate && roomType != domain.RoomTypeCasual && roomType != domain.RoomTypeMatch {
		return nil, fmt.Errorf("%w: type must be one of private, casual, match", errs.ErrInvalidInput)
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
		Settings: domain.RoomSettings{},
	}
	if roomType == domain.RoomTypeMatch {
		room.RefereeUserID = &ownerID
	}
	if err := s.rooms.Create(ctx, room); err != nil {
		s.log.Error("failed to create room", zap.Int64("owner_id", ownerID), zap.Error(err))
		return nil, err
	}
	s.log.Info("room created",
		zap.String("room_id", room.ID.Hex()),
		zap.String("code", room.Code),
		zap.String("type", string(roomType)),
		zap.Int64("owner_id", ownerID),
	)
	return room, nil
}

// GetRooms returns a paginated room directory using database-side filters.
func (s *RoomService) GetRooms(ctx context.Context, params paginate.Params, filter repository.RoomListFilter) (paginate.Result[domain.Room], error) {
	return s.rooms.List(ctx, params, filter)
}

// GetRoom fetches a room by id.
func (s *RoomService) GetRoom(ctx context.Context, id bson.ObjectID) (*domain.Room, error) {
	return s.rooms.ByID(ctx, id)
}

// GetRoomByCode fetches a room by invite code.
func (s *RoomService) GetRoomByCode(ctx context.Context, code string) (*domain.Room, error) {
	return s.rooms.ByCode(ctx, code)
}

// SetTeams assigns the red and blue team references for a room. Both teams
// must exist and be ready (leader + strategist, R1), must differ, and must not
// share players. Nil clears the corresponding reference.
func (s *RoomService) SetTeams(ctx context.Context, callerID int64, roomID bson.ObjectID, redTeamID, blueTeamID *bson.ObjectID) (*domain.Room, error) {
	room, err := s.authorizedRoomConfiguration(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	redTeam, err := s.loadLinkableTeam(ctx, redTeamID, "red_team_id")
	if err != nil {
		return nil, err
	}
	blueTeam, err := s.loadLinkableTeam(ctx, blueTeamID, "blue_team_id")
	if err != nil {
		return nil, err
	}
	if redTeamID != nil && blueTeamID != nil && *redTeamID == *blueTeamID {
		return nil, errs.NewValidationError(
			errs.FieldError{Field: "blue_team_id", Rule: "distinct", Message: "red and blue teams must differ"},
		)
	}
	if redTeam != nil && blueTeam != nil {
		if shared := sharedPlayers(redTeam.Players, blueTeam.Players); len(shared) > 0 {
			return nil, errs.NewValidationError(
				errs.FieldError{Field: "blue_team_id", Rule: "no_overlap", Message: fmt.Sprintf("teams share players: %v", shared)},
			)
		}
	}
	return s.updateRoomFields(ctx, roomID, bson.M{
		"settings.red_team_id":  redTeamID,
		"settings.blue_team_id": blueTeamID,
	}, true)
}

// loadLinkableTeam loads a team by id and enforces the readiness gate (R1).
// A nil id yields a nil team without error.
func (s *RoomService) loadLinkableTeam(ctx context.Context, id *bson.ObjectID, field string) (*domain.Team, error) {
	if id == nil {
		return nil, nil
	}
	team, err := s.teams.ByID(ctx, *id)
	if err != nil {
		return nil, err
	}
	if !team.IsReady() {
		return nil, errs.NewValidationError(
			errs.FieldError{Field: field, Rule: "ready", Message: "team must have both a leader and a strategist before it can be linked"},
		)
	}
	return team, nil
}

func sharedPlayers(a, b []int64) []int64 {
	seen := make(map[int64]struct{}, len(a))
	for _, id := range a {
		seen[id] = struct{}{}
	}
	var shared []int64
	for _, id := range b {
		if _, ok := seen[id]; ok {
			shared = append(shared, id)
		}
	}
	return shared
}

// SetStreamer assigns the streamer user id for a room.
func (s *RoomService) SetStreamer(ctx context.Context, callerID int64, roomID bson.ObjectID, uid *int64) (*domain.Room, error) {
	room, err := s.authorizedRoomConfiguration(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.streamer_user_id": uid}, true)
}

// SetMappool links a mappool entity to the room. The mappool must exist; its
// entry invariants were validated at CRUD time. Nil clears the reference.
func (s *RoomService) SetMappool(ctx context.Context, callerID int64, roomID bson.ObjectID, mappoolID *bson.ObjectID) (*domain.Room, error) {
	room, err := s.authorizedRoomConfiguration(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	if mappoolID != nil {
		if _, err := s.mappools.ByID(ctx, *mappoolID); err != nil {
			return nil, err
		}
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.mappool_id": mappoolID}, true)
}

// SetBPOrder sets the pick/ban order for a room.
func (s *RoomService) SetBPOrder(ctx context.Context, callerID int64, roomID bson.ObjectID, order domain.BPOrder) (*domain.Room, error) {
	room, err := s.authorizedRoomConfiguration(ctx, callerID, roomID)
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

// SetMPLink sets the multiplayer link for a room.
func (s *RoomService) SetMPLink(ctx context.Context, callerID int64, roomID bson.ObjectID, link string) (*domain.Room, error) {
	room, err := s.authorizedRoom(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if room.Type == domain.RoomTypeMatch {
		if _, err := irc.ChannelFromMPLink(link); err != nil {
			return nil, fmt.Errorf("%w: formal match multiplayer link must use https://osu.ppy.sh/community/matches/<room-id>", errs.ErrInvalidInput)
		}
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.mp_link": &link}, false)
}

// SetStreamLink sets the stream link for a room.
func (s *RoomService) SetStreamLink(ctx context.Context, callerID int64, roomID bson.ObjectID, link string) (*domain.Room, error) {
	room, err := s.authorizedRoomConfiguration(ctx, callerID, roomID)
	if err != nil {
		return nil, err
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"settings.stream_link": &link}, true)
}

// authorizedRoomConfiguration keeps formal match setup under administrator
// control. Assigned referees retain the separate MP-link and command paths.
func (s *RoomService) authorizedRoomConfiguration(ctx context.Context, callerID int64, roomID bson.ObjectID) (*domain.Room, error) {
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
	if room.Type == domain.RoomTypeMatch || room.OwnerID != user.OnlineID {
		return nil, errs.ErrForbidden
	}
	return room, nil
}

// SetReferee assigns the referee for a formal room. Only administrators may
// change this relationship, and only before the match is bootstrapped.
func (s *RoomService) SetReferee(ctx context.Context, callerID int64, roomID bson.ObjectID, refereeID *int64) (*domain.Room, error) {
	caller, err := s.currentEligibleUser(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if !caller.HasRole(domain.RoleAdmin) {
		return nil, errs.ErrForbidden
	}
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if room.Type != domain.RoomTypeMatch {
		return nil, fmt.Errorf("%w: referee_user_id can only be set on match rooms", errs.ErrInvalidInput)
	}
	if err := requireRoomSetupOpen(room); err != nil {
		return nil, err
	}
	if refereeID != nil {
		referee, findErr := s.currentEligibleUser(ctx, *refereeID)
		if findErr != nil {
			return nil, findErr
		}
		if !referee.HasRole(domain.RoleReferee) {
			return nil, errs.ErrForbidden
		}
	}
	return s.updateRoomFields(ctx, roomID, bson.M{"referee_user_id": refereeID}, true)
}

// UpdateRoomMetadata replaces the administrator-managed room metadata in one
// setup-locked write. It intentionally does not alter match state.
func (s *RoomService) UpdateRoomMetadata(ctx context.Context, callerID int64, roomID bson.ObjectID, update RoomMetadataUpdate) (*domain.Room, error) {
	admin, err := s.currentEligibleUser(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if !admin.HasRole(domain.RoleAdmin) {
		return nil, errs.ErrForbidden
	}
	if update.Name == "" {
		return nil, fmt.Errorf("%w: name is required", errs.ErrInvalidInput)
	}
	room, err := s.rooms.ByID(ctx, roomID)
	if err != nil {
		return nil, err
	}
	if update.RefereeUserID != nil && room.Type != domain.RoomTypeMatch {
		return nil, fmt.Errorf("%w: referee_user_id can only be set on match rooms", errs.ErrInvalidInput)
	}
	if update.RefereeUserID != nil {
		referee, findErr := s.currentEligibleUser(ctx, *update.RefereeUserID)
		if findErr != nil {
			return nil, findErr
		}
		if !referee.HasRole(domain.RoleReferee) {
			return nil, errs.ErrForbidden
		}
	}
	fields := bson.M{
		"name":                      update.Name,
		"round":                     update.Round,
		"scheduled_at":              update.ScheduledAt,
		"referee_user_id":           update.RefereeUserID,
		"settings.streamer_user_id": update.StreamerUserID,
	}
	if err := s.rooms.UpdateFields(ctx, room.ID, fields, true); err != nil {
		return nil, err
	}
	return s.rooms.ByID(ctx, room.ID)
}

// UpdateRoomMetadataPartial applies an incremental update to the
// administrator-managed room metadata: only the fields present in the patch
// are written, absent fields keep their stored values. It intentionally does
// not alter match state and cannot clear optional fields.
func (s *RoomService) UpdateRoomMetadataPartial(ctx context.Context, callerID int64, roomID bson.ObjectID, patch RoomMetadataPatch) (*domain.Room, error) {
	admin, err := s.currentEligibleUser(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if !admin.HasRole(domain.RoleAdmin) {
		return nil, errs.ErrForbidden
	}
	fields := bson.M{}
	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, fmt.Errorf("%w: name cannot be empty", errs.ErrInvalidInput)
		}
		fields["name"] = *patch.Name
	}
	if patch.Round != nil {
		fields["round"] = *patch.Round
	}
	if patch.ScheduledAt != nil {
		fields["scheduled_at"] = patch.ScheduledAt
	}
	if patch.RefereeUserID != nil {
		room, findErr := s.rooms.ByID(ctx, roomID)
		if findErr != nil {
			return nil, findErr
		}
		if room.Type != domain.RoomTypeMatch {
			return nil, fmt.Errorf("%w: referee_user_id can only be set on match rooms", errs.ErrInvalidInput)
		}
		referee, findErr := s.currentEligibleUser(ctx, *patch.RefereeUserID)
		if findErr != nil {
			return nil, findErr
		}
		if !referee.HasRole(domain.RoleReferee) {
			return nil, errs.ErrForbidden
		}
		fields["referee_user_id"] = patch.RefereeUserID
	}
	if patch.StreamerUserID != nil {
		fields["settings.streamer_user_id"] = patch.StreamerUserID
	}
	if len(fields) == 0 {
		return nil, errs.ErrInvalidInput
	}
	if err := s.rooms.UpdateFields(ctx, roomID, fields, true); err != nil {
		return nil, err
	}
	return s.rooms.ByID(ctx, roomID)
}

// StartMatch creates a match from the room's linked entities and transitions the room.
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
		redTeam, blueTeam, mappool, loadErr := s.roomStartEntities(ctx, room)
		if loadErr != nil {
			return nil, loadErr
		}
		now := s.now().UTC()
		seed, seedErr := BuildFormalMatchSeed(*room, redTeam, blueTeam, mappool, now)
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
	redTeam, blueTeam, mappool, loadErr := s.roomStartEntities(ctx, room)
	if loadErr != nil {
		return nil, loadErr
	}
	if missing := MissingStartRequirements(*room, redTeam, blueTeam, mappool); len(missing) > 0 {
		fields := make([]errs.FieldError, 0, len(missing))
		for _, m := range missing {
			fields = append(fields, errs.FieldError{
				Field:   m,
				Rule:    "required",
				Message: fmt.Sprintf("%s is required before starting the match", m),
			})
		}
		return nil, errs.NewValidationError(fields...)
	}

	pool := domain.NewPool()
	if mappool != nil {
		pool = mappool.ToRuntime()
	}
	match := domain.NewMatch(*room, redTeam.Snapshot(domain.TeamSideRed), blueTeam.Snapshot(domain.TeamSideBlue), pool)
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
		s.log.Error("failed to create match", zap.String("room_id", roomID.Hex()), zap.Error(err))
		return nil, err
	}
	room.MatchID = &match.ID
	if err := s.rooms.Update(ctx, room); err != nil {
		s.log.Error("failed to link match to room", zap.String("room_id", roomID.Hex()), zap.String("match_id", match.ID.Hex()), zap.Error(err))
		return nil, err
	}
	s.log.Info("audit: match started",
		zap.String("room_id", roomID.Hex()),
		zap.String("match_id", match.ID.Hex()),
		zap.Int64("caller_id", callerID),
	)
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

// roomStartEntities resolves the Team / Mappool entities referenced by the
// room settings. A broken reference (deleted entity) fails with ErrNotFound.
func (s *RoomService) roomStartEntities(ctx context.Context, room *domain.Room) (*domain.Team, *domain.Team, *domain.Mappool, error) {
	redTeam, err := s.loadTeamRef(ctx, room.Settings.RedTeamID)
	if err != nil {
		return nil, nil, nil, err
	}
	blueTeam, err := s.loadTeamRef(ctx, room.Settings.BlueTeamID)
	if err != nil {
		return nil, nil, nil, err
	}
	var mappool *domain.Mappool
	if room.Settings.MappoolID != nil {
		mappool, err = s.mappools.ByID(ctx, *room.Settings.MappoolID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return redTeam, blueTeam, mappool, nil
}

func (s *RoomService) loadTeamRef(ctx context.Context, id *bson.ObjectID) (*domain.Team, error) {
	if id == nil {
		return nil, nil
	}
	return s.teams.ByID(ctx, *id)
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
	if room.Type == domain.RoomTypeMatch {
		if !user.HasRole(domain.RoleReferee) || room.RefereeUserID == nil || *room.RefereeUserID != user.OnlineID {
			return nil, errs.ErrForbidden
		}
		return room, nil
	}
	if room.OwnerID != user.OnlineID {
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
