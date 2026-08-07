package service

import (
	"rctHubBackend/internal/logger"
	"rctHubBackend/internal/repository"
)

// Services wires together all domain services.
type Services struct {
	Rooms         *RoomService
	Matchs        *MatchService
	Moves         *MoveService
	Users         *UserService
	Beatmaps      *BeatmapService
	Announcements *AnnouncementService
}

// NewServices creates a service container from repositories.
// invalidator is used by UserService and BeatmapService to invalidate
// Redis cache entries when local-only fields are modified.
// logs provides category-specific loggers for audit and storage logging.
func NewServices(repos *repository.Repositories, invalidator CacheInvalidator, logs *logger.Provider) *Services {
	auditLog := logs.Get(string(logger.CatAudit))
	storageLog := logs.Get(string(logger.CatStorage))

	return &Services{
		Rooms:         NewRoomService(repos.Rooms, repos.Matches, repos.Users, repos.FormalMatches, auditLog),
		Matchs:        NewMatchService(repos.Matches, repos.Rooms, repos.Moves, repos.Results),
		Moves:         NewMoveService(repos.Moves),
		Users:         NewUserService(repos.Users, invalidator, auditLog),
		Beatmaps:      NewBeatmapService(repos.Beatmaps, invalidator, storageLog),
		Announcements: NewAnnouncementService(repos.Announcements),
	}
}
