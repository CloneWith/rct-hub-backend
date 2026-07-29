package domain

import "time"

// MatchPhase is an alias for the rule-engine Phase type.
// Deprecated: use Phase instead.
type MatchPhase = Phase

// TurnAction describes what the active team is expected to do this turn.
type TurnAction string

const (
	TurnActionBan  TurnAction = "ban"
	TurnActionRob  TurnAction = "rob" // optional rob before pick
	TurnActionPick TurnAction = "pick"
	TurnActionWin  TurnAction = "win"
	TurnActionTB   TurnAction = "tb"
)

// BPOrder records the team that acts first for ban and pick phases.
type BPOrder struct {
	FirstPick TeamSide `json:"first_pick" bson:"first_pick"`
	FirstBan  TeamSide `json:"first_ban" bson:"first_ban"`
}

// TurnState tracks the current turn and phase progression.
// This is a client-friendly wrapper; the authoritative state lives in State.
type TurnState struct {
	Phase      MatchPhase    `json:"phase" bson:"phase"`
	Counter    int           `json:"counter" bson:"counter"` // -3,-2,-1,0 for ban; 1,2,3... for pick
	ActiveTeam *TeamSide     `json:"active_team,omitempty" bson:"active_team,omitempty"`
	Action     TurnAction    `json:"action" bson:"action"`
	StartedAt  time.Time     `json:"started_at" bson:"started_at"`
	TimeLimit  time.Duration `json:"time_limit" bson:"time_limit"`
	BonusTime  time.Duration `json:"bonus_time" bson:"bonus_time"`
	BonusUsed  bool          `json:"bonus_used" bson:"bonus_used"`
}

// NewTurnState creates a fresh turn state.
func NewTurnState() TurnState {
	return TurnState{
		Phase:     PhaseNone,
		Counter:   0,
		Action:    TurnActionPick,
		BonusUsed: false,
	}
}

// StartBan transitions the state to the ban phase.
func (t *TurnState) StartBan(order BPOrder) {
	t.Phase = PhaseBan
	t.Counter = -3
	t.Action = TurnActionBan
	t.ActiveTeam = &order.FirstBan
	t.BonusUsed = false
	t.StartedAt = time.Now()
	t.TimeLimit = BanDuration
	t.BonusTime = BanAdditionalDuration
}

// StartPick transitions the state to the pick phase.
func (t *TurnState) StartPick(order BPOrder) {
	t.Phase = PhasePick
	t.Counter = 1
	t.Action = TurnActionPick
	t.ActiveTeam = &order.FirstPick
	t.BonusUsed = false
	t.StartedAt = time.Now()
	t.TimeLimit = PickDuration
	t.BonusTime = PickAdditionalDuration
}

// Next advances the turn counter and switches the active team.
func (t *TurnState) Next(order BPOrder) {
	t.Counter++
	t.BonusUsed = false
	t.StartedAt = time.Now()
	switch t.Phase {
	case PhaseBan:
		if t.Counter > 0 {
			t.StartPick(order)
			return
		}
		// ban order is yxxy where y is first ban
		if t.Counter == -2 || t.Counter == -1 {
			opponent := order.FirstBan.Opponent()
			t.ActiveTeam = &opponent
		} else {
			t.ActiveTeam = &order.FirstBan
		}
		t.Action = TurnActionBan
		t.TimeLimit = BanDuration
		t.BonusTime = BanAdditionalDuration
	case PhasePick:
		if t.Counter%2 == 1 {
			t.ActiveTeam = &order.FirstPick
		} else {
			opponent := order.FirstPick.Opponent()
			t.ActiveTeam = &opponent
		}
		t.Action = TurnActionPick
		t.TimeLimit = PickDuration
		t.BonusTime = PickAdditionalDuration
	}
}

// IsTeamTurn reports whether the given team is allowed to act now.
func (t *TurnState) IsTeamTurn(team TeamSide) bool {
	return t.ActiveTeam != nil && *t.ActiveTeam == team
}

// IsBanPhase reports whether the current phase is the ban phase.
func (t *TurnState) IsBanPhase() bool {
	return t.Phase == PhaseBan
}

// IsPickPhase reports whether the current phase is the pick phase.
func (t *TurnState) IsPickPhase() bool {
	return t.Phase == PhasePick
}
