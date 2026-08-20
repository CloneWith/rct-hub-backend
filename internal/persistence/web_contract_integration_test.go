package persistence_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/authsession"
	"rctHubBackend/internal/domain"
	rctgraphql "rctHubBackend/internal/graphql"
	"rctHubBackend/internal/matchcommand"
	"rctHubBackend/internal/persistence"
	"rctHubBackend/internal/realtime"
	"rctHubBackend/internal/repository"
	"rctHubBackend/internal/service"
	"rctHubBackend/pkg/jwtutil"
)

func TestMongoIntegrationGraphQLAndRealtimeConverge(t *testing.T) {
	client, db := integrationMongo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	gin.SetMode(gin.TestMode)

	users := repository.NewUserRepository(db)
	rooms := repository.NewRoomRepository(db)
	matches := repository.NewMatchRepository(db)
	room := integrationFormalRoom()
	referee := &domain.User{
		ID: bson.NewObjectID(), OnlineID: room.OwnerID, Username: "web-referee",
		VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleReferee},
	}
	if err := users.Create(ctx, referee); err != nil {
		t.Fatalf("create referee: %v", err)
	}
	if err := rooms.Create(ctx, &room); err != nil {
		t.Fatalf("create room: %v", err)
	}
	snapshots := persistence.NewSnapshotStore(db)
	if err := ensureIntegrationCollection(ctx, db, persistence.MatchSnapshotsCollection); err != nil {
		t.Fatal(err)
	}
	if err := snapshots.InstallValidator(ctx); err != nil {
		t.Fatalf("install snapshot validator: %v", err)
	}
	commands := persistence.NewCommandStore(client, db)
	if err := commands.EnsureIndexes(ctx); err != nil {
		t.Fatalf("ensure command indexes: %v", err)
	}
	if err := commands.InstallValidators(ctx); err != nil {
		t.Fatalf("install command validators: %v", err)
	}
	seedTime := time.Date(2026, time.August, 14, 14, 0, 0, 0, time.UTC)
	seed, err := service.BuildFormalMatchSeed(room, seedTime)
	if err != nil {
		t.Fatalf("BuildFormalMatchSeed: %v", err)
	}
	if err := persistence.NewFormalMatchBootstrapStore(client, db).Create(ctx, room.ID, seed.LegacyMatch, seed.State, seedTime); err != nil {
		t.Fatalf("bootstrap formal match: %v", err)
	}

	orchestrator := matchcommand.NewOrchestrator(commands, users, matches, rooms, func() time.Time { return seedTime.Add(time.Second) }, nil)
	signer := jwtutil.NewSigner(strings.Repeat("x", 32), "test")
	token, err := signer.Generate(referee.ID.Hex(), referee.OnlineID, referee.Username, referee.Roles, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	gql := rctgraphql.NewHandler(rctgraphql.NewResolver(nil, orchestrator))
	router := gin.New()
	router.POST("/graphql", rctgraphql.GinGraphQL(gql, signer, nil, nil, authsession.CookieConfig{Name: "session"}))
	commandID := "018f4f2c-8f4f-7fd0-a55e-34a7f1a09409"
	query := `mutation { startMatch(input: {matchId: "` + seed.LegacyMatch.ID.Hex() + `", expectedVersion: "0", commandId: "` + commandID + `"}) { success resultingVersion snapshot { lifecycle phase version } error { code message } } }`
	body, _ := json.Marshal(map[string]string{"query": query})
	request := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GraphQL start response status=%d body=%s", response.Code, response.Body.String())
	}
	var graphqlResponse struct {
		Data struct {
			StartMatch struct {
				Success          bool   `json:"success"`
				ResultingVersion string `json:"resultingVersion"`
				Snapshot         struct {
					Lifecycle string `json:"lifecycle"`
					Phase     string `json:"phase"`
					Version   string `json:"version"`
				} `json:"snapshot"`
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"startMatch"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &graphqlResponse); err != nil {
		t.Fatalf("decode GraphQL start response: %v; body=%s", err, response.Body.String())
	}
	start := graphqlResponse.Data.StartMatch
	if len(graphqlResponse.Errors) != 0 || !start.Success || start.Error != nil || start.ResultingVersion != "1" ||
		start.Snapshot.Version != "1" || start.Snapshot.Lifecycle != "RUNNING" || start.Snapshot.Phase != "BAN" {
		t.Fatalf("GraphQL start response = %+v", graphqlResponse)
	}

	gateway := realtime.NewGateway(snapshots, commands, signer, nil, "session", nil, nil, nil)
	server := httptest.NewServer(gateway)
	defer server.Close()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("dial realtime gateway: %v", err)
	}
	defer connection.Close(websocket.StatusNormalClosure, "")
	subscribe, _ := json.Marshal(map[string]any{"type": "subscribe", "schemaVersion": 1, "matchId": seed.LegacyMatch.ID.Hex()})
	if err := connection.Write(ctx, websocket.MessageText, subscribe); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_, message, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read realtime snapshot: %v", err)
	}
	var envelope struct {
		Type     string `json:"type"`
		Version  uint64 `json:"version"`
		Sequence uint64 `json:"sequence"`
		Snapshot struct {
			Lifecycle string `json:"lifecycle"`
			Phase     string `json:"phase"`
			Version   uint64 `json:"version"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil {
		t.Fatalf("decode realtime snapshot: %v", err)
	}
	if envelope.Type != "snapshot" || envelope.Version != 1 || envelope.Sequence == 0 ||
		envelope.Snapshot.Version != 1 || envelope.Snapshot.Lifecycle != "RUNNING" || envelope.Snapshot.Phase != "BAN" {
		t.Fatalf("realtime snapshot = %+v", envelope)
	}
}
