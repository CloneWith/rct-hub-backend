package matchfixture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
)

type mockReceipt struct {
	payload string
	result  matchcommand.Result
}

// Executor provides a stateful local mutation loop while preserving the
// production command contract: UUID idempotency, optimistic versions, Engine
// validation, typed events, and replayed responses.
type Executor struct {
	mu       sync.Mutex
	reader   *Reader
	receipts map[string]mockReceipt
}

func NewExecutor(reader *Reader) *Executor {
	return &Executor{reader: reader, receipts: make(map[string]mockReceipt)}
}

func (e *Executor) Execute(_ context.Context, request matchcommand.Request) (matchcommand.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e != nil && e.reader != nil {
		e.reader.mu.Lock()
		defer e.reader.mu.Unlock()
	}
	if e == nil || e.reader == nil || request.Command == nil || request.MatchID.IsZero() {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInvalidRequest, "match and command are required", nil)
	}
	if parsed, err := uuid.Parse(request.CommandID); err != nil || parsed == uuid.Nil {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInvalidRequest, "commandId must be a non-zero UUID", err)
	}
	payloadBytes, err := json.Marshal(request.Command)
	if err != nil {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInvalidRequest, "command cannot be encoded", err)
	}
	key := request.MatchID.Hex() + ":" + request.CommandID
	payload := fmt.Sprintf("%d:%T:%s", request.ExpectedVersion, request.Command, payloadBytes)
	if receipt, exists := e.receipts[key]; exists {
		if receipt.payload != payload {
			return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeDuplicateCommandMismatch, "commandId was already used for a different request", nil)
		}
		result := cloneMockResult(receipt.result)
		result.Disposition = matchcommand.DispositionReplayed
		return result, nil
	}
	formal, exists := e.reader.byID[request.MatchID]
	if !exists {
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeResourceNotFound, "fixture match was not found", nil)
	}
	if formal.State.Version != request.ExpectedVersion {
		return matchcommand.Result{}, matchcommand.VersionConflict(request.ExpectedVersion, formal.State.Version)
	}
	actor, err := fixtureActor(formal.State, request.Command)
	if err != nil {
		return matchcommand.Result{}, err
	}
	now := fixtureTime.Add(time.Duration(100+formal.State.Version) * time.Second)
	if formal.State.Timer.Duration > 0 {
		now = formal.State.Timer.StartedAt.Add(time.Second)
	}
	transition, err := matchengine.Execute(formal.State, actor, request.Command, now)
	if err != nil {
		var ruleErr *matchengine.RuleError
		if errors.As(err, &ruleErr) {
			return matchcommand.Result{}, matchcommand.NewError(matchcommand.ErrorCode(ruleErr.Code), ruleErr.Message, ruleErr)
		}
		return matchcommand.Result{}, matchcommand.NewError(matchcommand.CodeInternalError, "execute fixture command", err)
	}
	events := make([]matchcommand.CommittedEvent, len(transition.Events))
	for index, event := range transition.Events {
		team := actor.Team
		events[index] = matchcommand.CommittedEvent{
			EventID:  fmt.Sprintf("fixture-%s-%d-%d", request.MatchID.Hex(), transition.State.Version, index+1),
			Sequence: formal.State.Version*100 + uint64(index) + 1, ResultingVersion: transition.State.Version,
			Type: event.Type, OccurredAt: now,
			Actor:   matchcommand.EventActor{OsuID: request.CallerOsuID, Capability: actor.Capability, Team: team},
			Payload: event,
		}
	}
	formal.State = transition.State.Clone()
	e.reader.byID[formal.ID] = formal
	e.reader.byCode[formal.Code] = formal
	for index := range e.reader.items {
		if e.reader.items[index].ID == formal.ID {
			e.reader.items[index] = formal
			break
		}
	}
	result := matchcommand.Result{
		CommandID: request.CommandID, Disposition: matchcommand.DispositionApplied,
		PreviousVersion: request.ExpectedVersion, ResultingVersion: transition.State.Version,
		State: transition.State.Clone(), Events: events,
	}
	e.receipts[key] = mockReceipt{payload: payload, result: cloneMockResult(result)}
	return result, nil
}

func fixtureActor(state matchengine.State, command matchengine.Command) (matchengine.Actor, error) {
	switch command.(type) {
	case matchengine.BanPoolSlot, matchengine.PlacePiece, matchengine.PlaceShiro, matchengine.RobPiece:
		return matchengine.StrategistActor(state.ActiveTeam), nil
	case matchengine.RequestTB:
		return matchengine.CaptainActor(matchengine.TeamRed), nil
	case matchengine.RespondTBRequest:
		if state.PendingTBRequest == nil {
			return matchengine.Actor{}, matchcommand.NewError(matchcommand.CodeActionNotAllowed, "no TB request is pending", nil)
		}
		team := matchengine.TeamRed
		if state.PendingTBRequest.RequestedBy == matchengine.TeamRed {
			team = matchengine.TeamBlue
		}
		return matchengine.CaptainActor(team), nil
	default:
		return matchengine.RefereeActor(), nil
	}
}

func cloneMockResult(result matchcommand.Result) matchcommand.Result {
	encoded, _ := json.Marshal(result)
	var clone matchcommand.Result
	_ = json.Unmarshal(encoded, &clone)
	return clone
}
