package service

import "rctHubBackend/internal/repository"

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
func NewServices(repos *repository.Repositories, invalidator CacheInvalidator) *Services {
	return &Services{
		Rooms:         NewRoomService(repos.Rooms, repos.Matches, repos.Users, repos.FormalMatches),
		Matchs:        NewMatchService(repos.Matches, repos.Rooms, repos.Moves, repos.Results),
		Moves:         NewMoveService(repos.Moves),
		Users:         NewUserService(repos.Users, invalidator),
		Beatmaps:      NewBeatmapService(repos.Beatmaps, invalidator),
		Announcements: NewAnnouncementService(repos.Announcements),
	}
}
