package service

import (
	"rctHubBackend/internal/repository"
)

// Services wires together all domain services.
type Services struct {
	Rooms  *RoomService
	Matchs *MatchService
	Moves  *MoveService
}

// NewServices creates a service container from repositories.
func NewServices(repos *repository.Repositories) *Services {
	return &Services{
		Rooms:  NewRoomService(repos.Rooms, repos.Matches),
		Matchs: NewMatchService(repos.Matches, repos.Rooms, repos.Moves),
		Moves:  NewMoveService(repos.Moves),
	}
}
