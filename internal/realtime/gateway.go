package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/pkg/jwtutil"
)

const (
	maxMessageSize               = 64 << 10
	writeTimeout                 = 5 * time.Second
	subscribeTimeout             = 10 * time.Second
	pollInterval                 = 500 * time.Millisecond
	defaultMaxConnectionsPerUser = 8
	defaultMaxConnections        = 256
	realtimeSchemaVersion        = 1
)

type SnapshotSource interface {
	Load(context.Context, bson.ObjectID) (matchengine.State, error)
}

type EventSource interface {
	ListEventsAfter(context.Context, bson.ObjectID, uint64, int64) ([]persistence.MatchOutboxDocument, error)
	LatestEventSequenceAtVersion(context.Context, bson.ObjectID, uint64) (uint64, error)
	LoadStateAtVersion(context.Context, bson.ObjectID, uint64) (matchengine.State, error)
}

type Authorizer func(context.Context, *jwtutil.Claims, bson.ObjectID) error

type Gateway struct {
	snapshots             SnapshotSource
	events                EventSource
	sessions              authsession.Resolver
	signer                *jwtutil.Signer
	cookie                string
	authorize             Authorizer
	origins               []string
	log                   *zap.Logger
	subscribeTimeout      time.Duration
	connectionMu          sync.Mutex
	connections           map[string]int
	maxConnectionsPerUser int
	maxConnections        int
}

func NewGateway(snapshots SnapshotSource, events EventSource, signer *jwtutil.Signer, sessions authsession.Resolver, cookie string, origins []string, authorize Authorizer, log *zap.Logger) *Gateway {
	if log == nil {
		log = zap.NewNop()
	}
	return &Gateway{snapshots: snapshots, events: events, signer: signer, sessions: sessions, cookie: cookie, origins: originPatterns(origins), authorize: authorize, log: log, subscribeTimeout: subscribeTimeout, connections: make(map[string]int), maxConnectionsPerUser: defaultMaxConnectionsPerUser, maxConnections: defaultMaxConnections}
}

func (g *Gateway) withSubscribeTimeout(timeout time.Duration) *Gateway {
	if timeout > 0 {
		g.subscribeTimeout = timeout
	}
	return g
}

func (g *Gateway) withConnectionLimits(perUser, total int) *Gateway {
	if perUser > 0 {
		g.maxConnectionsPerUser = perUser
	}
	if total > 0 {
		g.maxConnections = total
	}
	return g
}

func (g *Gateway) acquireConnection(key string) bool {
	g.connectionMu.Lock()
	defer g.connectionMu.Unlock()
	if g.maxConnections > 0 {
		total := 0
		for _, count := range g.connections {
			total += count
		}
		if total >= g.maxConnections {
			return false
		}
	}
	if g.maxConnectionsPerUser > 0 && g.connections[key] >= g.maxConnectionsPerUser {
		return false
	}
	g.connections[key]++
	return true
}

func (g *Gateway) releaseConnection(key string) {
	g.connectionMu.Lock()
	defer g.connectionMu.Unlock()
	if g.connections[key] <= 1 {
		delete(g.connections, key)
		return
	}
	g.connections[key]--
}

func originPatterns(origins []string) []string {
	result := make([]string, 0, len(origins))
	for _, origin := range origins {
		parsed, err := url.Parse(strings.TrimSpace(origin))
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			result = append(result, parsed.Scheme+"://"+parsed.Host)
		}
	}
	return result
}

type envelope struct {
	Type          string     `json:"type"`
	SchemaVersion int        `json:"schemaVersion"`
	ServerTime    *time.Time `json:"serverTime,omitempty"`
	MatchID       string     `json:"matchId,omitempty"`
	Sequence      uint64     `json:"sequence,omitempty"`
	Version       uint64     `json:"version,omitempty"`
	Snapshot      any        `json:"snapshot,omitempty"`
	Event         any        `json:"event,omitempty"`
	Code          string     `json:"code,omitempty"`
	Message       string     `json:"message,omitempty"`
	NextSequence  uint64     `json:"nextSequence,omitempty"`
}

type publicEvent struct {
	ID               string                `json:"id"`
	Type             matchengine.EventType `json:"type"`
	ResultingVersion uint64                `json:"resultingVersion"`
	Fact             publicEventFact       `json:"fact"`
	OccurredAt       time.Time             `json:"occurredAt"`
}

type publicEventFact struct {
	Team                 *matchengine.TeamSide `json:"team,omitempty"`
	PoolSlotID           string                `json:"poolSlotId,omitempty"`
	BoardPieceID         string                `json:"boardPieceId,omitempty"`
	BoardPieceIDs        []string              `json:"boardPieceIds"`
	Cell                 string                `json:"cell,omitempty"`
	DurationMilliseconds *int64                `json:"durationMilliseconds,omitempty"`
	RequestID            string                `json:"requestId,omitempty"`
	TBBasis              matchengine.TBBasis   `json:"tbBasis,omitempty"`
	PlayerIDs            []string              `json:"playerIds"`
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	claims, _, err := authsession.ClaimsFromRequest(r, g.signer, g.sessions, g.cookie)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	connectionKey := claims.UserID
	if connectionKey == "" {
		connectionKey = strconv.FormatInt(claims.OsuID, 10)
	}
	if !g.acquireConnection(connectionKey) {
		http.Error(w, "too many realtime connections", http.StatusTooManyRequests)
		return
	}
	defer g.releaseConnection(connectionKey)
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: g.origins})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(maxMessageSize)

	ctx := r.Context()
	readCtx, cancelRead := context.WithTimeout(ctx, g.subscribeTimeout)
	defer cancelRead()
	var req envelope
	_, message, err := conn.Read(readCtx)
	if err != nil || json.Unmarshal(message, &req) != nil || req.Type != "subscribe" {
		code := "INVALID_SUBSCRIPTION"
		message := "first message must subscribe to a match"
		if errors.Is(readCtx.Err(), context.DeadlineExceeded) {
			code = "SUBSCRIPTION_TIMEOUT"
			message = "subscription was not received before the deadline"
		}
		g.write(ctx, conn, envelope{Type: "error", Code: code, Message: message})
		return
	}
	if req.SchemaVersion != realtimeSchemaVersion {
		g.write(ctx, conn, envelope{Type: "error", Code: "UNSUPPORTED_SCHEMA_VERSION", Message: "realtime schema version is not supported"})
		return
	}
	ctx = conn.CloseRead(ctx)
	matchID, err := bson.ObjectIDFromHex(req.MatchID)
	if err != nil || matchID.IsZero() {
		g.write(ctx, conn, envelope{Type: "error", Code: "INVALID_MATCH_ID", Message: "matchId is invalid"})
		return
	}
	if g.authorize != nil {
		if err := g.authorize(ctx, claims, matchID); err != nil {
			g.write(ctx, conn, envelope{Type: "error", Code: "FORBIDDEN", Message: "match access denied"})
			return
		}
	}
	state, err := g.snapshots.Load(ctx, matchID)
	if err != nil {
		g.write(ctx, conn, envelope{Type: "error", Code: "MATCH_NOT_FOUND", Message: "match snapshot unavailable"})
		return
	}
	baseline, err := g.events.LatestEventSequenceAtVersion(ctx, matchID, state.Version)
	if err != nil {
		g.write(ctx, conn, envelope{Type: "error", Code: "EVENT_SOURCE_UNAVAILABLE", Message: "event stream temporarily unavailable"})
		return
	}
	if err := g.write(ctx, conn, envelope{Type: "snapshot", MatchID: req.MatchID, Version: state.Version, Sequence: baseline, Snapshot: mapSnapshot(state)}); err != nil {
		return
	}
	next := baseline + 1
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events, err := g.events.ListEventsAfter(ctx, matchID, next-1, 100)
			if err != nil {
				if g.write(ctx, conn, envelope{Type: "error", Code: "EVENT_SOURCE_UNAVAILABLE", Message: "event stream temporarily unavailable"}) != nil {
					return
				}
				continue
			}
			for _, event := range events {
				if event.MatchID != matchID || event.Sequence < next {
					continue
				}
				if event.Sequence != next {
					g.write(ctx, conn, envelope{Type: "resync_required", MatchID: req.MatchID, Code: "EVENT_GAP", Message: "event sequence gap detected", NextSequence: next})
					return
				}
				public, err := mapEvent(event)
				if err != nil {
					g.write(ctx, conn, envelope{Type: "resync_required", MatchID: req.MatchID, Code: "EVENT_PAYLOAD_INVALID", Message: "event payload could not be decoded", NextSequence: next})
					return
				}
				eventState, err := g.events.LoadStateAtVersion(ctx, matchID, event.ResultingVersion)
				if err != nil {
					g.write(ctx, conn, envelope{Type: "resync_required", MatchID: req.MatchID, Code: "EVENT_STATE_UNAVAILABLE", Message: "event state could not be loaded", NextSequence: next})
					return
				}
				if err := g.write(ctx, conn, envelope{Type: "event", MatchID: req.MatchID, Sequence: event.Sequence, Version: event.ResultingVersion, Event: public, Snapshot: mapSnapshot(eventState)}); err != nil {
					return
				}
				next++
			}
		}
	}
}

func mapEvent(event persistence.MatchOutboxDocument) (publicEvent, error) {
	// Outbox payloads are created from the engine's JSON contract and therefore
	// use camelCase keys. Decode through an explicit persistence projection;
	// matchengine.Event intentionally has no BSON concerns.
	payload := struct {
		Team          matchengine.TeamSide `bson:"team"`
		PoolSlotID    string               `bson:"poolSlotId"`
		BoardPieceID  string               `bson:"boardPieceId"`
		BoardPieceIDs []string             `bson:"boardPieceIds"`
		Cell          matchengine.Cell     `bson:"cell"`
		Duration      time.Duration        `bson:"duration"`
		RequestID     string               `bson:"requestId"`
		Basis         matchengine.TBBasis  `bson:"tbBasis"`
		PlayerIDs     []int64              `bson:"playerIds"`
	}{}
	if len(event.Payload) > 0 {
		if err := bson.Unmarshal(event.Payload, &payload); err != nil {
			return publicEvent{}, err
		}
	}
	playerIDs := make([]string, len(payload.PlayerIDs))
	for index, id := range payload.PlayerIDs {
		playerIDs[index] = strconv.FormatInt(id, 10)
	}
	var team *matchengine.TeamSide
	if payload.Team == matchengine.TeamRed || payload.Team == matchengine.TeamBlue {
		value := payload.Team
		team = &value
	}
	var duration *int64
	if payload.Duration != 0 {
		value := payload.Duration.Milliseconds()
		duration = &value
	}
	return publicEvent{
		ID: event.EventID, Type: event.Type, ResultingVersion: event.ResultingVersion,
		Fact: publicEventFact{
			Team: team, PoolSlotID: payload.PoolSlotID, BoardPieceID: payload.BoardPieceID,
			BoardPieceIDs: append([]string{}, payload.BoardPieceIDs...), Cell: string(payload.Cell),
			DurationMilliseconds: duration, RequestID: payload.RequestID, TBBasis: payload.Basis,
			PlayerIDs: playerIDs,
		},
		OccurredAt: event.OccurredAt,
	}, nil
}

func (g *Gateway) write(ctx context.Context, conn *websocket.Conn, value envelope) error {
	value.SchemaVersion = realtimeSchemaVersion
	serverTime := time.Now().UTC()
	value.ServerTime = &serverTime
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, mustJSON(value))
}

func mustJSON(value any) []byte {
	b, _ := json.Marshal(value)
	return b
}

var _ http.Handler = (*Gateway)(nil)
