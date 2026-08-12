package realtime

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/matchengine"
	"rctHubBackend/internal/persistence"
)

func TestRealtimeV1ContractCoversRuntimeMessages(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate realtime contract test")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "contracts", "realtime-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract map[string]any
	if err := json.Unmarshal(content, &contract); err != nil {
		t.Fatalf("realtime contract is not valid JSON: %v", err)
	}
	definitions, ok := contract["$defs"].(map[string]any)
	if !ok {
		t.Fatal("realtime contract has no definitions")
	}

	matchID := bson.NewObjectID().Hex()
	state := matchengine.State{
		Version: 1, Lifecycle: matchengine.LifecycleRunning, Phase: matchengine.PhasePick,
		FirstBan: matchengine.TeamRed, FirstPick: matchengine.TeamBlue,
		Board: matchengine.NewBoard(), PoolSlots: map[string]matchengine.PoolSlot{},
		RobberyUsed: map[matchengine.TeamSide]bool{}, TeamPauseUsed: map[matchengine.TeamSide]bool{},
		Rosters: map[matchengine.TeamSide]matchengine.Roster{},
	}
	payload, err := bson.Marshal(matchengine.Event{Type: matchengine.EventTimerPaused})
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapEvent(persistence.MatchOutboxDocument{
		EventID: "event-1", Type: matchengine.EventTimerPaused, ResultingVersion: 1,
		Payload: payload, OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]any{
		"subscribe":       envelope{Type: "subscribe", SchemaVersion: realtimeSchemaVersion, MatchID: matchID},
		"snapshotMessage": serverEnvelope(envelope{Type: "snapshot", MatchID: matchID, Version: 1, Snapshot: mapSnapshot(state)}),
		"eventMessage":    serverEnvelope(envelope{Type: "event", MatchID: matchID, Sequence: 1, Version: 1, Event: event, Snapshot: mapSnapshot(state)}),
		"errorMessage":    serverEnvelope(envelope{Type: "error", Code: "FORBIDDEN", Message: "match access denied"}),
		"resyncMessage":   serverEnvelope(envelope{Type: "resync_required", MatchID: matchID, Code: "EVENT_GAP", Message: "event sequence gap detected", NextSequence: 1}),
	}
	for name, message := range messages {
		t.Run(name, func(t *testing.T) {
			encoded := mustJSON(message)
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			definition, ok := definitions[name].(map[string]any)
			if !ok {
				t.Fatalf("contract definition %q is missing", name)
			}
			assertMatchesDefinition(t, definitions, definition, value, name)
		})
	}

	red := matchengine.TeamRed
	forceMod := matchengine.ForceModNM
	now := time.Now().UTC()
	remaining := int64(45000)
	nestedValues := map[string]any{
		"eventFact": publicEventFact{
			Team: &red, PoolSlotID: "FM1", BoardPieceID: "piece-1",
			BoardPieceIDs: []string{"piece-1", "piece-2"}, Cell: "A1",
			DurationMilliseconds: &remaining, RequestID: "tb-1",
			TBBasis: matchengine.TBBasisCaptainAgreement, PlayerIDs: []string{"1", "2"},
		},
		"poolSlot":  poolSlot{ID: "FM1", Mod: matchengine.ModFM, State: matchengine.PoolSlotSelected},
		"board":     mapBoard(matchengine.NewBoard()),
		"boardCell": boardCell{Cell: "A1", Row: 0, Col: 0, Zone: matchengine.ZoneDT},
		"boardPiece": boardPiece{
			ID: "piece-1", SourcePoolSlotID: "FM1", Mod: matchengine.ModFM,
			ForceMod: &forceMod, SelectedBy: matchengine.TeamRed, Owner: &red,
			Outcome: matchengine.OutcomeWon,
		},
		"timer":             timer{StartedAt: &now, DurationMilliseconds: 90000, Paused: true, RemainingAtPauseMilliseconds: &remaining},
		"teamCounts":        teamCounts{Red: 2, Blue: 1},
		"teamFlags":         teamFlags{Red: true, Blue: false},
		"roster":            roster{LeaderID: "1", PlayerIDs: []string{"1", "2"}},
		"rosters":           rosters{Red: roster{LeaderID: "1", PlayerIDs: []string{}}, Blue: roster{LeaderID: "2", PlayerIDs: []string{}}},
		"tbRequest":         tbRequest{ID: "tb-1", RequestedBy: matchengine.TeamRed, Basis: matchengine.TBBasisCaptainAgreement},
		"tbEntry":           tbEntry{Basis: matchengine.TBBasisCaptainAgreement, RequestID: "tb-1", RequestedBy: matchengine.TeamRed},
		"matchResult":       matchResult{Winner: matchengine.TeamRed, Reason: matchengine.ResultReasonFourAlignment, ConfirmingPlayerIDs: []string{"1"}, WonCounts: teamCounts{Red: 4}},
		"stalemateEvidence": stalemateEvidence{WonCounts: teamCounts{Red: 2, Blue: 1}},
	}
	for name, value := range nestedValues {
		t.Run("nested/"+name, func(t *testing.T) {
			encoded := mustJSON(value)
			var document any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			definition, ok := definitions[name].(map[string]any)
			if !ok {
				t.Fatalf("contract definition %q is missing", name)
			}
			assertMatchesDefinition(t, definitions, definition, document, name)
		})
	}

	for _, name := range []string{
		"eventFact", "poolSlot", "board", "boardCell", "boardPiece", "timer",
		"teamCounts", "teamFlags", "roster", "rosters", "tbRequest", "tbEntry",
		"matchResult", "stalemateEvidence",
	} {
		definition, ok := definitions[name].(map[string]any)
		if !ok {
			t.Errorf("realtime contract is missing nested definition %q", name)
			continue
		}
		if definition["additionalProperties"] != false {
			t.Errorf("nested definition %q must reject undocumented fields", name)
		}
	}
	assertPropertyReference(t, definitions, "event", "fact", "#/$defs/eventFact")
	assertPropertyReference(t, definitions, "snapshot", "board", "#/$defs/board")
	assertPropertyReference(t, definitions, "snapshot", "timer", "#/$defs/timer")
	assertPropertyReference(t, definitions, "snapshot", "rosters", "#/$defs/rosters")
}

func serverEnvelope(value envelope) envelope {
	now := time.Now().UTC()
	value.SchemaVersion = realtimeSchemaVersion
	value.ServerTime = &now
	return value
}

func assertMatchesDefinition(t *testing.T, definitions, definition map[string]any, value any, path string) {
	t.Helper()
	if reference, ok := definition["$ref"].(string); ok {
		const prefix = "#/$defs/"
		if len(reference) <= len(prefix) || reference[:len(prefix)] != prefix {
			t.Fatalf("%s has unsupported reference %q", path, reference)
		}
		resolved, ok := definitions[reference[len(prefix):]].(map[string]any)
		if !ok {
			t.Fatalf("%s references missing definition %q", path, reference)
		}
		assertMatchesDefinition(t, definitions, resolved, value, path)
		return
	}

	switch definition["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s = %T, want object", path, value)
		}
		assertObjectMatchesDefinition(t, definitions, definition, object, path)
	case "array":
		items, ok := value.([]any)
		if !ok {
			t.Fatalf("%s = %T, want array", path, value)
		}
		itemDefinition, _ := definition["items"].(map[string]any)
		for index, item := range items {
			assertMatchesDefinition(t, definitions, itemDefinition, item, path+"["+strconv.Itoa(index)+"]")
		}
	case "string":
		if _, ok := value.(string); !ok {
			t.Fatalf("%s = %T, want string", path, value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || number != math.Trunc(number) {
			t.Fatalf("%s = %v, want integer", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			t.Fatalf("%s = %T, want boolean", path, value)
		}
	}

	if enum, ok := definition["enum"].([]any); ok {
		for _, candidate := range enum {
			if reflect.DeepEqual(candidate, value) {
				return
			}
		}
		t.Fatalf("%s = %v, want one of %v", path, value, enum)
	}
}

func assertObjectMatchesDefinition(t *testing.T, definitions, definition, object map[string]any, path string) {
	t.Helper()
	required, _ := definition["required"].([]any)
	for _, raw := range required {
		name, _ := raw.(string)
		if _, exists := object[name]; !exists {
			t.Fatalf("%s is missing contract field %q", path, name)
		}
	}
	properties, _ := definition["properties"].(map[string]any)
	for name, value := range object {
		property, exists := properties[name]
		if !exists {
			t.Fatalf("%s contains undocumented field %q", path, name)
		}
		propertyDefinition, ok := property.(map[string]any)
		if !ok {
			t.Fatalf("%s.%s has an invalid contract definition", path, name)
		}
		assertMatchesDefinition(t, definitions, propertyDefinition, value, path+"."+name)
	}
}

func assertPropertyReference(t *testing.T, definitions map[string]any, definitionName, propertyName, expected string) {
	t.Helper()
	definition, ok := definitions[definitionName].(map[string]any)
	if !ok {
		t.Fatalf("contract definition %q is missing", definitionName)
	}
	properties, ok := definition["properties"].(map[string]any)
	if !ok {
		t.Fatalf("contract definition %q has no properties", definitionName)
	}
	property, ok := properties[propertyName].(map[string]any)
	if !ok || property["$ref"] != expected {
		t.Errorf("%s.%s reference = %v, want %q", definitionName, propertyName, property["$ref"], expected)
	}
}
