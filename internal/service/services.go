package service

import (
	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/repository"

	"go.uber.org/zap"
)

// Services wires together all domain services.
type Services struct {
	Rooms         *RoomService
	Matchs        *MatchService
	Moves         *MoveService
	Users         *UserService
	Beatmaps      *BeatmapService
	Announcements *AnnouncementService
	Teams         *TeamService
	Mappools      *MappoolService
	FormalMatches *FormalMatchReadService
}

// NewServices creates a service container from repositories.
// invalidator is used by UserService and BeatmapService to invalidate
// Redis cache entries when local-only fields are modified.
func NewServices(repos *repository.Repositories, invalidator CacheInvalidator, logs *logger.Provider, sessionRevokers ...authsession.Revoker) *Services {
	auditLog := zap.NewNop()
	storageLog := zap.NewNop()
	if logs != nil {
		auditLog = logs.Get(string(logger.CatAudit))
		storageLog = logs.Get(string(logger.CatStorage))
	}

	return &Services{
		Rooms:         NewRoomService(repos.Rooms, repos.Matches, repos.Users, repos.Teams, repos.Mappools, repos.FormalMatches, repos.MatchSnapshots, nil, auditLog),
		Matchs:        NewMatchService(repos.Matches, repos.Rooms, repos.Teams, repos.Moves, repos.Results),
		Moves:         NewMoveService(repos.Moves),
		Users:         NewUserService(repos.Users, invalidator, sessionRevokers...).WithLogger(auditLog),
		Beatmaps:      NewBeatmapService(repos.Beatmaps, invalidator, storageLog),
		Announcements: NewAnnouncementService(repos.Announcements),
		Teams:         NewTeamService(repos.Teams),
		Mappools:      NewMappoolService(repos.Mappools),
		FormalMatches: NewFormalMatchReadService(repos.Matches, repos.MatchSnapshots),
	}
}

// WithMatchCommandDriver wires a MatchCommandDriver into the already-built
// RoomService. The orchestrator is created later in server lifecycle (it
// needs the matchcommand store), so this setter closes the wiring loop
// without forcing NewServices to take a circular dependency. Returns the
// same *Services for fluent use.
func (s *Services) WithMatchCommandDriver(driver MatchCommandDriver) *Services {
	if s != nil && s.Rooms != nil && driver != nil {
		s.Rooms.matchCommands = driver
	}
	return s
}
