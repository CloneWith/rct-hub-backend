package matchfixture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"rctHubBackend/internal/graphql"
	"rctHubBackend/internal/matchfixture"
	"rctHubBackend/pkg/jwtutil"
)

func TestMockExposesPoolMetadataAndPrivateViews(t *testing.T) {
	reader, err := matchfixture.NewReader()
	if err != nil {
		t.Fatal(err)
	}
	resolver := graphql.NewResolver(nil, matchfixture.NewExecutor(reader)).
		WithFormalMatchReader(reader).
		WithBeatmapReader(reader).
		WithPrivateReaders(reader.PrivateUsers(), reader.PrivateRooms())
	server := graphql.NewHandler(resolver)
	query := `query {
		me { onlineID username verifyStatus roles }
		matchByCode(code: "FIXTURE_READY") {
			pool { poolSlotID beatmapID beatmap { onlineID title } }
			strategistView { myTeam analysis { allowedActions } }
			captainView { myTeam analysis { allowedActions } }
			refereeView { snapshot { version } analysis { allowedActions } }
		}
	}`
	payload, _ := json.Marshal(map[string]string{"query": query})
	request := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(graphql.WithClaims(context.Background(), &jwtutil.Claims{OsuID: 1001}))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(`"errors"`)) {
		t.Fatalf("mock private query failed: status=%d body=%s", response.Code, response.Body.String())
	}
	for _, expected := range [][]byte{[]byte(`"me":{"onlineID":"1001"`), []byte(`"poolSlotID"`), []byte(`"beatmapID"`), []byte(`"strategistView"`), []byte(`"captainView"`), []byte(`"refereeView"`)} {
		if !bytes.Contains(response.Body.Bytes(), expected) {
			t.Fatalf("mock response is missing %s: %s", expected, response.Body.String())
		}
	}
}
