package matchfixture

import (
	"testing"

	"rctHubBackend/internal/matchengine"
)

func TestScenariosCoverWebLifecycleContractAndValidate(t *testing.T) {
	scenarios, err := Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"READY": false, "BAN": false, "PICK": false, "WAITING_FOR_RESULT": false,
		"SUSPENDED": false, "TB_PREPARATION": false, "TB_PLAYING": false,
		"FINISHED": false, "ABORTED": false, "ADJUDICATION_REQUIRED": false,
	}
	for _, scenario := range scenarios {
		if err := matchengine.ValidateState(scenario.Match.State); err != nil {
			t.Errorf("%s is invalid: %v", scenario.Name, err)
		}
		if _, exists := want[scenario.Name]; !exists {
			t.Errorf("unexpected scenario %s", scenario.Name)
		} else {
			want[scenario.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing scenario %s", name)
		}
	}
}
