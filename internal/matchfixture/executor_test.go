package matchfixture

import (
	"context"
	"testing"

	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/matchengine"
)

func TestExecutorSupportsApplyReplayAndVersionConflict(t *testing.T) {
	reader, err := NewReader()
	if err != nil {
		t.Fatal(err)
	}
	ready, err := reader.ByCode(context.Background(), "FIXTURE_READY")
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(reader)
	request := matchcommand.Request{
		MatchID: ready.ID, ExpectedVersion: 0, CommandID: "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409",
		CallerOsuID: 1001, Command: matchengine.StartMatch{},
	}
	applied, err := executor.Execute(context.Background(), request)
	if err != nil || applied.Disposition != matchcommand.DispositionApplied || applied.ResultingVersion != 1 {
		t.Fatalf("applied = %+v, %v", applied, err)
	}
	replayed, err := executor.Execute(context.Background(), request)
	if err != nil || replayed.Disposition != matchcommand.DispositionReplayed || replayed.ResultingVersion != 1 {
		t.Fatalf("replayed = %+v, %v", replayed, err)
	}
	request.CommandID = "028f4f2c-8f4f-7fd0-a55e-34a7f1a09409"
	_, err = executor.Execute(context.Background(), request)
	commandErr := matchcommand.ErrorOf(err)
	if commandErr == nil || commandErr.Code != matchcommand.CodeMatchVersionConflict {
		t.Fatalf("version conflict = %v", err)
	}
}
