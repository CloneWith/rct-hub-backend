package main

import (
	"slices"
	"testing"
	"time"

	"rctHubBackend/internal/service"
)

// TestSeedConsistency asserts that the seeded room + teams + mappool satisfy
// the service-layer start requirements. The seed is a casual room, so the
// match-only rules (roster size, MP link, pool topology) do not apply, but
// every shared requirement — team readiness, BP order, scheduled time and
// referee (D3) — must hold. This keeps `initdb` from drifting away from the
// domain rules when the start requirements evolve.
func TestSeedConsistency(t *testing.T) {
	t.Parallel()

	seed := BuildSeedData(time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC))
	red, blue, pool := seed.RedTeam, seed.BlueTeam, seed.Mappool
	if missing := service.MissingStartRequirements(seed.Room, &red, &blue, &pool); len(missing) > 0 {
		t.Fatalf("seed room cannot start, missing %v", missing)
	}
	if !red.IsReady() || !blue.IsReady() {
		t.Fatal("seed teams must be ready (leader + strategist)")
	}
	if slices.Contains(red.Players, *blue.LeaderID) || slices.Contains(blue.Players, *red.LeaderID) {
		t.Fatal("seed team rosters must not overlap")
	}
	if seed.Room.Settings.RedTeamID == nil || *seed.Room.Settings.RedTeamID != red.ID {
		t.Fatal("seed room must reference the seeded red team")
	}
	if seed.Room.Settings.BlueTeamID == nil || *seed.Room.Settings.BlueTeamID != blue.ID {
		t.Fatal("seed room must reference the seeded blue team")
	}
	if seed.Room.Settings.MappoolID == nil || *seed.Room.Settings.MappoolID != pool.ID {
		t.Fatal("seed room must reference the seeded mappool")
	}
}
