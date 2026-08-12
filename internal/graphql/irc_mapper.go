package graphql

import (
	"rctHubBackend/internal/irc"
	"rctHubBackend/internal/persistence"
)

func mapIRCObservation(value persistence.IRCObservation) *IRCObservation {
	result := &IRCObservation{
		ID: value.ID, Channel: value.Channel, Sender: value.Sender, Command: value.Command,
		Raw: value.Raw, ObservedAt: value.ObservedAt, ReviewStatus: IRCReviewStatus(value.ReviewStatus),
		ReviewReason: optionalString(value.ReviewReason),
	}
	if value.SuggestedWinningTeam != "" && value.SuggestedBoardPieceID != "" {
		result.SuggestedResult = &IRCSuggestedResult{WinningTeam: TeamSide(value.SuggestedWinningTeam), BoardPieceID: value.SuggestedBoardPieceID}
	}
	return result
}

func mapIRCJob(value irc.Job) *IRCJob {
	result := &IRCJob{
		ID: value.ID, MatchID: value.MatchID, Channel: value.Channel, Kind: value.Kind,
		Payload: string(value.Payload), Status: IRCJobStatus(value.Status), Attempts: value.Attempts,
		AutomaticRetry: value.AutomaticRetry, LastError: optionalString(value.LastError),
	}
	if value.AutomaticRetry && !value.NextTryAt.IsZero() {
		nextTryAt := value.NextTryAt
		result.NextTryAt = &nextTryAt
	}
	if !value.SentAt.IsZero() {
		sentAt := value.SentAt
		result.SentAt = &sentAt
	}
	if !value.AckDeadline.IsZero() {
		deadline := value.AckDeadline
		result.AckDeadline = &deadline
	}
	if !value.AcknowledgedAt.IsZero() {
		acknowledgedAt := value.AcknowledgedAt
		result.AcknowledgedAt = &acknowledgedAt
	}
	return result
}
