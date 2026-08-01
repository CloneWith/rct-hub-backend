package domain

import (
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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
		pos := Position{X: x, Y: 0}
		b.Place(ModNM, pos, "NM-1", red)
		// Engine requires explicit result confirmation.
		b.SetOwner(pos, red)
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

func TestBoardBSONRoundTrip(t *testing.T) {
	b := NewBoard()
	red := TeamSideRed

	b.Place(ModNM, Position{X: 0, Y: 0}, "NM-1", red)
	b.Place(ModHD, Position{X: 2, Y: 0}, "HD-1", red)

	data, err := bson.Marshal(b)
	if err != nil {
		t.Fatalf("BSON marshal: %v", err)
	}

	var restored Board
	if err := bson.Unmarshal(data, &restored); err != nil {
		t.Fatalf("BSON unmarshal: %v", err)
	}

	// Verify the NM-1 piece survived the round trip.
	nmCell := PositionCell(0, 0)
	nmPiece, ok := restored.PieceAt(nmCell)
	if !ok {
		t.Fatal("NM-1 lost after BSON round trip")
	}
	if nmPiece.ID != "NM-1" || nmPiece.Mod != ModNM {
		t.Errorf("NM-1: expected ID=NM-1, Mod=NM; got ID=%s, Mod=%s", nmPiece.ID, nmPiece.Mod)
	}
	if nmPiece.Owner == nil || *nmPiece.Owner != red {
		t.Error("NM-1: owner should be red")
	}

	// Verify the HD-1 piece survived the round trip.
	hdCell := PositionCell(2, 0)
	hdPiece, ok := restored.PieceAt(hdCell)
	if !ok {
		t.Fatal("HD-1 lost after BSON round trip")
	}
	if hdPiece.ID != "HD-1" || hdPiece.Mod != ModHD {
		t.Errorf("HD-1: expected ID=HD-1, Mod=HD; got ID=%s, Mod=%s", hdPiece.ID, hdPiece.Mod)
	}

	// Verify no stray pieces.
	if len(restored.Pieces()) != 2 {
		t.Errorf("expected 2 pieces after round trip, got %d", len(restored.Pieces()))
	}
}

func TestParseSlotRefRoundTrip(t *testing.T) {
	tests := []SlotRef{
		{Mod: ModNM, Index: 1},
		{Mod: ModNM, Index: 12},
		{Mod: ModHD, Index: 3},
		{Mod: ModDT, Index: 1},
		{Mod: ModFM, Index: 5},
		{Mod: ModShiro, Index: 1},
		{Mod: ModTB, Index: 1},
	}
	for _, want := range tests {
		got, ok := ParseSlotRef(want.String())
		if !ok {
			t.Errorf("ParseSlotRef(%q): not ok", want.String())
			continue
		}
		if got.Mod != want.Mod || got.Index != want.Index {
			t.Errorf("ParseSlotRef(%q): got {Mod:%s, Index:%d}, want {Mod:%s, Index:%d}",
				want.String(), got.Mod, got.Index, want.Mod, want.Index)
		}
	}
}

func TestMarkDeadKeepsPieceOnBoard(t *testing.T) {
	b := NewBoard()
	red := TeamSideRed

	pos := Position{X: 0, Y: 0}
	b.Place(ModNM, pos, "NM-1", red)
	b.SetOwner(pos, red)

	// Mark the piece as dead — it must remain occupying its cell.
	b.MarkDeadByIDs([]string{"NM-1"})

	// Cell should still be occupied (engine invariant).
	if b.Empty(PositionCell(0, 0)) {
		t.Fatal("DEAD piece was removed from board")
	}

	piece, ok := b.PieceAt(PositionCell(0, 0))
	if !ok {
		t.Fatal("DEAD piece missing from board")
	}
	if piece.Outcome != OutcomeDead {
		t.Errorf("expected OutcomeDead, got %s", piece.Outcome)
	}

	// DEAD pieces should not count toward four-in-a-row.
	if b.HasFour(red) {
		t.Error("DEAD pieces should not count toward alignment")
	}
}

func TestTeamSideCaseNormalisation(t *testing.T) {
	tests := []struct {
		jsonInput string
		want      TeamSide
	}{
		{`"RED"`, TeamSideRed},
		{`"red"`, TeamSideRed},
		{`"BLUE"`, TeamSideBlue},
		{`"blue"`, TeamSideBlue},
		{`"Red"`, TeamSideRed},
		{`"Blue"`, TeamSideBlue},
	}
	for _, tt := range tests {
		var s TeamSide
		if err := json.Unmarshal([]byte(tt.jsonInput), &s); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", tt.jsonInput, err)
			continue
		}
		if s != tt.want {
			t.Errorf("UnmarshalJSON(%s): got %q, want %q", tt.jsonInput, s, tt.want)
		}
	}
}

func TestTeamSideBSONCaseNormalisation(t *testing.T) {
	// Create a BSON document with a TeamSide field, then round-trip it.
	type doc struct {
		Val TeamSide `bson:"val"`
	}

	tests := []struct {
		input TeamSide
		want  TeamSide
	}{
		{TeamSide("RED"), TeamSideRed},
		{TeamSide("red"), TeamSideRed},
		{TeamSide("BLUE"), TeamSideBlue},
		{TeamSide("blue"), TeamSideBlue},
	}
	for _, tt := range tests {
		raw, err := bson.Marshal(doc{Val: tt.input})
		if err != nil {
			t.Fatalf("marshal %q: %v", tt.input, err)
		}
		var decoded doc
		if err := bson.Unmarshal(raw, &decoded); err != nil {
			t.Errorf("UnmarshalBSON(%q): %v", tt.input, err)
			continue
		}
		if decoded.Val != tt.want {
			t.Errorf("UnmarshalBSON(%q): got %q, want %q", tt.input, decoded.Val, tt.want)
		}
	}
}
