package matchcommand

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"rctHubBackend/internal/domain"
	"rctHubBackend/internal/matchengine"
)

// This table is the backend half of the browser acceptance matrix. The page
// may hide a control, but the command boundary must enforce the same result.
func TestFormalRoomControlAcceptanceMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		caller int64
		mutate func(*commandFixture)
		want   ErrorCode
	}{
		{name: "ordinary verified user", caller: 3101, want: CodeGlobalRoleRequired},
		{name: "player", caller: 1101, want: CodeGlobalRoleRequired},
		{name: "strategist", caller: redStrategistOsuID, want: CodeGlobalRoleRequired},
		{name: "streamer", caller: 3201, want: CodeGlobalRoleRequired},
		{name: "unassigned referee", caller: refereeOsuID, mutate: func(f *commandFixture) {
			f.room.RefereeUserID = func() *int64 { value := int64(9999); return &value }()
		}, want: CodeRoomRoleRequired},
		{name: "pending referee", caller: refereeOsuID, mutate: func(f *commandFixture) {
			f.users[refereeOsuID].VerifyStatus = domain.Pending
		}, want: CodeUserNotVerified},
		{name: "banned referee", caller: refereeOsuID, mutate: func(f *commandFixture) {
			f.users[refereeOsuID].IsBanned = true
		}, want: CodeUserBanned},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommandFixture(t)
			fixture.users[3101] = &domain.User{OnlineID: 3101, VerifyStatus: domain.Verified}
			fixture.users[3201] = &domain.User{OnlineID: 3201, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleStreamer}}
			if test.mutate != nil {
				test.mutate(fixture)
			}
			_, err := fixture.orchestrator.Execute(context.Background(), Request{
				MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: uuid.NewString(),
				CallerOsuID: test.caller, Command: matchengine.StartMatch{},
			})
			assertCommandError(t, err, test.want)
		})
	}
}

func TestFormalRoomControlAcceptanceAdminAndAssignedReferee(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		caller int64
		setup  func(*commandFixture)
	}{
		{name: "assigned referee", caller: refereeOsuID},
		{name: "admin override", caller: 3301, setup: func(f *commandFixture) {
			f.users[3301] = &domain.User{OnlineID: 3301, VerifyStatus: domain.Verified, Roles: []domain.UserRole{domain.RoleAdmin}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommandFixture(t)
			if test.setup != nil {
				test.setup(fixture)
			}
			result, err := fixture.orchestrator.Execute(context.Background(), Request{
				MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: uuid.NewString(),
				CallerOsuID: test.caller, Command: matchengine.StartMatch{},
			})
			if err != nil || result.Disposition != DispositionApplied || result.ResultingVersion != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestFormalRoomControlAcceptanceRejectsInvalidLifecycleTransition(t *testing.T) {
	t.Parallel()

	fixture := newCommandFixture(t)
	first, err := fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 0, CommandID: uuid.NewString(),
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	})
	if err != nil || first.ResultingVersion != 1 {
		t.Fatalf("first start result=%+v err=%v", first, err)
	}
	_, err = fixture.orchestrator.Execute(context.Background(), Request{
		MatchID: fixture.match.ID, ExpectedVersion: 1, CommandID: uuid.NewString(),
		CallerOsuID: refereeOsuID, Command: matchengine.StartMatch{},
	})
	assertCommandError(t, err, ErrorCode(matchengine.CodeMatchLifecycleConflict))
}
