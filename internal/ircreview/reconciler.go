// Package ircreview keeps referee review evidence consistent with committed
// authoritative match commands.
package ircreview

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

type ObservationStore interface {
	ListConfirming(context.Context, int64) ([]persistence.IRCObservation, error)
	FinalizeConfirmation(context.Context, string, string) error
	ReleaseConfirmation(context.Context, string, string) error
}

type ReceiptStore interface {
	LoadConfirmationReceipt(context.Context, bson.ObjectID, string) (*persistence.ConfirmationReceipt, error)
}

type Reconciler struct {
	observations ObservationStore
	receipts     ReceiptStore
	staleAfter   time.Duration
	now          func() time.Time
}

func New(observations ObservationStore, receipts ReceiptStore, staleAfter time.Duration) *Reconciler {
	if staleAfter <= 0 {
		staleAfter = time.Minute
	}
	return &Reconciler{observations: observations, receipts: receipts, staleAfter: staleAfter, now: time.Now}
}

func (r *Reconciler) RunOnce(ctx context.Context) error {
	if r == nil || r.observations == nil || r.receipts == nil {
		return fmt.Errorf("IRC review reconciler is not configured")
	}
	items, err := r.observations.ListConfirming(ctx, 100)
	if err != nil {
		return fmt.Errorf("list confirming IRC observations: %w", err)
	}
	var firstErr error
	for _, item := range items {
		if item.MatchID == nil || item.MatchID.IsZero() || item.ConfirmationCommandID == "" || item.ConfirmationPieceID == "" ||
			(item.ConfirmationWinner != matchengine.TeamRed && item.ConfirmationWinner != matchengine.TeamBlue) || item.ReviewStartedAt == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("IRC observation %q has an incomplete confirmation claim", item.ID)
			}
			continue
		}
		receipt, receiptErr := r.receipts.LoadConfirmationReceipt(ctx, *item.MatchID, item.ConfirmationCommandID)
		if receiptErr != nil {
			if firstErr == nil {
				firstErr = receiptErr
			}
			continue
		}
		if receipt != nil && receipt.CommandType == "CONFIRM_BEATMAP_RESULT" && receipt.BoardPieceID == item.ConfirmationPieceID && receipt.WinningTeam == item.ConfirmationWinner {
			err = r.observations.FinalizeConfirmation(ctx, item.ID, item.ConfirmationCommandID)
		} else if !item.ReviewStartedAt.Add(r.staleAfter).After(r.now().UTC()) {
			err = r.observations.ReleaseConfirmation(ctx, item.ID, item.ConfirmationCommandID)
		} else {
			continue
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
