package domain

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNewBoard(t *testing.T) {
	b := NewBoard()
	if b.Rows != 4 || b.Cols != 4 {
		t.Fatalf("expected 4x4 board, got %dx%d", b.Cols, b.Rows)
	}
	expected := [4][4]Zone{
		{ZoneHD, ZoneHD, ZoneDT, ZoneDT},
		{ZoneHD, ZoneHD, ZoneDT, ZoneDT},
		{ZoneHR, ZoneHR, ZoneNM, ZoneNM},
		{ZoneHR, ZoneHR, ZoneNM, ZoneNM},
	}
	for y := range 4 {
		for x := range 4 {
			if b.Cells[y][x].Zone != expected[y][x] {
				t.Errorf("cell (%d,%d): expected zone %s, got %s", x, y, expected[y][x], b.Cells[y][x].Zone)
			}
		}
	}
}

func TestBoardCanPlace(t *testing.T) {
	b := NewBoard()
	tests := []struct {
		mod  PieceMod
		pos  Position
		want bool
	}{
		{PieceModNM, Position{X: 0, Y: 0}, true}, // free in any zone
		{PieceModHD, Position{X: 0, Y: 0}, true},
		{PieceModHD, Position{X: 2, Y: 2}, false},
		{PieceModDT, Position{X: 2, Y: 0}, true},
		{PieceModDT, Position{X: 0, Y: 0}, false},
	}
	for _, tt := range tests {
		if got := b.CanPlace(tt.mod, tt.pos); got != tt.want {
			t.Errorf("CanPlace(%s, %v) = %v, want %v", tt.mod, tt.pos, got, tt.want)
		}
	}
}

func TestBoardFourInARow(t *testing.T) {
	b := NewBoard()
	red := string(TeamSideRed)
	for x := range 4 {
		b.Place(PieceModNM, Position{X: x, Y: 0}, "NM-1", red)
	}
	if !b.HasFourInARow(red) {
		t.Error("expected red to have four in a row")
	}
	blue := string(TeamSideBlue)
	if b.HasFourInARow(blue) {
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

	if ts.Phase != MatchPhasePick {
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
	ts := TimerState{
		StartedAt: time.Now().Add(-30 * time.Second),
		TimeLimit: 60 * time.Second,
		BonusTime: 15 * time.Second,
	}
	rem := ts.Remaining()
	if rem < 25*time.Second || rem > 35*time.Second {
		t.Errorf("remaining = %v, expected around 30s", rem)
	}

	ts.UseBonus()
	rem = ts.Remaining()
	if rem < 40*time.Second || rem > 50*time.Second {
		t.Errorf("remaining with bonus = %v, expected around 45s", rem)
	}
}

func TestMappoolFlexible(t *testing.T) {
	pool := NewPool()
	pool.Slots[PieceModNM] = []Piece{{}, {}, {}}
	pool.Slots[PieceModHD] = []Piece{{}, {}}
	pool.Slots[PieceModShiro] = []Piece{{}}

	if got := len(pool.ActiveSlotsByMod(PieceModNM)); got != 3 {
		t.Errorf("NM active slots = %d, want 3", got)
	}
	if got := len(pool.ActiveSlotsByMod(PieceModHD)); got != 2 {
		t.Errorf("HD active slots = %d, want 2", got)
	}

	removed := int64(-1)
	pool.Slots[PieceModNM][1].BeatmapID = &removed
	if got := len(pool.ActiveSlotsByMod(PieceModNM)); got != 2 {
		t.Errorf("NM active slots after removal = %d, want 2", got)
	}

	slot := PoolSlot{Mod: PieceModHD, Index: 2}
	if p := pool.FindSlot(slot); p == nil {
		t.Error("expected to find HD-2")
	}
	slot.Index = 5
	if p := pool.FindSlot(slot); p != nil {
		t.Error("expected HD-5 not to exist")
	}
}

func TestPoolSlotStringParseRoundTrip(t *testing.T) {
	t.Parallel()

	mods := []PieceMod{
		PieceModNM, PieceModHD, PieceModHR, PieceModDT,
		PieceModFM, PieceModShiro, PieceModTB,
	}
	for _, mod := range mods {
		for _, index := range []int{1, 12, 999} {
			want := PoolSlot{Mod: mod, Index: index}
			got, ok := ParsePoolSlot(want.String())
			if !ok || got != want {
				t.Fatalf("ParsePoolSlot(%q) = %+v, %v; want %+v, true", want.String(), got, ok, want)
			}
		}
	}

	for _, invalid := range []string{"", "NM", "NM-0", "NM--1", "NM-1-extra", "XX-1"} {
		if got, ok := ParsePoolSlot(invalid); ok {
			t.Fatalf("ParsePoolSlot(%q) = %+v, true; want rejection", invalid, got)
		}
	}
	for input, want := range map[string]PoolSlot{
		"NM1":   {Mod: PieceModNM, Index: 1},
		"HD12":  {Mod: PieceModHD, Index: 12},
		"TB":    {Mod: PieceModTB, Index: 1},
		"Shiro": {Mod: PieceModShiro, Index: 1},
	} {
		if got, ok := ParsePoolSlot(input); !ok || got != want {
			t.Fatalf("ParsePoolSlot(%q) = %+v, %v; want %+v, true", input, got, ok, want)
		}
	}
}

func TestLegacyMatchBSONRoundTripPreservesBoard(t *testing.T) {
	t.Parallel()

	board := NewBoard()
	position := Position{X: 0, Y: 0}
	if !board.Place(PieceModNM, position, "NM-12", string(TeamSideRed)) {
		t.Fatal("place legacy board fixture")
	}
	want := Match{ID: bson.NewObjectID(), Board: board}

	encoded, err := bson.Marshal(want)
	if err != nil {
		t.Fatalf("marshal match BSON: %v", err)
	}
	var got Match
	if err := bson.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal match BSON: %v", err)
	}
	if !reflect.DeepEqual(got.Board, want.Board) {
		t.Fatalf("board after BSON round trip = %#v; want %#v", got.Board, want.Board)
	}
}
