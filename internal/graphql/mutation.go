package graphql

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	failure := func(code, message string, currentVersion *string) *MatchCommandResult {
		return &MatchCommandResult{
			Success: false, CommandID: commandID, Events: []*MatchCommandEvent{},
			CurrentVersion: currentVersion,
			Error:          &MatchError{Code: MatchErrorCode(code), Message: message, CurrentVersion: currentVersion},
		}
	}
	claims, ok := ClaimsFromCtx(ctx)
	if !ok || claims == nil {
		return failure(string(matchcommand.CodeAuthRequired), "authentication required", nil)
	}
	if meta == nil {
		return failure(string(matchcommand.CodeInvalidRequest), "valid command metadata is required", nil)
	}
	expectedVersion, err := strconv.ParseUint(meta.ExpectedVersion, 10, 64)
	if err != nil {
		return failure(string(matchcommand.CodeInvalidRequest), "expectedVersion must be an unsigned decimal string", nil)
	}
	matchID, err := bson.ObjectIDFromHex(meta.MatchID)
	if err != nil {
		return failure(string(matchcommand.CodeInvalidRequest), "matchId must be a valid ObjectID", nil)
	}
	if r.commands == nil {
		return failure(string(matchcommand.CodeInternalError), "match command service is unavailable", nil)
	}
	result, err := r.commands.Execute(ctx, matchcommand.Request{
		MatchID: matchID, ExpectedVersion: expectedVersion, CommandID: meta.CommandID,
		CallerOsuID: claims.OsuID, Command: command,
	})
	if err != nil {
		commandErr := matchcommand.ErrorOf(err)
		if commandErr == nil {
			return failure(string(matchcommand.CodeInternalError), "match command failed", nil)
		}
		var currentVersion *string
		if commandErr.CurrentVersion != nil {
			value := strconv.FormatUint(*commandErr.CurrentVersion, 10)
			currentVersion = &value
		}
		return failure(string(commandErr.Code), commandErr.Message, currentVersion)
	}

	previousVersion := strconv.FormatUint(result.PreviousVersion, 10)
	resultingVersion := strconv.FormatUint(result.ResultingVersion, 10)
	disposition := MatchCommandDisposition(result.Disposition)
	events := make([]*MatchCommandEvent, 0, len(result.Events))
	for _, event := range result.Events {
		actorTeam := gqlTeamPtrValue(event.Actor.Team)
		events = append(events, &MatchCommandEvent{
			EventID: event.EventID, SchemaVersion: matchcommand.EventSchemaVersion, AggregateID: matchID.Hex(), AggregateType: "MATCH",
			Sequence: strconv.FormatUint(event.Sequence, 10), ResultingVersion: strconv.FormatUint(event.ResultingVersion, 10),
			Type: MatchEventType(event.Type), OccurredAt: event.OccurredAt,
			Actor: &MatchCommandActor{
				OsuID: strconv.FormatInt(event.Actor.OsuID, 10), Capability: MatchActorCapability(event.Actor.Capability), Team: actorTeam,
				AdminOverride: event.Actor.AdminOverride, RefereeOverride: event.Actor.RefereeOverride,
			},
			Fact: mapEventFact(event.Payload),
		})
	}
	return &MatchCommandResult{
		Success: true, CommandID: result.CommandID, Disposition: &disposition,
		PreviousVersion: &previousVersion, ResultingVersion: &resultingVersion,
		Snapshot: mapMatchSnapshot(result.State), Events: events,
	}
}

func mapEventFact(event matchengine.Event) *MatchEventFact {
	playerIDs := make([]string, len(event.PlayerIDs))
	for index, id := range event.PlayerIDs {
		playerIDs[index] = strconv.FormatInt(id, 10)
	}
	return &MatchEventFact{
		Team: gqlTeamPtr(event.Team), PoolSlotID: optionalString(event.PoolSlotID),
		BoardPieceID: optionalString(event.BoardPieceID), BoardPieceIDs: append([]string(nil), event.BoardPieceIDs...),
		Cell: optionalString(string(event.Cell)), DurationMilliseconds: optionalDurationMilliseconds(event.Duration),
		Reason: optionalString(event.Reason), RequestID: optionalString(event.RequestID),
		TbBasis: gqlTBBasis(event.Basis), PlayerIDs: playerIDs,
	}
}

func optionalDurationMilliseconds(value time.Duration) *int {
	if value == 0 {
		return nil
	}
	milliseconds := int(value / time.Millisecond)
	return &milliseconds
}

func gqlTBBasis(value matchengine.TBBasis) *TBBasis {
	if value == "" {
		return nil
	}
	result := TBBasis(value)
	return &result
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
		Error: &MatchError{Code: MatchErrorCode(matchcommand.CodeInvalidRequest), Message: err.Error()},
	}
}

func int64IDs(ids []string) ([]int64, error) {
	converted := make([]int64, len(ids))
	for index, id := range ids {
		value, err := strconv.ParseInt(id, 10, 64)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("player IDs must be positive decimal strings")
		}
		converted[index] = value
	}
	return converted, nil
}

func milliseconds(value int) time.Duration { return time.Duration(value) * time.Millisecond }

func ircResultMatches(command string, team TeamSide, piece string) bool {
	parts := strings.Fields(strings.TrimPrefix(strings.TrimSpace(command), ":"))
	return len(parts) == 3 && strings.EqualFold(parts[0], "!result") && strings.EqualFold(parts[1], string(team)) && parts[2] == piece
}
