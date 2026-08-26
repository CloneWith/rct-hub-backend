package graphql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"rctHubBackend/internal/beatmapmetadata"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/errs"
	"rctHubBackend/pkg/paginate"
)

func (r *Resolver) authorizeIRCObservation(ctx context.Context, matchID, channel string) error {
	room, err := r.authorizeIRCMatch(ctx, matchID)
	if err != nil {
		return err
	}
	linkedChannel, channelErr := irc.ChannelFromMPLink(*room.Settings.MPLink)
	if channelErr != nil || linkedChannel != channel || !irc.MatchChannel(channel) {
		return fmt.Errorf("IRC channel is not linked to this match")
	}
	return nil
}

func (r *Resolver) authorizeIRCMatch(ctx context.Context, matchID string) (*domain.Room, error) {
	_, room, err := r.authorizeRefereeMatchContext(ctx, matchID)
	if err != nil {
		return nil, err
	}
	if room.Settings.MPLink == nil {
		return nil, fmt.Errorf("match has no multiplayer link")
	}
	return room, nil
}

func (r *Resolver) authorizeRefereeMatch(ctx context.Context, matchID string) (*service.FormalMatch, error) {
	match, _, err := r.authorizeRefereeMatchContext(ctx, matchID)
	return match, err
}

func (r *Resolver) authorizeRefereeMatchContext(ctx context.Context, matchID string) (*service.FormalMatch, *domain.Room, error) {
	if r == nil || r.formal == nil {
		return nil, nil, fmt.Errorf("match service is unavailable")
	}
	parsed, err := bson.ObjectIDFromHex(matchID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid match ID")
	}
	match, err := r.formal.ByID(ctx, parsed)
	if err != nil {
		return nil, nil, err
	}
	room, user, err := r.privateMatchContext(ctx, matchID, match.RoomID.Hex())
	if err != nil {
		return nil, nil, err
	}
	if err := authorizeRefereeViewer(user, room); err != nil {
		return nil, nil, err
	}
	return match, room, nil
}

type CommandExecutor interface {
	Execute(context.Context, matchcommand.Request) (matchcommand.Result, error)
}

type AuditReader interface {
	ListActions(context.Context, bson.ObjectID, int) ([]persistence.MatchActionDocument, error)
}

type AutomationIssueReader interface {
	ListFailedEvents(context.Context, bson.ObjectID, int64) ([]persistence.MatchOutboxDocument, error)
	RetryFailedEvent(context.Context, bson.ObjectID, string) error
}

type FormalMatchReader interface {
	ByID(context.Context, bson.ObjectID) (*service.FormalMatch, error)
	ByCode(context.Context, string) (*service.FormalMatch, error)
	List(context.Context, paginate.Params) (paginate.Result[service.FormalMatch], error)
}

type BeatmapReader interface {
	GetByOsuID(context.Context, int64) (*domain.Beatmap, error)
}

type BeatmapMetadataReader interface {
	State(context.Context, int64) (beatmapmetadata.Record, error)
	Beatmap(context.Context, int64) (*domain.Beatmap, error)
	Retry(context.Context, int64) error
}

type PrivateUserReader interface {
	GetByOsuID(context.Context, int64) (*domain.User, error)
}

// UserFetcher is the osu! read-through cache (Redis → MongoDB → osu! API).
// GetUser upserts on cold fetch, which powers the admin "add user" flow (D4).
type UserFetcher interface {
	GetUser(context.Context, int64) (*domain.User, error)
}

type PrivateRoomReader interface {
	GetRoom(context.Context, bson.ObjectID) (*domain.Room, error)
}

// TeamReader loads team entities by ID. It backs the room-settings team
// resolvers and the strategist/captain access predicates.
type TeamReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Team, error)
}

// teamServiceReader adapts the TeamService getter to the TeamReader shape.
type teamServiceReader struct{ svc *service.TeamService }

func (t teamServiceReader) ByID(ctx context.Context, id bson.ObjectID) (*domain.Team, error) {
	return t.svc.Get(ctx, id)
}

type IRCObservationReader interface {
	List(context.Context, string, int64) ([]persistence.IRCObservation, error)
	ByID(context.Context, string) (*persistence.IRCObservation, error)
	Reject(context.Context, string, string, int64) error
	ClaimConfirmation(context.Context, string, bson.ObjectID, string, string, matchengine.TeamSide, int64) (*persistence.IRCObservation, error)
	FinalizeConfirmation(context.Context, string, string) error
	ReleaseConfirmation(context.Context, string, string) error
}
type IRCJobReader interface {
	List(context.Context, bson.ObjectID, int64) ([]irc.Job, error)
	Retry(context.Context, bson.ObjectID, string, string, time.Time) error
}
type IRCStatusReader interface{ Status() irc.ConnectionStatus }

type Resolver struct {
	svc        *service.Services
	commands   CommandExecutor
	audit      AuditReader
	automation AutomationIssueReader
	formal     FormalMatchReader
	beatmaps   BeatmapReader
	metadata   BeatmapMetadataReader
	users      PrivateUserReader
	rooms      PrivateRoomReader
	teams      TeamReader
	irc        IRCObservationReader
	ircJobs    IRCJobReader
	ircStatus  IRCStatusReader
	fetcher    UserFetcher
}

func (r *Resolver) WithIRCReader(reader IRCObservationReader) *Resolver { r.irc = reader; return r }
func (r *Resolver) WithIRCJobs(reader IRCJobReader) *Resolver           { r.ircJobs = reader; return r }
func (r *Resolver) WithIRCStatus(reader IRCStatusReader) *Resolver      { r.ircStatus = reader; return r }

func (r *Resolver) WithAuditReader(reader AuditReader) *Resolver {
	r.audit = reader
	return r
}

func (r *Resolver) WithAutomationIssues(reader AutomationIssueReader) *Resolver {
	r.automation = reader
	return r
}

func NewResolver(svc *service.Services, commands ...CommandExecutor) *Resolver {
	resolver := &Resolver{svc: svc}
	if svc != nil {
		resolver.formal = svc.FormalMatches
		resolver.beatmaps = svc.Beatmaps
		resolver.users = svc.Users
		resolver.rooms = svc.Rooms
		if svc.Teams != nil {
			resolver.teams = teamServiceReader{svc: svc.Teams}
		}
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

// WithTeamReader overrides the team lookup channel (used by fixture-backed
// mocks where the service container is absent).
func (r *Resolver) WithTeamReader(teams TeamReader) *Resolver {
	r.teams = teams
	return r
}

func (r *Resolver) WithBeatmapReader(reader BeatmapReader) *Resolver {
	r.beatmaps = reader
	return r
}

func (r *Resolver) WithBeatmapMetadata(reader BeatmapMetadataReader) *Resolver {
	r.metadata = reader
	return r
}

func (r *Resolver) WithFormalMatchReader(reader FormalMatchReader) *Resolver {
	r.formal = reader
	return r
}

func (r *Resolver) WithUserFetcher(fetcher UserFetcher) *Resolver {
	r.fetcher = fetcher
	return r
}

// loadLinkedTeams resolves the room's red/blue team entity references.
// Missing references yield nil (unlinked side) rather than an error so the
// access predicates treat them as unassigned; a dangling reference surfaces
// the underlying read error.
func (r *Resolver) loadLinkedTeams(ctx context.Context, room *domain.Room) (*domain.Team, *domain.Team, error) {
	if room == nil {
		return nil, nil, nil
	}
	var source TeamReader
	if r != nil && r.teams != nil {
		source = r.teams
	}
	if source == nil {
		if room.Settings.RedTeamID == nil && room.Settings.BlueTeamID == nil {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("team service is unavailable")
	}
	var redTeam, blueTeam *domain.Team
	if room.Settings.RedTeamID != nil {
		team, err := source.ByID(ctx, *room.Settings.RedTeamID)
		if err != nil {
			return nil, nil, err
		}
		redTeam = team
	}
	if room.Settings.BlueTeamID != nil {
		team, err := source.ByID(ctx, *room.Settings.BlueTeamID)
		if err != nil {
			return nil, nil, err
		}
		blueTeam = team
	}
	return redTeam, blueTeam, nil
}

// loadTeamByID resolves a single room-settings team reference to its entity.
// A dangling reference (team deleted after linking) or an unparsable id
// yields nil so the GraphQL field resolves to null instead of failing the
// whole query; a broken lookup channel surfaces its error.
func (r *Resolver) loadTeamByID(ctx context.Context, id string) (*domain.Team, error) {
	if id == "" {
		return nil, nil
	}
	if r == nil || r.teams == nil {
		return nil, fmt.Errorf("team service is unavailable")
	}
	parsed, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return nil, nil
	}
	team, err := r.teams.ByID(ctx, parsed)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) || errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return team, nil
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
