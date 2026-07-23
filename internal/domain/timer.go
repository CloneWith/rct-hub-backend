package domain

import "time"

// Timer durations for each match action.
const (
	// Ban action: 60s + 15s bonus.
	BanTimeLimit = 60 * time.Second
	BanBonusTime = 15 * time.Second

	// Rob (optional) + Pick action: 90s + 30s bonus, includes setup time.
	PickTimeLimit = 90 * time.Second
	PickBonusTime = 30 * time.Second

	// Setting a won piece: 20s + 10s bonus.
	WinTimeLimit = 20 * time.Second
	WinBonusTime = 10 * time.Second

	// Tie-breaker preparation: 90s fixed, includes setup time.
	TBTimeLimit = 90 * time.Second
)

// TimerState represents the running countdown for the current turn.
type TimerState struct {
	StartedAt        time.Time     `json:"started_at" bson:"started_at"`
	TimeLimit        time.Duration `json:"time_limit" bson:"time_limit"`
	BonusTime        time.Duration `json:"bonus_time" bson:"bonus_time"`
	BonusUsed        bool          `json:"bonus_used" bson:"bonus_used"`
	IsPaused         bool          `json:"is_paused" bson:"is_paused"`
	PausedAt         *time.Time    `json:"paused_at,omitempty" bson:"paused_at,omitempty"`
	RemainingAtPause time.Duration `json:"remaining_at_pause,omitempty" bson:"remaining_at_pause,omitempty"`
}

// NewTimerState creates a timer for the given limits.
func NewTimerState(limit, bonus time.Duration) TimerState {
	return TimerState{
		StartedAt: time.Now(),
		TimeLimit: limit,
		BonusTime: bonus,
	}
}

// Remaining returns the time left on the timer (may be negative).
func (ts *TimerState) Remaining() time.Duration {
	if ts.IsPaused {
		return ts.RemainingAtPause
	}
	elapsed := time.Since(ts.StartedAt)
	limit := ts.TimeLimit
	if ts.BonusUsed {
		limit += ts.BonusTime
	}
	return limit - elapsed
}

// UseBonus extends the current turn with the bonus time.
func (ts *TimerState) UseBonus() {
	ts.BonusUsed = true
}

// Pause stops the timer.
func (ts *TimerState) Pause() {
	if ts.IsPaused {
		return
	}
	ts.IsPaused = true
	now := time.Now()
	ts.PausedAt = &now
	ts.RemainingAtPause = ts.Remaining()
}

// Resume restarts the timer from the remaining time.
func (ts *TimerState) Resume() {
	if !ts.IsPaused {
		return
	}
	ts.IsPaused = false
	ts.StartedAt = time.Now().Add(-ts.TimeLimit + ts.RemainingAtPause)
	if ts.BonusUsed {
		ts.StartedAt = ts.StartedAt.Add(-ts.BonusTime)
	}
	ts.PausedAt = nil
}
