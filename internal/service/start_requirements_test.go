package service

import (
	"testing"

	"rctHubBackend/internal/domain"
)

func TestMissingStartRequirements(t *testing.T) {
	t.Parallel()

	// Match rooms report every missing setting. Red/blue team readiness and
	// roster size are separate requirements, so an unlinked side is reported
	// once per requirement (duplicates included).
	room := domain.Room{Type: domain.RoomTypeMatch}
	got := MissingStartRequirements(room, nil, nil, nil)
	if len(got) != 8 {
		t.Fatalf("expected 8 missing fields for empty match room, got %v", got)
	}

	// Casual rooms require the shared subset only.
	room.Type = domain.RoomTypeCasual
	got = MissingStartRequirements(room, nil, nil, nil)
	if len(got) != 4 {
		t.Fatalf("expected 4 missing fields for empty casual room, got %v", got)
	}

	// Private rooms have no requirements.
	room.Type = domain.RoomTypePrivate
	got = MissingStartRequirements(room, nil, nil, nil)
	if len(got) != 0 {
		t.Fatalf("expected no requirements for private room, got %v", got)
	}

	// Unknown types are reported as invalid.
	room.Type = domain.RoomType("bogus")
	got = MissingStartRequirements(room, nil, nil, nil)
	if len(got) != 1 || got[0] != "type" {
		t.Fatalf("expected [type] for unknown room type, got %v", got)
	}

	// A fully configured match room reports nothing.
	link := "https://osu.ppy.sh/community/matches/1"
	firstPick, firstBan := domain.TeamSideRed, domain.TeamSideBlue
	redLeader, blueLeader := int64(1), int64(11)
	redStrategist, blueStrategist := int64(101), int64(201)
	redTeam := domain.Team{
		LeaderID:     &redLeader,
		StrategistID: &redStrategist,
		Players:      []int64{1, 2, 3, 4, 5, 6, 7, 8},
	}
	blueTeam := domain.Team{
		LeaderID:     &blueLeader,
		StrategistID: &blueStrategist,
		Players:      []int64{11, 12, 13, 14, 15, 16, 17, 18},
	}
	pool := domain.Mappool{Name: "Pool"}
	complete := domain.Room{
		Type: domain.RoomTypeMatch,
		Settings: domain.RoomSettings{
			FirstPick: &firstPick,
			FirstBan:  &firstBan,
			MPLink:    &link,
		},
	}
	if got = MissingStartRequirements(complete, &redTeam, &blueTeam, &pool); len(got) != 0 {
		t.Fatalf("expected complete room to start, missing %v", got)
	}
}
