package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/pkg/jwtutil"
)

type fakeRealtimeSource struct {
	mu              sync.RWMutex
	state           matchengine.State
	latest          uint64
	events          []persistence.MatchOutboxDocument
	baselineVersion uint64
}

func (f *fakeRealtimeSource) Load(context.Context, bson.ObjectID) (matchengine.State, error) {
	return f.state, nil
}

func (f *fakeRealtimeSource) LatestEventSequenceAtVersion(_ context.Context, _ bson.ObjectID, version uint64) (uint64, error) {
	f.mu.Lock()
	f.baselineVersion = version
	f.mu.Unlock()
	return f.latest, nil
}
func (f *fakeRealtimeSource) LoadStateAtVersion(_ context.Context, _ bson.ObjectID, version uint64) (matchengine.State, error) {
	state := f.state.Clone()
	state.Version = version
	return state, nil
}
func (f *fakeRealtimeSource) ListEventsAfter(_ context.Context, _ bson.ObjectID, sequence uint64, _ int64) ([]persistence.MatchOutboxDocument, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var result []persistence.MatchOutboxDocument
	for _, event := range f.events {
		if event.Sequence > sequence {
			result = append(result, event)
		}
	}
	return result, nil
}

func (f *fakeRealtimeSource) setEvents(events []persistence.MatchOutboxDocument) {
	f.mu.Lock()
	f.events = events
	f.mu.Unlock()
}

func (f *fakeRealtimeSource) getBaselineVersion() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.baselineVersion
}

func TestGatewayRequiresAuthentication(t *testing.T) {
	gateway := NewGateway(&fakeRealtimeSource{}, &fakeRealtimeSource{}, jwtutil.NewSigner(strings.Repeat("x", 32), "test"), nil, "session", nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/ws/match", nil)
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestOriginPatternsPreserveConfiguredScheme(t *testing.T) {
	got := originPatterns([]string{"https://web.example.test", "http://localhost:3000"})
	if len(got) != 2 || got[0] != "https://web.example.test" || got[1] != "http://localhost:3000" {
		t.Fatalf("origin patterns=%v", got)
	}
}

func TestGatewayAcceptsOnlyConfiguredBrowserOrigin(t *testing.T) {
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	gateway := NewGateway(&fakeRealtimeSource{}, &fakeRealtimeSource{}, signer, nil, "session", []string{"https://web.example.test"}, nil, nil).
		withSubscribeTimeout(30 * time.Millisecond)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, _ := signer.Generate("user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")

	for _, test := range []struct {
		name   string
		origin string
		wantOK bool
	}{
		{name: "exact origin", origin: "https://web.example.test", wantOK: true},
		{name: "scheme mismatch", origin: "http://web.example.test", wantOK: false},
		{name: "host mismatch", origin: "https://other.example.test", wantOK: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{
				"Authorization": {"Bearer " + token},
				"Origin":        {test.origin},
			}})
			if test.wantOK {
				if err != nil {
					t.Fatalf("configured origin was rejected: %v", err)
				}
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			if err == nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				t.Fatal("unconfigured origin was accepted")
			}
			if response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("response=%v err=%v, want 403", response, err)
			}
		})
	}
}

func TestGatewayBoundsSubscriptionHandshake(t *testing.T) {
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	gateway := NewGateway(&fakeRealtimeSource{}, &fakeRealtimeSource{}, signer, nil, "session", nil, nil, nil).
		withSubscribeTimeout(30 * time.Millisecond)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, _ := signer.Generate("user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	readDone := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(ctx)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("subscription connection remained open after timeout")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscription connection was not reclaimed after timeout")
	}
}

func TestGatewayRejectsUnsupportedRealtimeSchema(t *testing.T) {
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	gateway := NewGateway(&fakeRealtimeSource{}, &fakeRealtimeSource{}, signer, nil, "session", nil, nil, nil)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, _ := signer.Generate("user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(envelope{Type: "subscribe", SchemaVersion: 2, MatchID: bson.NewObjectID().Hex()})); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil || got.Code != "UNSUPPORTED_SCHEMA_VERSION" || got.SchemaVersion != realtimeSchemaVersion || got.ServerTime == nil {
		t.Fatalf("response=%+v err=%v", got, err)
	}
}

func TestGatewayLimitsConnectionsPerAuthenticatedUser(t *testing.T) {
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	gateway := NewGateway(&fakeRealtimeSource{}, &fakeRealtimeSource{}, signer, nil, "session", nil, nil, nil).
		withConnectionLimits(2, 10).
		withSubscribeTimeout(time.Second)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, _ := signer.Generate("same-user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	header := http.Header{"Authorization": {"Bearer " + token}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	first, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(websocket.StatusNormalClosure, "")
	second, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(websocket.StatusNormalClosure, "")
	third, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		_ = third.Close(websocket.StatusNormalClosure, "")
		t.Fatal("third connection for one user was accepted")
	}
	if response == nil || response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("response=%v err=%v, want 429", response, err)
	}

	_ = first.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(time.Second)
	for {
		replacement, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: header})
		if err == nil {
			_ = replacement.Close(websocket.StatusNormalClosure, "")
			break
		}
		if response == nil || response.StatusCode != http.StatusTooManyRequests || time.Now().After(deadline) {
			t.Fatalf("released connection slot was not reusable: response=%v err=%v", response, err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGatewaySendsSnapshotThenOrderedEvent(t *testing.T) {
	matchID := bson.NewObjectID()
	source := &fakeRealtimeSource{state: matchengine.State{Version: 4}, latest: 8}
	gateway := NewGateway(source, source, jwtutil.NewSigner(strings.Repeat("x", 32), "test"), nil, "session", nil, func(context.Context, *jwtutil.Claims, bson.ObjectID) error { return nil }, nil)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, err := jwtutil.NewSigner(strings.Repeat("x", 32), "test").Generate("user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, mustJSON(envelope{Type: "subscribe", SchemaVersion: realtimeSchemaVersion, MatchID: matchID.Hex()})); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "snapshot" || got.SchemaVersion != realtimeSchemaVersion || got.ServerTime == nil || got.Sequence != 8 || got.Version != 4 {
		t.Fatalf("snapshot = %+v", got)
	}
	if source.getBaselineVersion() != 4 {
		t.Fatalf("baseline was selected at version %d, want snapshot version 4", source.getBaselineVersion())
	}
	source.setEvents([]persistence.MatchOutboxDocument{{MatchID: matchID, Sequence: 9, ResultingVersion: 5}})
	_, raw, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Type != "event" || got.Sequence != 9 || got.Snapshot == nil {
		t.Fatalf("event = %+v err=%v", got, err)
	}
}

func TestGatewayPublishesTypedEventEnvelope(t *testing.T) {
	event := persistence.MatchOutboxDocument{
		EventID: "event-1", Type: matchengine.EventPiecePlaced, ResultingVersion: 5,
		Payload: bson.Raw(bson.Raw{7, 0, 0, 0, 0x10, 'c', 'e', 'l', 'l', 0, 0, 0, 0, 0}),
	}
	// Use the public mapper directly as the contract seam; malformed BSON must
	// never leak a raw driver value to browsers.
	if _, err := mapEvent(event); err == nil {
		t.Fatal("malformed event payload was accepted")
	}
	valid, err := bson.Marshal(bson.M{"cell": "A1"})
	if err != nil {
		t.Fatal(err)
	}
	event.Payload = valid
	got, err := mapEvent(event)
	if err != nil || got.ID != "event-1" || got.Type != matchengine.EventPiecePlaced || got.Fact.Cell != "A1" {
		t.Fatalf("typed event=%+v err=%v", got, err)
	}
}

func TestRealtimeEventProjectionPreservesCamelCaseOutboxFacts(t *testing.T) {
	payload, err := bson.Marshal(bson.M{
		"team":          matchengine.TeamBlue,
		"poolSlotId":    "FM1",
		"boardPieceId":  "piece-1",
		"boardPieceIds": bson.A{"piece-2", "piece-3"},
		"cell":          "D4",
		"duration":      int64(2500 * time.Millisecond),
		"requestId":     "tb-request-1",
		"tbBasis":       matchengine.TBBasisCaptainAgreement,
		"playerIds":     bson.A{int64(123), int64(456)},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapEvent(persistence.MatchOutboxDocument{
		EventID: "event-facts", Type: matchengine.EventPieceRobbed,
		ResultingVersion: 9, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.Fact.Team == nil || *event.Fact.Team != matchengine.TeamBlue ||
		event.Fact.PoolSlotID != "FM1" || event.Fact.BoardPieceID != "piece-1" ||
		!reflect.DeepEqual(event.Fact.BoardPieceIDs, []string{"piece-2", "piece-3"}) ||
		event.Fact.Cell != "D4" || event.Fact.DurationMilliseconds == nil ||
		*event.Fact.DurationMilliseconds != 2500 || event.Fact.RequestID != "tb-request-1" ||
		event.Fact.TBBasis != matchengine.TBBasisCaptainAgreement ||
		!reflect.DeepEqual(event.Fact.PlayerIDs, []string{"123", "456"}) {
		t.Fatalf("projected event facts = %+v", event.Fact)
	}
}

func TestRealtimeProjectionUsesMillisecondsAndRedactsInternalReasons(t *testing.T) {
	started := time.Date(2026, time.August, 12, 10, 0, 0, 0, time.UTC)
	state := matchengine.State{
		Version: 3, Lifecycle: matchengine.LifecycleRunning, Phase: matchengine.PhasePick,
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue, ActiveTeam: matchengine.TeamRed,
		Board: matchengine.NewBoard(), Timer: matchengine.Timer{StartedAt: started, Duration: 90 * time.Second},
		RobberyUsed: map[matchengine.TeamSide]bool{}, TeamPauseUsed: map[matchengine.TeamSide]bool{},
		Rosters: map[matchengine.TeamSide]matchengine.Roster{}, PoolSlots: map[string]matchengine.PoolSlot{},
	}
	encoded := mustJSON(mapSnapshot(state))
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	timer, ok := document["timer"].(map[string]any)
	if !ok || timer["durationMilliseconds"] != float64(90000) {
		t.Fatalf("timer projection=%v", document["timer"])
	}

	payload, err := bson.Marshal(matchengine.Event{Type: matchengine.EventTimerPaused, Duration: 2 * time.Second, Reason: "private referee note"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapEvent(persistence.MatchOutboxDocument{EventID: "event-2", Type: matchengine.EventTimerPaused, Payload: payload})
	if err != nil || event.Fact.DurationMilliseconds == nil || *event.Fact.DurationMilliseconds != 2000 {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	encoded = mustJSON(event)
	if strings.Contains(string(encoded), "private referee note") || strings.Contains(string(encoded), "reason") || strings.Contains(string(encoded), "actor") {
		t.Fatalf("private event fields leaked: %s", encoded)
	}
}

func TestGatewayRequiresResyncOnSequenceGap(t *testing.T) {
	matchID := bson.NewObjectID()
	source := &fakeRealtimeSource{state: matchengine.State{Version: 4}, latest: 8, events: []persistence.MatchOutboxDocument{{MatchID: matchID, Sequence: 10, ResultingVersion: 6}}}
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	gateway := NewGateway(source, source, signer, nil, "session", nil, func(context.Context, *jwtutil.Claims, bson.ObjectID) error { return nil }, nil)
	server := httptest.NewServer(gateway)
	defer server.Close()
	token, _ := signer.Generate("user", 1, "tester", []domain.UserRole{domain.RolePlayer}, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = conn.Write(ctx, websocket.MessageText, mustJSON(envelope{Type: "subscribe", SchemaVersion: realtimeSchemaVersion, MatchID: matchID.Hex()}))
	_, _, _ = conn.Read(ctx)
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got envelope
	_ = json.Unmarshal(raw, &got)
	if got.Type != "resync_required" || got.NextSequence != 9 {
		t.Fatalf("response=%+v", got)
	}
}
