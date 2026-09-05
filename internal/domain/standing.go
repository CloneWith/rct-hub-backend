package domain

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// WeeklyPoints records the points a single team earned in a single week of
// a single season. WinPoints / LossPoints are awarded automatically when a
// match finishes; ProvisionalExtraPoints is written by the (optional)
// auto-fetch path and is moved into ExtraPoints only after an admin
// confirms via confirmExtraPoints.
//
// The collection name is "weekly_points"; the recommended unique index is
// (season_id, week_index, team_id) so re-writing the same week's row is
// idempotent (upsert by that key).
type WeeklyPoints struct {
	ID     bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Season bson.ObjectID `json:"season" bson:"season"`
	// WeekIndex is the 1-based week number within the season
	// (1=Warmup, 2=Assault, 3=Protracted, 4=Finals, 0=Qualifiers).
	WeekIndex int           `json:"week_index" bson:"week_index"`
	Team      bson.ObjectID `json:"team" bson:"team"`

	// WinPoints is awarded to the winning side; LossPoints to the loser.
	// Both are derived from the WeekRule's WinPoints / LossPoints for the
	// given WeekIndex.
	WinPoints  int `json:"win_points" bson:"win_points"`
	LossPoints int `json:"loss_points" bson:"loss_points"`

	// ProvisionalExtraPoints is populated by the optional auto-fetch path
	// (mplink scrape) before an admin confirms. While this is set,
	// standings queries surface it as a separate "pending" column; after
	// admin confirmation it is moved into ExtraPoints.
	ProvisionalExtraPoints int `json:"provisional_extra_points" bson:"provisional_extra_points"`

	// ExtraPoints is the admin-confirmed extra-points total for the week.
	// Confirmed extra points are subject to the per-week Cap defined on
	// the WeekRule (e.g. Week 1 caps at 12, Week 3 has no cap).
	ExtraPoints int `json:"extra_points" bson:"extra_points"`

	// ExtraPointsConfirmedAt is non-nil once admin has run
	// confirmExtraPoints (or recorded them manually) for the week. After
	// that point ProvisionalExtraPoints is frozen at zero and edits must
	// go through the service layer (which logs the change).
	ExtraPointsConfirmedAt *time.Time `json:"extra_points_confirmed_at,omitempty" bson:"extra_points_confirmed_at,omitempty"`

	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// Total returns the team's confirmed points total for the week. Provisional
// points are intentionally excluded so the standings table does not flicker
// while an admin is still reviewing the auto-fetched draft.
func (w *WeeklyPoints) Total() int {
	if w == nil {
		return 0
	}
	return w.WinPoints + w.LossPoints + w.ExtraPoints
}

// PendingTotal returns the total counting provisional extra points. Used
// by the standings "preview" view shown to admins while review is pending.
func (w *WeeklyPoints) PendingTotal() int {
	if w == nil {
		return 0
	}
	return w.WinPoints + w.LossPoints + w.ExtraPoints + w.ProvisionalExtraPoints
}

// NewWeeklyPoints creates an empty row for the given (season, week, team)
// triple. UpdatedAt is stamped to now.
func NewWeeklyPoints(season, team bson.ObjectID, weekIndex int) *WeeklyPoints {
	return &WeeklyPoints{
		ID:        bson.NewObjectID(),
		Season:    season,
		WeekIndex: weekIndex,
		Team:      team,
		UpdatedAt: time.Now().UTC(),
	}
}

// Validate performs structural validation. Used by service-layer guards
// before write; not a substitute for the MongoDB schema validator.
func (w *WeeklyPoints) Validate() error {
	if w == nil {
		return errors.New("domain.WeeklyPoints: nil")
	}
	if w.Season.IsZero() {
		return errors.New("domain.WeeklyPoints: Season is required")
	}
	if w.Team.IsZero() {
		return errors.New("domain.WeeklyPoints: Team is required")
	}
	if w.WeekIndex < 0 || w.WeekIndex > 4 {
		return errors.New("domain.WeeklyPoints: WeekIndex must be in [0,4]")
	}
	if w.WinPoints < 0 || w.LossPoints < 0 || w.ExtraPoints < 0 || w.ProvisionalExtraPoints < 0 {
		return errors.New("domain.WeeklyPoints: point counters must be non-negative")
	}
	return nil
}

// =============================================================================

// TeamStanding is the cumulative cross-week snapshot for a single team
// inside a single season. It is recomputed lazily by the standing service
// when a WeeklyPoints row changes; the recompute is cheap (a single
// aggregation per team per season) so there is no incremental view to
// maintain.
//
// The collection name is "team_standings"; the recommended unique index is
// (season, team) so the upsert is idempotent.
type TeamStanding struct {
	ID     bson.ObjectID `json:"id" bson:"_id,omitempty"`
	Season bson.ObjectID `json:"season" bson:"season"`
	Team   bson.ObjectID `json:"team" bson:"team"`

	// Seed is the qualification rank assigned at the end of the qualifier
	// stage. Set by admin via the setTeamSeed mutation; QualifierService
	// (which would set this automatically) is not yet implemented.
	// Seed=0 means "not yet seeded"; Seed>0 is the rank (1 is best).
	Seed int `json:"seed" bson:"seed"`

	// Credit is the rolling rank computed from TotalPoints, with Seed as
	// the tiebreaker. Computed by StandingService.CalculateCredit after
	// every WeeklyPoints update; never written by hand. Credit=0 means
	// "not yet ranked" (the team has no weekly points yet).
	Credit int `json:"credit" bson:"credit"`

	// TotalPoints is the sum of all WeeklyPoints.Total() for this team
	// across weeks 1..4. Provisional extra points are NOT included here
	// (same rule as WeeklyPoints.Total). WeekIndex=4 (Finals) is also
	// excluded from standings — Finals decides podium only.
	TotalPoints int `json:"total_points" bson:"total_points"`

	Wins   int `json:"wins" bson:"wins"`
	Losses int `json:"losses" bson:"losses"`

	// ExtraPoints mirrors the sum of WeeklyPoints.ExtraPoints across all
	// weeks. Kept separate from TotalPoints so the standings page can
	// surface a "X from bonus / Y from W/L" breakdown without re-aggregating
	// the weekly collection on every render.
	ExtraPoints int `json:"extra_points" bson:"extra_points"`

	// EliminatedAt is non-nil for teams that finished outside the top 6
	// of qualifiers. Such teams may still play practice matches but their
	// season is over (no more weekly points accumulate, no Credit assigned).
	EliminatedAt *time.Time `json:"eliminated_at,omitempty" bson:"eliminated_at,omitempty"`

	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// NewTeamStanding constructs an empty row for the (season, team) pair.
func NewTeamStanding(season, team bson.ObjectID) *TeamStanding {
	return &TeamStanding{
		ID:        bson.NewObjectID(),
		Season:    season,
		Team:      team,
		UpdatedAt: time.Now().UTC(),
	}
}

// Validate performs structural validation. The standing service runs this
// before upsert.
func (s *TeamStanding) Validate() error {
	if s == nil {
		return errors.New("domain.TeamStanding: nil")
	}
	if s.Season.IsZero() {
		return errors.New("domain.TeamStanding: Season is required")
	}
	if s.Team.IsZero() {
		return errors.New("domain.TeamStanding: Team is required")
	}
	if s.Seed < 0 {
		return errors.New("domain.TeamStanding: Seed must be >= 0")
	}
	if s.Credit < 0 {
		return errors.New("domain.TeamStanding: Credit must be >= 0")
	}
	if s.TotalPoints < 0 || s.ExtraPoints < 0 {
		return errors.New("domain.TeamStanding: point counters must be non-negative")
	}
	if s.Wins < 0 || s.Losses < 0 {
		return errors.New("domain.TeamStanding: win/loss counters must be non-negative")
	}
	return nil
}

// IsEliminated reports whether the team is out of the season.
func (s *TeamStanding) IsEliminated() bool {
	return s != nil && s.EliminatedAt != nil
}
