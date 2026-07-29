package domain

import (
	"testing"
	"time"
)

func TestNewBoard(t *testing.T) {
	b := NewBoard()
	// Board is now a map[Cell]BoardPiece; zones are computed via ZoneAt.
	// Correct layout (col, row):
	//   top-left  (row<2, col<2) = DT
	//   top-right (row<2, col≥2) = HD
	//   bot-left  (row≥2, col<2) = HR
	//   bot-right (row≥2, col≥2) = DT
	expected := [4][4]Zone{
		{ZoneDT, ZoneDT, ZoneHD, ZoneHD},
		{ZoneDT, ZoneDT, ZoneHD, ZoneHD},
		{ZoneHR, ZoneHR, ZoneDT, ZoneDT},
		{ZoneHR, ZoneHR, ZoneDT, ZoneDT},
	}
	for row := range 4 {
		for col := range 4 {
			cell := PositionCell(col, row)
			zone, ok := b.ZoneAt(cell)
			if !ok {
				t.Errorf("cell %s: ZoneAt returned false", cell)
				continue
			}
			if zone != expected[row][col] {
				t.Errorf("cell %s (col=%d,row=%d): expected zone %s, got %s", cell, col, row, expected[row][col], zone)
			}
		}
	}
}

func TestBoardCanPlace(t *testing.T) {
	b := NewBoard()
	tests := []struct {
		mod  Mod
		pos  Position
		want bool
	}{
		{ModNM, Position{X: 0, Y: 0}, true},  // NM free in any zone (DT)
		{ModHD, Position{X: 0, Y: 0}, false}, // HD cannot go in DT zone
		{ModHD, Position{X: 2, Y: 0}, true},  // HD goes in HD zone (top-right)
		{ModDT, Position{X: 0, Y: 0}, true},  // DT goes in DT zone (top-left)
		{ModDT, Position{X: 2, Y: 0}, false}, // DT cannot go in HD zone
	}
	for _, tt := range tests {
		got := b.Place(tt.mod, tt.pos, "test-1", TeamSideRed)
		if got != tt.want {
			t.Errorf("Place(%s, %v) = %v, want %v", tt.mod, tt.pos, got, tt.want)
		}
		if got {
			b.Remove(tt.pos) // clean up for next subtest
		}
	}
}

func TestBoardFourInARow(t *testing.T) {
	b := NewBoard()
	red := TeamSideRed
	for x := range 4 {
		b.Place(ModNM, Position{X: x, Y: 0}, "NM-1", red)
	}
	if !b.HasFour(red) {
		t.Error("expected red to have four in a row")
	}
	if b.HasFour(TeamSideBlue) {
		t.Error("blue should not have four in a row")
	}
}

func TestTurnStateBanOrder(t *testing.T) {
	order := BPOrder{FirstBan: TeamSideRed, FirstPick: TeamSideBlue}
	ts := NewTurnState()
	ts.StartBan(order)

	expected := []struct {
		counter int
		team    TeamSide
		action  TurnAction
	}{
		{-3, TeamSideRed, TurnActionBan},
		{-2, TeamSideBlue, TurnActionBan},
		{-1, TeamSideBlue, TurnActionBan},
		{0, TeamSideRed, TurnActionBan},
	}

	for _, exp := range expected {
		if ts.Counter != exp.counter {
			t.Errorf("counter = %d, want %d", ts.Counter, exp.counter)
		}
		if *ts.ActiveTeam != exp.team {
			t.Errorf("active team = %s, want %s", *ts.ActiveTeam, exp.team)
		}
		if ts.Action != exp.action {
			t.Errorf("action = %s, want %s", ts.Action, exp.action)
		}
		ts.Next(order)
	}

	if ts.Phase != PhasePick {
		t.Errorf("expected phase to transition to pick, got %s", ts.Phase)
	}
	if ts.Counter != 1 {
		t.Errorf("expected pick counter to start at 1, got %d", ts.Counter)
	}
}

func TestTurnStatePickOrder(t *testing.T) {
	order := BPOrder{FirstPick: TeamSideRed, FirstBan: TeamSideBlue}
	ts := NewTurnState()
	ts.StartPick(order)

	for range 4 {
		expected := order.FirstPick
		if ts.Counter%2 == 0 {
			expected = order.FirstPick.Opponent()
		}
		if *ts.ActiveTeam != expected {
			t.Errorf("counter %d: active team = %s, want %s", ts.Counter, *ts.ActiveTeam, expected)
		}
		ts.Next(order)
	}
}

func TestTimerRemaining(t *testing.T) {
	now := time.Now()
	ts := Timer{
		StartedAt: now.Add(-30 * time.Second),
		Duration:  60 * time.Second,
	}
	rem := ts.Remaining(now)
	if rem < 25*time.Second || rem > 35*time.Second {
		t.Errorf("remaining = %v, expected around 30s", rem)
	}

	// Pause should freeze the remaining time
	ts.Pause(now)
	rem = ts.Remaining(now.Add(10 * time.Second))
	if rem < 25*time.Second || rem > 35*time.Second {
		t.Errorf("remaining after pause + 10s = %v, expected around 30s", rem)
	}

	// Resume should restart with the frozen remaining
	ts.Resume(now.Add(10 * time.Second))
	rem = ts.Remaining(now.Add(15 * time.Second))
	if rem < 20*time.Second || rem > 30*time.Second {
		t.Errorf("remaining after resume + 5s = %v, expected around 25s", rem)
	}
}

func TestMappoolFlexible(t *testing.T) {
	pool := NewMappool()
	pool.Slots[ModNM] = []Piece{{}, {}, {}}
	pool.Slots[ModHD] = []Piece{{}, {}}
	pool.Slots[ModShiro] = []Piece{{}}

	if got := len(pool.ActiveSlotsByMod(ModNM)); got != 3 {
		t.Errorf("NM active slots = %d, want 3", got)
	}
	if got := len(pool.ActiveSlotsByMod(ModHD)); got != 2 {
		t.Errorf("HD active slots = %d, want 2", got)
	}

	removed := int64(-1)
	pool.Slots[ModNM][1].BeatmapID = &removed
	if got := len(pool.ActiveSlotsByMod(ModNM)); got != 2 {
		t.Errorf("NM active slots after removal = %d, want 2", got)
	}

	slot := SlotRef{Mod: ModHD, Index: 2}
	if p := pool.FindSlot(slot); p == nil {
		t.Error("expected to find HD-2")
	}
	slot.Index = 5
	if p := pool.FindSlot(slot); p != nil {
		t.Error("expected HD-5 not to exist")
	}
}
