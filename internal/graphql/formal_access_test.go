package graphql

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
)

func TestPrivateViewerUsesCurrentUserState(t *testing.T) {
	for _, test := range []struct {
		name string
		user *domain.User
	}{
		{"banned", &domain.User{VerifyStatus: domain.Verified, IsBanned: true}},
		{"pending", &domain.User{VerifyStatus: domain.Pending}},
		{"unverified", &domain.User{VerifyStatus: domain.Unverified}},
		{"missing", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validatePrivateViewer(test.user); err == nil {
				t.Fatal("expected viewer to be rejected")
			}
		})
	}
	if err := validatePrivateViewer(&domain.User{VerifyStatus: domain.Verified}); err != nil {
		t.Fatalf("verified viewer rejected: %v", err)
	}
}

func TestStrategistViewerRequiresCurrentRoleAndRoomAssignment(t *testing.T) {
	redID, blueID := int64(1001), int64(2001)
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, StrategistID: &redID}
	blueTeam := &domain.Team{ID: blueTeamID, StrategistID: &blueID}
	user := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RoleStrategist}}
	team, err := strategistViewerTeam(user, room, redTeam, blueTeam)
	if err != nil || team != matchengine.TeamRed {
		t.Fatalf("assigned strategist = %s, %v", team, err)
	}
	user.Roles = []domain.UserRole{domain.RolePlayer}
	if _, err := strategistViewerTeam(user, room, redTeam, blueTeam); err == nil {
		t.Fatal("revoked strategist role remained authorized")
	}
	user.Roles = []domain.UserRole{domain.RoleStrategist}
	user.OnlineID = 9999
	if _, err := strategistViewerTeam(user, room, redTeam, blueTeam); err == nil {
		t.Fatal("strategist from another match remained authorized")
	}
	user.OnlineID = redID
	blueTeam.StrategistID = &redID
	if _, err := strategistViewerTeam(user, room, redTeam, blueTeam); err == nil {
		t.Fatal("strategist assigned to both teams remained authorized")
	}
}

func TestRefereeViewerRequiresAssignmentUnlessAdmin(t *testing.T) {
	refereeID := int64(1001)
	room := &domain.Room{OwnerID: 1001, RefereeUserID: &refereeID}
	assigned := &domain.User{OnlineID: 1001, Roles: []domain.UserRole{domain.RoleReferee}}
	if err := authorizeRefereeViewer(assigned, room); err != nil {
		t.Fatal(err)
	}
	unassigned := &domain.User{OnlineID: 2001, Roles: []domain.UserRole{domain.RoleReferee}}
	if err := authorizeRefereeViewer(unassigned, room); err == nil {
		t.Fatal("unassigned referee remained authorized")
	}
	admin := &domain.User{OnlineID: 2001, Roles: []domain.UserRole{domain.RoleAdmin}}
	if err := authorizeRefereeViewer(admin, room); err != nil {
		t.Fatalf("admin override rejected: %v", err)
	}
}

func TestCaptainViewerRequiresRoomLeaderAssignment(t *testing.T) {
	redID := int64(1001)
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, LeaderID: &redID}
	blueTeam := &domain.Team{ID: blueTeamID}
	team, err := captainViewerTeam(&domain.User{OnlineID: redID}, room, redTeam, blueTeam)
	if err != nil || team != matchengine.TeamRed {
		t.Fatalf("assigned captain = %s, %v", team, err)
	}
	if _, err := captainViewerTeam(&domain.User{OnlineID: 9999}, room, redTeam, blueTeam); err == nil {
		t.Fatal("unassigned captain remained authorized")
	}
	blueTeam.LeaderID = &redID
	if _, err := captainViewerTeam(&domain.User{OnlineID: redID}, room, redTeam, blueTeam); err == nil {
		t.Fatal("captain assigned to both teams remained authorized")
	}
}
