package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"rctHubBackend/internal/graphql"
	"rctHubBackend/internal/matchfixture"
)

const fixtureQuery = `query Fixture($code: String!) {
  matchByCode(code: $code) {
    id code name roomID
    pool { poolSlotID beatmapID beatmap { onlineID title artist difficultyName modString } }
    snapshot {
      version lifecycle phase firstBan firstPick turn activeTeam
      poolSlots { id mod state }
      board { cells { cell row col zone piece { id sourcePoolSlotID mod forceMod selectedBy owner outcome } } }
      wonCounts { red blue }
      timer { startedAt durationMilliseconds paused remainingAtPauseMilliseconds }
      robberyUsed { red blue }
      teamPauseUsed { red blue }
      rosters { red { leaderID playerIDs } blue { leaderID playerIDs } }
      pendingPieceID
      pendingTBRequest { id requestedBy basis }
      tbEntry { basis requestID requestedBy }
      winner
      result { winner reason surrenderingTeam confirmingPlayerIDs wonCounts { red blue } }
      stalemate { wonCounts { red blue } }
    }
    spectatorView { lifecycle currentPhase activeTeam turnNumber wonCounts { red blue } }
    overlayView { lifecycle phase activeTeam timer { startedAt durationMilliseconds paused remainingAtPauseMilliseconds } wonCounts { red blue } }
  }
}`

func main() {
	reader, err := matchfixture.NewReader()
	if err != nil {
		fatal(err)
	}
	scenarios, err := matchfixture.Scenarios()
	if err != nil {
		fatal(err)
	}
	server := graphql.NewHandler(graphql.NewResolver(nil).WithFormalMatchReader(reader).WithBeatmapReader(reader))
	destination := filepath.Join("contracts", "fixtures")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		fatal(err)
	}
	for _, scenario := range scenarios {
		payload, _ := json.Marshal(map[string]any{"query": fixtureQuery, "variables": map[string]string{"code": scenario.Match.Code}})
		request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(`"errors"`)) {
			fatal(fmt.Errorf("generate %s: %s", scenario.Name, response.Body.String()))
		}
		var document any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			fatal(err)
		}
		formatted, _ := json.MarshalIndent(document, "", "  ")
		name := strings.ToLower(scenario.Name) + ".json"
		if err := os.WriteFile(filepath.Join(destination, name), append(formatted, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
