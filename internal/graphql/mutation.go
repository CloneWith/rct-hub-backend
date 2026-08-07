package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
)

func (r *mutationResolver) executeCommand(ctx context.Context, meta *CommandMeta, command matchengine.Command) *MatchCommandResult {
	commandID := ""
	if meta != nil {
		commandID = meta.CommandID
	}
	failure := func(code, message string, currentVersion *int) *MatchCommandResult {
		return &MatchCommandResult{
			Success: false, CommandID: commandID, Events: []*MatchCommandEvent{},
			CurrentVersion: currentVersion,
			Error:          &MatchError{Code: code, Message: message, CurrentVersion: currentVersion},
		}
	}
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return failure(string(matchcommand.CodeAuthRequired), "authentication required", nil)
	}
	if meta == nil || meta.ExpectedVersion < 0 {
		return failure(string(matchcommand.CodeInvalidRequest), "valid command metadata is required", nil)
	}
	matchID, err := bson.ObjectIDFromHex(meta.MatchID)
	if err != nil {
		return failure(string(matchcommand.CodeInvalidRequest), "matchId must be a valid ObjectID", nil)
	}
	if r.commands == nil {
		return failure(string(matchcommand.CodeInternalError), "match command service is unavailable", nil)
	}
	result, err := r.commands.Execute(ctx, matchcommand.Request{
		MatchID: matchID, ExpectedVersion: uint64(meta.ExpectedVersion), CommandID: meta.CommandID,
		CallerOsuID: claims.OsuID, Command: command,
	})
	if err != nil {
		commandErr := matchcommand.ErrorOf(err)
		if commandErr == nil {
			return failure(string(matchcommand.CodeInternalError), "match command failed", nil)
		}
		var currentVersion *int
		if commandErr.CurrentVersion != nil {
			value := int(*commandErr.CurrentVersion)
			currentVersion = &value
		}
		return failure(string(commandErr.Code), commandErr.Message, currentVersion)
	}

	previousVersion, resultingVersion := int(result.PreviousVersion), int(result.ResultingVersion)
	disposition := string(result.Disposition)
	state := jsonMap(result.State)
	events := make([]*MatchCommandEvent, 0, len(result.Events))
	for _, event := range result.Events {
		events = append(events, &MatchCommandEvent{
			EventID: event.EventID, Sequence: int(event.Sequence), ResultingVersion: int(event.ResultingVersion),
			Type: string(event.Type), OccurredAt: event.OccurredAt, Payload: jsonMap(event.Payload),
		})
	}
	return &MatchCommandResult{
		Success: true, CommandID: result.CommandID, Disposition: &disposition,
		PreviousVersion: &previousVersion, ResultingVersion: &resultingVersion,
		State: state, Events: events,
	}
}

func jsonMap(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(encoded, &mapped); err != nil {
		return nil
	}
	return mapped
}

func engineTeam(side TeamSide) matchengine.TeamSide {
	return matchengine.TeamSide(side.String())
}

func engineCell(position *PositionInput) (matchengine.Cell, error) {
	if position == nil || position.Row < 0 || position.Row > 3 || position.Col < 0 || position.Col > 3 {
		return "", fmt.Errorf("position must use zero-based row and col values from 0 through 3")
	}
	return matchengine.Cell(fmt.Sprintf("%c%d", 'A'+rune(position.Col), position.Row+1)), nil
}

func pieceID(commandID string) string { return "piece-" + commandID }

func invalidCommandInput(meta *CommandMeta, err error) *MatchCommandResult {
	commandID := ""
	if meta != nil {
		commandID = meta.CommandID
	}
	return &MatchCommandResult{
		Success: false, CommandID: commandID, Events: []*MatchCommandEvent{},
		Error: &MatchError{Code: string(matchcommand.CodeInvalidRequest), Message: err.Error()},
	}
}

func int64IDs(ids []int) []int64 {
	converted := make([]int64, len(ids))
	for index, id := range ids {
		converted[index] = int64(id)
	}
	return converted
}

func milliseconds(value int) time.Duration { return time.Duration(value) * time.Millisecond }
