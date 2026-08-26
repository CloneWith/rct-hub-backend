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
		Rooms:         NewRoomService(repos.Rooms, repos.Matches, repos.Users, repos.Teams, repos.Mappools, repos.FormalMatches, auditLog),
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
