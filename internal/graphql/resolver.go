package graphql

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/paginate"
)

type CommandExecutor interface {
	Execute(context.Context, matchcommand.Request) (matchcommand.Result, error)
}

type AuditReader interface {
	ListActions(context.Context, bson.ObjectID, int) ([]persistence.MatchActionDocument, error)
}

type FormalMatchReader interface {
	ByID(context.Context, bson.ObjectID) (*service.FormalMatch, error)
	ByCode(context.Context, string) (*service.FormalMatch, error)
	List(context.Context, paginate.Params) (paginate.Result[service.FormalMatch], error)
}

type BeatmapReader interface {
	GetByOsuID(context.Context, int64) (*domain.Beatmap, error)
}

type PrivateUserReader interface {
	GetByOsuID(context.Context, int64) (*domain.User, error)
}

type PrivateRoomReader interface {
	GetRoom(context.Context, bson.ObjectID) (*domain.Room, error)
}

type Resolver struct {
	svc      *service.Services
	commands CommandExecutor
	audit    AuditReader
	formal   FormalMatchReader
	beatmaps BeatmapReader
	users    PrivateUserReader
	rooms    PrivateRoomReader
}

func (r *Resolver) WithAuditReader(reader AuditReader) *Resolver {
	r.audit = reader
	return r
}

func NewResolver(svc *service.Services, commands ...CommandExecutor) *Resolver {
	resolver := &Resolver{svc: svc}
	if svc != nil {
		resolver.formal = svc.FormalMatches
		resolver.beatmaps = svc.Beatmaps
		resolver.users = svc.Users
		resolver.rooms = svc.Rooms
	}
	if len(commands) > 0 {
		resolver.commands = commands[0]
	}
	return resolver
}

func (r *Resolver) WithPrivateReaders(users PrivateUserReader, rooms PrivateRoomReader) *Resolver {
	r.users = users
	r.rooms = rooms
	return r
}

func (r *Resolver) WithBeatmapReader(reader BeatmapReader) *Resolver {
	r.beatmaps = reader
	return r
}

func (r *Resolver) WithFormalMatchReader(reader FormalMatchReader) *Resolver {
	r.formal = reader
	return r
}

func buildPageParams(page, perPage *int) paginate.Params {
	params := paginate.Params{}
	if page != nil {
		params.Page = int64(*page)
	}
	if perPage != nil {
		params.PerPage = int64(*perPage)
	}
	params.Normalize()
	return params
}
