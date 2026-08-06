package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/jwtutil"
)

const graphqlCommandID = "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409"

type commandExecutorStub struct {
	request matchcommand.Request
	result  matchcommand.Result
	err     error
}

func (s *commandExecutorStub) Execute(_ context.Context, request matchcommand.Request) (matchcommand.Result, error) {
	s.request = request
	return s.result, s.err
}

func TestFormalMutationSchemaMatchesEngineCommands(t *testing.T) {
	server := NewHandler(NewResolver(nil))
	response := graphQLRequest(t, server, context.Background(), `{ __type(name: "Mutation") { fields { name } } }`)
	var body struct {
		Data struct {
			Type struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"__type"`
		} `json:"data"`
	}
	decodeGraphQL(t, response, &body)
	got := make([]string, 0, len(body.Data.Type.Fields))
	for _, field := range body.Data.Type.Fields {
		got = append(got, field.Name)
	}
	sort.Strings(got)
	want := []string{
		"abortMatch", "banPoolSlot", "calibrateTimer", "confirmBeatmapResult", "confirmTbResult",
		"grantAdditionalTime", "pauseTimer", "placePiece", "placeShiro", "recordSurrender",
		"refereeBanPoolSlot", "refereePlacePiece", "refereePlaceShiro", "refereeRequestTb",
		"refereeRespondTbRequest", "refereeRobPiece", "requestTb", "respondTbRequest", "resumeMatch",
		"resumeTimer", "robPiece", "skipCurrentAction", "startMatch", "startTb", "suspendMatch",
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("mutation fields = %v, want %v", got, want)
	}
	for _, removed := range []string{"advanceTurn", "beginRobbery", "cancelRobbery", "completeRobbery", "grantWinPermission", "unbanPoolSlot", "undoAction"} {
		for _, name := range got {
			if name == removed {
				t.Fatalf("legacy mutation %q remains public", removed)
			}
		}
	}
}

func TestMutationReturnsStableAuthError(t *testing.T) {
	server := NewHandler(NewResolver(nil, &commandExecutorStub{}))
	query := `mutation { startMatch(input: {matchId: "507f1f77bcf86cd799439011", expectedVersion: 0, commandId: "` + graphqlCommandID + `"}) { success commandId events { eventId } error { code message } } }`
	response := graphQLRequest(t, server, context.Background(), query)
	if !strings.Contains(response.Body.String(), `"code":"AUTH_REQUIRED"`) {
		t.Fatalf("response = %s", response.Body.String())
	}
}

func TestPlacePieceMapsTransportWithoutLegacyService(t *testing.T) {
	matchID := bson.NewObjectID()
	stub := &commandExecutorStub{result: appliedGraphQLResult(matchID)}
	resolver := NewResolver(nil, stub)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1234})
	result, err := resolver.Mutation().PlacePiece(ctx, PlacePieceInput{
		Meta:       &CommandMeta{MatchID: matchID.Hex(), ExpectedVersion: 7, CommandID: graphqlCommandID},
		PoolSlotID: "FM-2", Position: &PositionInput{Row: 2, Col: 3},
	})
	if err != nil || !result.Success {
		t.Fatalf("PlacePiece result=%+v err=%v", result, err)
	}
	command, ok := stub.request.Command.(matchengine.PlacePiece)
	if !ok {
		t.Fatalf("command = %T", stub.request.Command)
	}
	if stub.request.ExpectedVersion != 7 || stub.request.CallerOsuID != 1234 || command.Cell != "D3" || command.PieceID != "piece-"+graphqlCommandID || command.PoolSlotID != "FM-2" {
		t.Fatalf("mapped request=%+v command=%+v", stub.request, command)
	}
	if result.Disposition == nil || *result.Disposition != "APPLIED" || len(result.Events) != 1 || result.Events[0].Sequence != 41 {
		t.Fatalf("result contract = %+v", result)
	}
}

func TestMutationExposesVersionConflict(t *testing.T) {
	current := uint64(12)
	stub := &commandExecutorStub{err: &matchcommand.Error{Code: matchcommand.CodeMatchVersionConflict, Message: "stale match version", CurrentVersion: &current}}
	resolver := NewResolver(nil, stub)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1234})
	result, err := resolver.Mutation().StartMatch(ctx, CommandMeta{MatchID: bson.NewObjectID().Hex(), ExpectedVersion: 4, CommandID: graphqlCommandID})
	if err != nil || result.Success || result.Error == nil || result.Error.Code != "MATCH_VERSION_CONFLICT" || result.CurrentVersion == nil || *result.CurrentVersion != 12 {
		t.Fatalf("version conflict result=%+v err=%v", result, err)
	}
}

func TestMutationPreservesEngineRuleError(t *testing.T) {
	stub := &commandExecutorStub{err: matchcommand.NewError(matchcommand.ErrorCode(matchengine.CodeNotActiveTeam), "the other team is active", nil)}
	resolver := NewResolver(nil, stub)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1234})
	result, err := resolver.Mutation().BanPoolSlot(ctx, BanPoolSlotInput{Meta: &CommandMeta{MatchID: bson.NewObjectID().Hex(), CommandID: graphqlCommandID}, PoolSlotID: "NM-1"})
	if err != nil || result.Error == nil || result.Error.Code != "NOT_ACTIVE_TEAM" || result.Error.Message != "the other team is active" {
		t.Fatalf("rule error result=%+v err=%v", result, err)
	}
}

func TestRobberyPreservesSacrificeGroups(t *testing.T) {
	matchID := bson.NewObjectID()
	stub := &commandExecutorStub{result: appliedGraphQLResult(matchID)}
	resolver := NewResolver(nil, stub)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1234})
	sets := [][]string{{"a", "b", "c"}, {"d", "e"}}
	_, _ = resolver.Mutation().RobPiece(ctx, RobPieceInput{Meta: &CommandMeta{MatchID: matchID.Hex(), CommandID: graphqlCommandID}, TargetPieceID: "target", SacrificeSets: sets})
	command := stub.request.Command.(matchengine.RobPiece)
	if len(command.SacrificeSets) != 2 || len(command.SacrificeSets[0]) != 3 || command.SacrificeSets[1][1] != "e" {
		t.Fatalf("sacrifice sets = %v", command.SacrificeSets)
	}
}

func TestPositionAndDurationValidationHappenBeforeExecution(t *testing.T) {
	stub := &commandExecutorStub{}
	resolver := NewResolver(nil, stub)
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1234})
	meta := &CommandMeta{MatchID: bson.NewObjectID().Hex(), CommandID: graphqlCommandID}
	positionResult, _ := resolver.Mutation().PlaceShiro(ctx, PlaceShiroInput{Meta: meta, Position: &PositionInput{Row: 4, Col: 0}})
	durationResult, _ := resolver.Mutation().CalibrateTimer(ctx, CalibrateTimerInput{Meta: meta, RemainingMilliseconds: -1, Reason: "clock correction"})
	if positionResult.Error == nil || durationResult.Error == nil || stub.request.Command != nil {
		t.Fatalf("position=%+v duration=%+v request=%+v", positionResult, durationResult, stub.request)
	}
}

func appliedGraphQLResult(matchID bson.ObjectID) matchcommand.Result {
	now := time.Date(2026, time.August, 7, 1, 2, 3, 0, time.UTC)
	return matchcommand.Result{
		CommandID: graphqlCommandID, Disposition: matchcommand.DispositionApplied,
		PreviousVersion: 7, ResultingVersion: 8,
		Events: []matchcommand.CommittedEvent{{EventID: "event-1", Sequence: 41, ResultingVersion: 8, Type: matchengine.EventPiecePlaced, OccurredAt: now, Payload: matchengine.Event{Type: matchengine.EventPiecePlaced, Cell: "D3"}}},
	}
}

func graphQLRequest(t *testing.T, server http.Handler, ctx context.Context, query string) *httptest.ResponseRecorder {
	t.Helper()
	encoded, _ := json.Marshal(map[string]string{"query": query})
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(encoded))).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func decodeGraphQL(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
