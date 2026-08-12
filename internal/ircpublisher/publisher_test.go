package ircpublisher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

type eventMemory struct {
	events      []persistence.MatchOutboxDocument
	published   int
	failed      int
	lastFailure string
	markErr     error
}

func (m *eventMemory) ListUnpublishedEvents(context.Context, int64) ([]persistence.MatchOutboxDocument, error) {
	return m.events, nil
}
func (m *eventMemory) MarkEventPublished(context.Context, string, time.Time) error {
	m.published++
	return nil
}
func (m *eventMemory) MarkEventFailed(_ context.Context, _ string, message string) error {
	m.failed++
	m.lastFailure = message
	return m.markErr
}

type jobMemory struct {
	jobs map[string]irc.Job
	err  error
}

type userMemory struct{ users map[int64]*domain.User }

func (m userMemory) ByOsuID(_ context.Context, id int64) (*domain.User, error) {
	user, ok := m.users[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (m *jobMemory) Enqueue(_ context.Context, job irc.Job) error {
	if m.err != nil {
		return m.err
	}
	if m.jobs == nil {
		m.jobs = map[string]irc.Job{}
	}
	if _, ok := m.jobs[job.ID]; !ok {
		m.jobs[job.ID] = job
	}
	return nil
}

func TestPublisherMapsMatchStartRosterAndTB(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	tbID := int64(999)
	mp := "https://osu.ppy.sh/community/matches/42"
	match := &domain.Match{
		ID: matchID, RoomID: roomID,
		TeamRed: domain.Team{Players: []int64{1, 2}}, TeamBlue: domain.Team{Players: []int64{2, 3}},
		Mappool: domain.Mappool{Slots: map[domain.PieceMod][]domain.Piece{domain.PieceModTB: {{BeatmapID: &tbID}}}},
	}
	tests := []struct {
		name  string
		type_ matchengine.EventType
		want  map[string]string
	}{
		{name: "roster", type_: matchengine.EventMatchStarted, want: map[string]string{
			"event-start-invite-1": "PRIVMSG #mp_42 :!mp invite Red_One",
			"event-start-invite-2": "PRIVMSG #mp_42 :!mp invite Shared_Player",
			"event-start-invite-3": "PRIVMSG #mp_42 :!mp invite Blue_Three",
		}},
		{name: "tiebreaker", type_: matchengine.EventTBStarted, want: map[string]string{
			"event-tb-map": "PRIVMSG #mp_42 :!mp map 999",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventID := "event-start"
			if tt.type_ == matchengine.EventTBStarted {
				eventID = "event-tb"
			}
			events := &eventMemory{events: []persistence.MatchOutboxDocument{{EventID: eventID, MatchID: matchID, Type: tt.type_}}}
			jobs := &jobMemory{}
			users := userMemory{users: map[int64]*domain.User{
				1: {OnlineID: 1, Username: "Red One"},
				2: {OnlineID: 2, Username: "Shared_Player"},
				3: {OnlineID: 3, Username: "Blue Three"},
			}}
			publisher := New(events, jobs, matchMemory{match}, roomMemory{&domain.Room{ID: roomID, Settings: domain.RoomSettings{MPLink: &mp}}}, users)
			if err := publisher.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if events.published != 1 || len(jobs.jobs) != len(tt.want) {
				t.Fatalf("published=%d jobs=%+v", events.published, jobs.jobs)
			}
			for id, payload := range tt.want {
				job := jobs.jobs[id]
				if job.MatchID != matchID.Hex() || job.Channel != "#mp_42" || string(job.Payload) != payload {
					t.Fatalf("job %s=%+v", id, job)
				}
				if tt.type_ == matchengine.EventTBStarted && job.Kind != "TB_MAP" {
					t.Fatalf("TB job kind=%q, want TB_MAP", job.Kind)
				}
			}
		})
	}
}

func TestPublisherAcceptsAuthoritativeEngineSlotIDs(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	mp := "https://osu.ppy.sh/community/matches/42"
	nmID, tbID := int64(123), int64(999)
	match := &domain.Match{
		ID: matchID, RoomID: roomID,
		Mappool: domain.Mappool{Slots: map[domain.PieceMod][]domain.Piece{
			domain.PieceModNM: {{BeatmapID: &nmID}},
			domain.PieceModTB: {{BeatmapID: &tbID}},
		}},
	}
	nmPayload := mustBSON(t, bson.M{"poolSlotId": "NM1"})
	tbPayload := mustBSON(t, bson.M{})
	events := &eventMemory{events: []persistence.MatchOutboxDocument{
		{EventID: "event-nm", MatchID: matchID, Type: matchengine.EventPiecePlaced, Payload: nmPayload},
		{EventID: "event-tb", MatchID: matchID, Type: matchengine.EventTBStarted, Payload: tbPayload},
	}}
	jobs := &jobMemory{}
	publisher := New(events, jobs, matchMemory{match}, roomMemory{&domain.Room{ID: roomID, Settings: domain.RoomSettings{MPLink: &mp}}}, userMemory{})
	if err := publisher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := string(jobs.jobs["event-nm-map"].Payload); got != "PRIVMSG #mp_42 :!mp map 123" {
		t.Fatalf("NM1 payload = %q", got)
	}
	if got := string(jobs.jobs["event-tb-map"].Payload); got != "PRIVMSG #mp_42 :!mp map 999" {
		t.Fatalf("TB payload = %q", got)
	}
}

func mustBSON(t *testing.T, value bson.M) []byte {
	t.Helper()
	encoded, err := bson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPublisherPersistsExplainableFailures(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	badLink := "https://osu.ppy.sh/community/matches/not-a-number"
	events := &eventMemory{events: []persistence.MatchOutboxDocument{{EventID: "event-1", MatchID: matchID, Type: matchengine.EventMatchStarted}}}
	publisher := New(events, &jobMemory{}, matchMemory{&domain.Match{ID: matchID, RoomID: roomID}}, roomMemory{&domain.Room{ID: roomID, Settings: domain.RoomSettings{MPLink: &badLink}}}, userMemory{})
	if err := publisher.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if events.failed != 1 || events.published != 0 || events.lastFailure == "" {
		t.Fatalf("failed=%d published=%d reason=%q", events.failed, events.published, events.lastFailure)
	}

	events = &eventMemory{events: []persistence.MatchOutboxDocument{{EventID: "event-2", MatchID: matchID, Type: matchengine.EventMatchStarted}}, markErr: errors.New("storage unavailable")}
	publisher = New(events, &jobMemory{}, matchMemory{&domain.Match{ID: matchID, RoomID: roomID}}, roomMemory{&domain.Room{ID: roomID, Settings: domain.RoomSettings{MPLink: &badLink}}}, userMemory{})
	if err := publisher.RunOnce(context.Background()); err == nil || !strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("error=%v", err)
	}
}

type matchMemory struct{ match *domain.Match }

func (m matchMemory) ByID(context.Context, bson.ObjectID) (*domain.Match, error) { return m.match, nil }

type roomMemory struct{ room *domain.Room }

func (m roomMemory) ByID(context.Context, bson.ObjectID) (*domain.Room, error) { return m.room, nil }

func TestPublisherMapsCommittedPieceToIdempotentMapJob(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	beatmapID := int64(123)
	payload, _ := bson.Marshal(bson.M{"type": string(matchengine.EventPiecePlaced), "poolSlotId": "NM-1"})
	events := &eventMemory{events: []persistence.MatchOutboxDocument{{EventID: "event-1", MatchID: matchID, Type: matchengine.EventPiecePlaced, Payload: payload}}}
	jobs := &jobMemory{}
	mp := "https://osu.ppy.sh/community/matches/42"
	match := &domain.Match{ID: matchID, RoomID: roomID, Mappool: domain.Mappool{Slots: map[domain.PieceMod][]domain.Piece{domain.PieceModNM: {{BeatmapID: &beatmapID}}}}}
	pub := New(events, jobs, matchMemory{match}, roomMemory{&domain.Room{ID: roomID, Settings: domain.RoomSettings{MPLink: &mp}}}, userMemory{})
	if err := pub.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pub.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(jobs.jobs) != 1 || string(jobs.jobs["event-1-map"].Payload) != "PRIVMSG #mp_42 :!mp map 123" {
		t.Fatalf("jobs=%+v", jobs.jobs)
	}
}
