package ircreview

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

type reviewStoreStub struct {
	items     []persistence.IRCObservation
	finalized []string
	released  []string
}

func (s *reviewStoreStub) ListConfirming(context.Context, int64) ([]persistence.IRCObservation, error) {
	return s.items, nil
}
func (s *reviewStoreStub) FinalizeConfirmation(_ context.Context, id, _ string) error {
	s.finalized = append(s.finalized, id)
	return nil
}
func (s *reviewStoreStub) ReleaseConfirmation(_ context.Context, id, _ string) error {
	s.released = append(s.released, id)
	return nil
}

type receiptStoreStub struct {
	receipts map[string]*persistence.ConfirmationReceipt
	err      error
}

func (s receiptStoreStub) LoadConfirmationReceipt(_ context.Context, _ bson.ObjectID, commandID string) (*persistence.ConfirmationReceipt, error) {
	return s.receipts[commandID], s.err
}

func TestReconcilerFinalizesCommittedAndReleasesStaleClaims(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	matchID := bson.NewObjectID()
	old := now.Add(-2 * time.Minute)
	recent := now.Add(-time.Second)
	store := &reviewStoreStub{items: []persistence.IRCObservation{
		{ID: "committed", MatchID: &matchID, ConfirmationCommandID: "done", ConfirmationPieceID: "piece-1", ConfirmationWinner: matchengine.TeamRed, ReviewStartedAt: &recent},
		{ID: "abandoned", MatchID: &matchID, ConfirmationCommandID: "missing", ConfirmationPieceID: "piece-2", ConfirmationWinner: matchengine.TeamBlue, ReviewStartedAt: &old},
		{ID: "active", MatchID: &matchID, ConfirmationCommandID: "active", ConfirmationPieceID: "piece-3", ConfirmationWinner: matchengine.TeamRed, ReviewStartedAt: &recent},
	}}
	reconciler := New(store, receiptStoreStub{receipts: map[string]*persistence.ConfirmationReceipt{
		"done": {CommandType: "CONFIRM_BEATMAP_RESULT", BoardPieceID: "piece-1", WinningTeam: matchengine.TeamRed},
	}}, time.Minute)
	reconciler.now = func() time.Time { return now }
	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.finalized) != 1 || store.finalized[0] != "committed" {
		t.Fatalf("finalized=%v", store.finalized)
	}
	if len(store.released) != 1 || store.released[0] != "abandoned" {
		t.Fatalf("released=%v", store.released)
	}
}

func TestReconcilerDoesNotFinalizeReceiptForDifferentResult(t *testing.T) {
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Minute)
	matchID := bson.NewObjectID()
	store := &reviewStoreStub{items: []persistence.IRCObservation{{
		ID: "evidence", MatchID: &matchID, ConfirmationCommandID: "reused", ConfirmationPieceID: "piece-1",
		ConfirmationWinner: matchengine.TeamRed, ReviewStartedAt: &old,
	}}}
	reconciler := New(store, receiptStoreStub{receipts: map[string]*persistence.ConfirmationReceipt{
		"reused": {CommandType: "CONFIRM_BEATMAP_RESULT", BoardPieceID: "other-piece", WinningTeam: matchengine.TeamBlue},
	}}, time.Minute)
	reconciler.now = func() time.Time { return now }
	if err := reconciler.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.finalized) != 0 || len(store.released) != 1 || store.released[0] != "evidence" {
		t.Fatalf("finalized=%v released=%v", store.finalized, store.released)
	}
}

func TestReconcilerPreservesClaimsWhenReceiptLookupFails(t *testing.T) {
	now := time.Now().UTC().Add(-time.Hour)
	matchID := bson.NewObjectID()
	store := &reviewStoreStub{items: []persistence.IRCObservation{{ID: "evidence", MatchID: &matchID, ConfirmationCommandID: "command", ConfirmationPieceID: "piece-1", ConfirmationWinner: matchengine.TeamRed, ReviewStartedAt: &now}}}
	reconciler := New(store, receiptStoreStub{err: errors.New("mongo unavailable")}, time.Minute)
	if err := reconciler.RunOnce(context.Background()); err == nil {
		t.Fatal("receipt failure was hidden")
	}
	if len(store.finalized)+len(store.released) != 0 {
		t.Fatal("claim changed without durable receipt evidence")
	}
}
