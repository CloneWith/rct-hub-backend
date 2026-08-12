package graphql

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/beatmapmetadata"
	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

type metadataBeatmapReader struct {
	beatmap *domain.Beatmap
	err     error
}

func (r metadataBeatmapReader) GetByOsuID(context.Context, int64) (*domain.Beatmap, error) {
	return r.beatmap, r.err
}

func TestMatchPoolSlotMetadataStatus(t *testing.T) {
	id := "123"
	tests := []struct {
		name   string
		id     *string
		reader BeatmapReader
		want   BeatmapMetadataStatus
	}{
		{name: "not configured", want: BeatmapMetadataStatusNotConfigured},
		{name: "ready", id: &id, reader: metadataBeatmapReader{beatmap: &domain.Beatmap{OnlineID: 123}}, want: BeatmapMetadataStatusReady},
		{name: "pending", id: &id, reader: metadataBeatmapReader{}, want: BeatmapMetadataStatusPending},
		{name: "upstream failed", id: &id, reader: metadataBeatmapReader{err: errors.New("osu unavailable")}, want: BeatmapMetadataStatusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &Resolver{beatmaps: tt.reader}
			got, err := (&matchPoolSlotMetadataResolver{resolver}).MetadataStatus(context.Background(), &MatchPoolSlotMetadata{BeatmapID: tt.id})
			if err != nil || got != tt.want {
				t.Fatalf("status=%s err=%v, want %s", got, err, tt.want)
			}
		})
	}
}

type metadataManagerStub struct {
	record  beatmapmetadata.Record
	beatmap *domain.Beatmap
	retried int64
	err     error
	states  int
}

func (s *metadataManagerStub) State(context.Context, int64) (beatmapmetadata.Record, error) {
	s.states++
	return s.record, s.err
}

func TestMatchPoolMetadataUsesOneDurableStatePerRequest(t *testing.T) {
	id := "123"
	metadata := &metadataManagerStub{record: beatmapmetadata.Record{BeatmapID: 123, Status: beatmapmetadata.StatusPending, Attempts: 1}}
	resolver := &matchPoolSlotMetadataResolver{&Resolver{metadata: metadata}}
	object := &MatchPoolSlotMetadata{BeatmapID: &id}
	ctx := withMetadataRequestCache(context.Background())
	if _, err := resolver.MetadataStatus(ctx, object); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.MetadataAttempts(ctx, object); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.MetadataNextRetryAt(ctx, object); err != nil {
		t.Fatal(err)
	}
	if metadata.states != 1 {
		t.Fatalf("metadata state reads=%d, want one consistent request snapshot", metadata.states)
	}
}
func (s *metadataManagerStub) Beatmap(context.Context, int64) (*domain.Beatmap, error) {
	return s.beatmap, s.err
}
func (s *metadataManagerStub) Retry(_ context.Context, id int64) error {
	s.retried = id
	return s.err
}

func TestMatchPoolMetadataExposesDurableFailureDetails(t *testing.T) {
	id := "123"
	next := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	metadata := &metadataManagerStub{record: beatmapmetadata.Record{BeatmapID: 123, Status: beatmapmetadata.StatusFailed, Attempts: 2, NextTryAt: next, LastError: "osu unavailable"}}
	resolver := &matchPoolSlotMetadataResolver{&Resolver{metadata: metadata}}
	object := &MatchPoolSlotMetadata{BeatmapID: &id}
	status, _ := resolver.MetadataStatus(context.Background(), object)
	attempts, _ := resolver.MetadataAttempts(context.Background(), object)
	retryAt, _ := resolver.MetadataNextRetryAt(context.Background(), object)
	lastError, _ := resolver.MetadataLastError(context.Background(), object)
	if status != BeatmapMetadataStatusFailed || attempts != 2 || retryAt == nil || !retryAt.Equal(next) || lastError == nil || *lastError != "osu unavailable" {
		t.Fatalf("status=%s attempts=%d retryAt=%v error=%v", status, attempts, retryAt, lastError)
	}
}

func TestRetryBeatmapMetadataRequiresLinkedMatchBeatmap(t *testing.T) {
	matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
	beatmapID := int64(123)
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee}}
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeMatch, OwnerID: 100, MatchID: &matchID}
	metadata := &metadataManagerStub{}
	resolver := NewResolver(nil).
		WithFormalMatchReader(ircFormalReader{&service.FormalMatch{ID: matchID, RoomID: roomID, Pool: map[string]*int64{"NM1": &beatmapID}}}).
		WithPrivateReaders(ircUserReader{user}, ircRoomReader{room}).
		WithBeatmapMetadata(metadata)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	ok, err := resolver.Mutation().RetryBeatmapMetadata(ctx, RetryBeatmapMetadataInput{MatchID: matchID.Hex(), BeatmapID: "123"})
	if err != nil || !ok || metadata.retried != 123 {
		t.Fatalf("ok=%v retried=%d err=%v", ok, metadata.retried, err)
	}
	if _, err := resolver.Mutation().RetryBeatmapMetadata(ctx, RetryBeatmapMetadataInput{MatchID: matchID.Hex(), BeatmapID: "999"}); err == nil {
		t.Fatal("unlinked beatmap was retryable")
	}
}

func TestIRCResultEvidenceMustMatchConfirmedCommand(t *testing.T) {
	if !ircResultMatches(":!result RED piece-1", TeamSideRed, "piece-1") {
		t.Fatal("matching IRC result was rejected")
	}
	if ircResultMatches(":!result BLUE piece-1", TeamSideRed, "piece-1") || ircResultMatches(":hello", TeamSideRed, "piece-1") {
		t.Fatal("unrelated IRC evidence was accepted")
	}
}
