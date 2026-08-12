package ircpublisher

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

type EventStore interface {
	ListUnpublishedEvents(context.Context, int64) ([]persistence.MatchOutboxDocument, error)
	MarkEventPublished(context.Context, string, time.Time) error
	MarkEventFailed(context.Context, string, string) error
}
type JobStore interface {
	Enqueue(context.Context, irc.Job) error
}
type MatchReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Match, error)
}
type RoomReader interface {
	ByID(context.Context, bson.ObjectID) (*domain.Room, error)
}
type UserReader interface {
	ByOsuID(context.Context, int64) (*domain.User, error)
}

type Publisher struct {
	events  EventStore
	jobs    JobStore
	matches MatchReader
	rooms   RoomReader
	users   UserReader
}

func New(events EventStore, jobs JobStore, matches MatchReader, rooms RoomReader, users UserReader) *Publisher {
	return &Publisher{events: events, jobs: jobs, matches: matches, rooms: rooms, users: users}
}

func (p *Publisher) RunOnce(ctx context.Context) error {
	if p == nil || p.events == nil || p.jobs == nil || p.matches == nil || p.rooms == nil || p.users == nil {
		return fmt.Errorf("IRC publisher is not configured")
	}
	events, err := p.events.ListUnpublishedEvents(ctx, 100)
	if err != nil {
		return err
	}
	for _, event := range events {
		jobs, planErr := p.plan(ctx, event)
		if planErr != nil {
			if err := p.events.MarkEventFailed(ctx, event.EventID, planErr.Error()); err != nil {
				return fmt.Errorf("record IRC plan failure for event %q: %w", event.EventID, err)
			}
			continue
		}
		failed := false
		for _, job := range jobs {
			if err := p.jobs.Enqueue(ctx, job); err != nil {
				if markErr := p.events.MarkEventFailed(ctx, event.EventID, err.Error()); markErr != nil {
					return fmt.Errorf("enqueue IRC job and record event %q failure: %v; %w", event.EventID, err, markErr)
				}
				failed = true
				break
			}
		}
		if !failed {
			if err := p.events.MarkEventPublished(ctx, event.EventID, time.Now().UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Publisher) plan(ctx context.Context, document persistence.MatchOutboxDocument) ([]irc.Job, error) {
	if document.Type != matchengine.EventMatchStarted && document.Type != matchengine.EventPiecePlaced && document.Type != matchengine.EventTBStarted {
		return nil, nil
	}
	match, err := p.matches.ByID(ctx, document.MatchID)
	if err != nil {
		return nil, err
	}
	room, err := p.rooms.ByID(ctx, match.RoomID)
	if err != nil {
		return nil, err
	}
	channel, err := roomChannel(room)
	if err != nil {
		return nil, err
	}
	poolSlotID, _ := document.Payload.Lookup("poolSlotId").StringValueOK()
	switch document.Type {
	case matchengine.EventMatchStarted:
		ids := append(append([]int64{}, match.TeamRed.Players...), match.TeamBlue.Players...)
		jobs := make([]irc.Job, 0, len(ids))
		seen := make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return nil, fmt.Errorf("match roster contains invalid osu! ID")
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			user, err := p.users.ByOsuID(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("load osu! username for player %d: %w", id, err)
			}
			username, err := banchoUsername(user, id)
			if err != nil {
				return nil, err
			}
			invite := job(document.MatchID, channel, document.Sequence, document.EventID+"-invite-"+strconv.FormatInt(id, 10), "INVITE", fmt.Sprintf("PRIVMSG %s :!mp invite %s", channel, username))
			invite.AckTarget = username
			jobs = append(jobs, invite)
		}
		return jobs, nil
	case matchengine.EventPiecePlaced:
		return mapJob(document.MatchID, channel, document.Sequence, document.EventID, "MAP", match.Mappool, poolSlotID)
	case matchengine.EventTBStarted:
		for index, piece := range match.Mappool.Slots[domain.PieceModTB] {
			if piece.BeatmapID != nil && *piece.BeatmapID > 0 {
				return mapJob(document.MatchID, channel, document.Sequence, document.EventID, "TB_MAP", match.Mappool, fmt.Sprintf("TB-%d", index+1))
			}
		}
	}
	return nil, nil
}

func banchoUsername(user *domain.User, expectedID int64) (string, error) {
	if user == nil || user.OnlineID != expectedID {
		return "", fmt.Errorf("player %d has no matching local osu! account", expectedID)
	}
	username := strings.Join(strings.Fields(user.Username), "_")
	if username == "" || strings.ContainsAny(username, "\r\n") {
		return "", fmt.Errorf("player %d has no usable osu! username", expectedID)
	}
	return username, nil
}

func mapJob(matchID bson.ObjectID, channel string, sequence uint64, eventID, kind string, pool domain.Mappool, slotID string) ([]irc.Job, error) {
	slot, ok := domain.ParsePoolSlot(slotID)
	if !ok {
		return nil, fmt.Errorf("invalid pool slot %q", slotID)
	}
	piece := pool.FindSlot(slot)
	if piece == nil || piece.BeatmapID == nil || *piece.BeatmapID <= 0 {
		return nil, fmt.Errorf("pool slot %q has no playable beatmap", slotID)
	}
	return []irc.Job{job(matchID, channel, sequence, eventID+"-map", kind, fmt.Sprintf("PRIVMSG %s :!mp map %d", channel, *piece.BeatmapID))}, nil
}
func job(matchID bson.ObjectID, channel string, sequence uint64, id, kind, payload string) irc.Job {
	return irc.Job{ID: id, MatchID: matchID.Hex(), Channel: channel, Sequence: sequence, Kind: kind, Payload: []byte(payload), Status: irc.JobPending}
}
func roomChannel(room *domain.Room) (string, error) {
	if room == nil || room.Settings.MPLink == nil {
		return "", fmt.Errorf("match has no multiplayer link")
	}
	return irc.ChannelFromMPLink(*room.Settings.MPLink)
}
