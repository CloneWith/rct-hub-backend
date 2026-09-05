package graphql

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
	"rctHubBackend/pkg/jwtutil"
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

	// Assigned strategist with the strategist role: admitted as RED.
	assigned := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RoleStrategist}}
	team, err := strategistViewerTeam(assigned, room, redTeam, blueTeam)
	if err != nil || team != matchengine.TeamRed {
		t.Fatalf("assigned strategist = %s, %v", team, err)
	}

	// Assignment is authoritative even with the role revoked: the user
	// is still the red team's StrategistID, so the read remains valid.
	revoked := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RolePlayer}}
	if _, err := strategistViewerTeam(revoked, room, redTeam, blueTeam); err != nil {
		t.Fatalf("assignment should override missing role, got %v", err)
	}

	// Strategist from another match (OnlineID does not match either
	// team's StrategistID) is rejected regardless of role.
	strategistElsewhere := &domain.User{OnlineID: 9999, Roles: []domain.UserRole{domain.RoleStrategist}}
	if _, err := strategistViewerTeam(strategistElsewhere, room, redTeam, blueTeam); err == nil {
		t.Fatal("strategist from another match remained authorized")
	}

	// Assigned to both teams is rejected (the side is ambiguous).
	blueTeam.StrategistID = &redID
	if _, err := strategistViewerTeam(assigned, room, redTeam, blueTeam); err == nil {
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

// TestPrivateMatchContextAcceptsCasualAndPrivateRooms locks down the fix for
// the regression where matchByCode(...).strategistView / captainView /
// refereeView failed with "formal match room relationship is invalid" for any
// room whose Type was not "match". BuildFormalMatchSeed (service layer) wires
// formal matches onto casual and private rooms too, so the resolver must
// trust the room.MatchID back-pointer regardless of room type.
func TestPrivateMatchContextAcceptsCasualAndPrivateRooms(t *testing.T) {
	for _, roomType := range []domain.RoomType{domain.RoomTypeMatch, domain.RoomTypeCasual, domain.RoomTypePrivate} {
		t.Run(string(roomType), func(t *testing.T) {
			matchID, roomID := bson.NewObjectID(), bson.NewObjectID()
			user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
			room := &domain.Room{ID: roomID, Type: roomType, OwnerID: 100, MatchID: &matchID}
			resolver := NewResolver(nil).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room})
			ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
			gotRoom, gotUser, err := resolver.privateMatchContext(ctx, matchID.Hex(), roomID.Hex())
			if err != nil {
				t.Fatalf("privateMatchContext rejected %s room: %v", roomType, err)
			}
			if gotRoom == nil || gotRoom.ID != roomID {
				t.Fatalf("room = %+v, want %s", gotRoom, roomID.Hex())
			}
			if gotUser == nil || gotUser.OnlineID != 100 {
				t.Fatalf("user = %+v, want OnlineID=100", gotUser)
			}
		})
	}
}

// TestPrivateMatchContextRejectsMismatchedMatchID guards the back-pointer
// invariant: the room must reference the match we are authorizing for. This
// covers the "casual room with a stale or cross-roomed MatchID" case.
func TestPrivateMatchContextRejectsMismatchedMatchID(t *testing.T) {
	matchID, otherMatchID, roomID := bson.NewObjectID(), bson.NewObjectID(), bson.NewObjectID()
	user := &domain.User{OnlineID: 100, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
	room := &domain.Room{ID: roomID, Type: domain.RoomTypeCasual, OwnerID: 100, MatchID: &otherMatchID}
	resolver := NewResolver(nil).WithPrivateReaders(ircUserReader{user}, ircRoomReader{room})
	ctx := WithClaims(context.Background(), &jwtutil.Claims{OsuID: 100})
	if _, _, err := resolver.privateMatchContext(ctx, matchID.Hex(), roomID.Hex()); err == nil {
		t.Fatal("mismatched match ID was accepted")
	}
	room.MatchID = nil
	if _, _, err := resolver.privateMatchContext(ctx, matchID.Hex(), roomID.Hex()); err == nil {
		t.Fatal("room with nil MatchID was accepted")
	}
}

// TestStrategistViewerRejectsAdminWithRefereeViewHint locks the contract
// that an admin reaching strategistView sees a dedicated error message
// pointing at refereeView (where admin already passes the auth gate via
// authorizeRefereeViewer's admin override). This avoids the misleading
// "current strategist role is required" output for the most common
// oversight: an admin testing the strategist-shaped read model.
func TestStrategistViewerRejectsAdminWithRefereeViewHint(t *testing.T) {
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, StrategistID: func() *int64 { v := int64(2001); return &v }()}
	blueTeam := &domain.Team{ID: blueTeamID, StrategistID: func() *int64 { v := int64(3001); return &v }()}
	admin := &domain.User{OnlineID: 100, Roles: []domain.UserRole{domain.RoleAdmin}}
	_, err := strategistViewerTeam(admin, room, redTeam, blueTeam)
	if err == nil {
		t.Fatal("admin was admitted to strategistView")
	}
	if !strings.Contains(err.Error(), "refereeView") {
		t.Fatalf("admin error does not point at refereeView: %v", err)
	}
	if strings.Contains(err.Error(), "strategist role is required") {
		t.Fatalf("admin got the generic strategist-role message: %v", err)
	}
}

// TestCaptainViewerRejectsAdminWithRefereeViewHint mirrors the strategist
// test for the captain-shaped read model.
func TestCaptainViewerRejectsAdminWithRefereeViewHint(t *testing.T) {
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, LeaderID: func() *int64 { v := int64(2001); return &v }()}
	blueTeam := &domain.Team{ID: blueTeamID, LeaderID: func() *int64 { v := int64(3001); return &v }()}
	admin := &domain.User{OnlineID: 100, Roles: []domain.UserRole{domain.RoleAdmin}}
	_, err := captainViewerTeam(admin, room, redTeam, blueTeam)
	if err == nil {
		t.Fatal("admin was admitted to captainView")
	}
	if !strings.Contains(err.Error(), "refereeView") {
		t.Fatalf("admin error does not point at refereeView: %v", err)
	}
}

// TestStrategistViewerKeepsRoleAndAssignmentGatesForNonAdmin ensures the
// admin-specific message did not weaken the existing checks: a user with
// no strategist role still gets the generic role-required error, and a
// strategist assigned to both teams still gets the assignment error.
func TestStrategistViewerKeepsRoleAndAssignmentGatesForNonAdmin(t *testing.T) {
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, StrategistID: func() *int64 { v := int64(2001); return &v }()}
	blueTeam := &domain.Team{ID: blueTeamID, StrategistID: func() *int64 { v := int64(3001); return &v }()}

	player := &domain.User{OnlineID: 100, Roles: []domain.UserRole{domain.RolePlayer}}
	if _, err := strategistViewerTeam(player, room, redTeam, blueTeam); err == nil ||
		strings.Contains(err.Error(), "refereeView") {
		t.Fatalf("player should get role-required error, got %v", err)
	}

	strategist := &domain.User{OnlineID: 100, Roles: []domain.UserRole{domain.RoleStrategist}}
	if _, err := strategistViewerTeam(strategist, room, redTeam, blueTeam); err == nil ||
		strings.Contains(err.Error(), "refereeView") {
		t.Fatalf("unassigned strategist should get assignment error, got %v", err)
	}
}

// TestStrategistViewerAdmitsAssignmentOverRole locks the assignment-first
// decision order: a user who is the actual StrategistID of one of the
// room's teams is admitted even when their role table does not list
// "strategist". This is the multi-role case from the test account that
// holds every role on the user side and matches the team's StrategistID
// through admin-managed configuration; before the reorder the role check
// shadowed the assignment check and rejected the request. The reverse
// direction (role without assignment) still produces the dedicated
// "not uniquely assigned" error.
func TestStrategistViewerAdmitsAssignmentOverRole(t *testing.T) {
	redID := int64(1001)
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, StrategistID: &redID}
	blueTeam := &domain.Team{ID: blueTeamID, StrategistID: func() *int64 { v := int64(2001); return &v }()}

	// Admin-only user who is *also* the red team's StrategistID (e.g.
	// because admin configuration wrote the assignment). Should pass.
	adminAssignee := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RoleAdmin}}
	team, err := strategistViewerTeam(adminAssignee, room, redTeam, blueTeam)
	if err != nil || team != matchengine.TeamRed {
		t.Fatalf("admin assignee should be admitted as red strategist, got team=%s err=%v", team, err)
	}

	// Player-only user who happens to be the red team's StrategistID.
	// Assignment still wins — no role requirement gates the read.
	playerAssignee := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RolePlayer}}
	if _, err := strategistViewerTeam(playerAssignee, room, redTeam, blueTeam); err != nil {
		t.Fatalf("player assignee should be admitted by assignment, got %v", err)
	}

	// User *without* assignment but *with* strategist role gets the
	// "not uniquely assigned" branch (not the generic role-required one).
	strategistElsewhere := &domain.User{OnlineID: 9999, Roles: []domain.UserRole{domain.RoleStrategist}}
	_, err = strategistViewerTeam(strategistElsewhere, room, redTeam, blueTeam)
	if err == nil || !strings.Contains(err.Error(), "not uniquely assigned") {
		t.Fatalf("unassigned strategist should see assignment error, got %v", err)
	}
	if strings.Contains(err.Error(), "refereeView") {
		t.Fatalf("unassigned strategist should not see admin hint: %v", err)
	}
}

// TestCaptainViewerAdmitsAssignmentOverRole mirrors the strategist test
// for the captain read model: a user whose OnlineID matches a team's
// LeaderID is admitted regardless of role table contents.
func TestCaptainViewerAdmitsAssignmentOverRole(t *testing.T) {
	redID := int64(1001)
	redTeamID, blueTeamID := bson.NewObjectID(), bson.NewObjectID()
	room := &domain.Room{Settings: domain.RoomSettings{RedTeamID: &redTeamID, BlueTeamID: &blueTeamID}}
	redTeam := &domain.Team{ID: redTeamID, LeaderID: &redID}
	blueTeam := &domain.Team{ID: blueTeamID, LeaderID: func() *int64 { v := int64(2001); return &v }()}

	adminAssignee := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RoleAdmin}}
	team, err := captainViewerTeam(adminAssignee, room, redTeam, blueTeam)
	if err != nil || team != matchengine.TeamRed {
		t.Fatalf("admin assignee should be admitted as red captain, got team=%s err=%v", team, err)
	}

	playerAssignee := &domain.User{OnlineID: redID, Roles: []domain.UserRole{domain.RolePlayer}}
	if _, err := captainViewerTeam(playerAssignee, room, redTeam, blueTeam); err != nil {
		t.Fatalf("player assignee should be admitted by assignment, got %v", err)
	}
}
