package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Round categorizes a room/match into a stage of the season.
//
// This is intentionally a typed string alias so the BSON driver can store
// the value as a plain string and GraphQL can map it to its own enum. The
// Round constants enumerate the canonical stage names; see
// RoundEliminated for the legacy-compat fallback.
//
// Custom UnmarshalBSONValue / UnmarshalJSON route every incoming string
// through FromLegacyString so that pre-enum documents (e.g. rooms with
// `round: "quarterfinal"`) deserialize into the closest current value.
type Round string

const (
	// RoundQualifiers marks the qualification stage (7* pool). NOTE: the
	// QualifierService itself is not yet implemented; admin currently sets
	// TeamStanding.Seed via the setTeamSeed mutation. The constant exists
	// to keep the schema stable while that flow is designed.
	RoundQualifiers Round = "qualifiers"

	// RoundWarmup marks Week 1 (Warmup Matches, 6.6-6.8* pool, +20/+6).
	RoundWarmup Round = "warmup"

	// RoundAssault marks Week 2 (Assault Matches, 7-7.2* pool, +40/+12).
	RoundAssault Round = "assault"

	// RoundProtracted marks Week 3 (Protracted Matches, 6.8-7* pool, +30/+8).
	RoundProtracted Round = "protracted"

	// RoundFinals marks Week 4 (Final Matches, 7.2-7.5* pool). Points are
	// NOT accumulated for this week; the round exists only to record the
	// decisive fixtures (1v2, 3v4, 5v6) in standings queries.
	RoundFinals Round = "finals"

	// RoundEliminated marks a legacy round string that does not map to any
	// current stage. Rooms carrying this value are read-only fossils from
	// the prior single-elimination bracket; they survive so that no
	// pre-migration data is silently lost.
	RoundEliminated Round = "eliminated"
)

// AllRounds lists every valid Round constant in canonical (stage) order.
// RoundEliminated is appended last because it is a compat label, not a
// playable stage.
var AllRounds = []Round{
	RoundQualifiers,
	RoundWarmup,
	RoundAssault,
	RoundProtracted,
	RoundFinals,
	RoundEliminated,
}

// legacyRoundAliases captures deprecated free-text round values from the
// previous single-elimination bracket. Both "quarterfinal" and "semifinal"
// are routed to RoundFinals because they were stages of the final bracket
// in the legacy format (the rulebook describes the prior format as
// "QF, SF, F, GF" and the only post-elimination stage is the finals).
var legacyRoundAliases = map[string]Round{
	"quarterfinal":  RoundFinals,
	"quarterfinals": RoundFinals,
	"semifinal":     RoundFinals,
	"semifinals":    RoundFinals,
}

// FromLegacyString converts a string (potentially from pre-enum data) to a
// Round. Recognized values map directly; "quarterfinal*" / "semifinal*"
// map to Finals; empty input returns the zero Round (callers that require
// a non-empty value should validate separately). Any other unknown value
// returns RoundEliminated so legacy data is never silently dropped.
func FromLegacyString(s string) Round {
	if s == "" {
		return ""
	}
	r := Round(s)
	if r.IsValid() {
		return r
	}
	if mapped, ok := legacyRoundAliases[strings.ToLower(s)]; ok {
		return mapped
	}
	return RoundEliminated
}

// IsValid reports whether r is one of the documented Round constants,
// including RoundEliminated. The zero value (empty Round) is NOT valid.
func (r Round) IsValid() bool {
	return slices.Contains(AllRounds, r)
}

// IsPlayable reports whether r is a round for which standings are
// accumulated. RoundQualifiers is excluded because the QualifierService is
// not yet implemented; RoundEliminated is excluded because those rooms
// are read-only fossils.
func (r Round) IsPlayable() bool {
	switch r {
	case RoundWarmup, RoundAssault, RoundProtracted, RoundFinals:
		return true
	}
	return false
}

// String implements fmt.Stringer. Invalid values render as their raw form
// so debug logs do not collapse them to "(unknown)".
func (r Round) String() string {
	if r == "" {
		return ""
	}
	if !r.IsValid() {
		return fmt.Sprintf("Round(%q)", string(r))
	}
	return string(r)
}

// MarshalBSONValue stores the Round as its underlying string form. We rely
// on the bson driver to serialize the string with type code 0x02 (string);
// the value is whatever the caller wrote (no further normalization is
// applied at write time, so writing a RoundEliminated preserves the
// "I came from a legacy value" signal in the document).
//
// Note: the bson.ValueMarshaler interface declares the type parameter as
// plain byte (not the bson.Type alias) so we manually convert here.
func (r Round) MarshalBSONValue() (byte, []byte, error) {
	typ, data, err := bson.MarshalValue(string(r))
	return byte(typ), data, err
}

// UnmarshalBSONValue reads a BSON string and routes it through
// FromLegacyString so old documents with values like "quarterfinal"
// deserialize into RoundFinals rather than an invalid Round constant.
func (r *Round) UnmarshalBSONValue(typ byte, value []byte) error {
	var s string
	if err := bson.UnmarshalValue(bson.Type(typ), value, &s); err != nil {
		return fmt.Errorf("domain.Round: bson unmarshal: %w", err)
	}
	*r = FromLegacyString(s)
	return nil
}

// MarshalJSON emits the Round as its underlying string. Empty / invalid
// values render as JSON null so downstream GraphQL resolvers can decide
// how to surface them (typically as the enum value ROUND_ELIMINATED or
// a custom null).
func (r Round) MarshalJSON() ([]byte, error) {
	if r == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(r))
}

// UnmarshalJSON accepts either a JSON string or null. Strings route
// through FromLegacyString so legacy inputs normalize transparently.
func (r *Round) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// null or absent → leave the Round as zero value.
		if string(data) == "null" {
			*r = ""
			return nil
		}
		return fmt.Errorf("domain.Round: json unmarshal: %w", err)
	}
	*r = FromLegacyString(s)
	return nil
}
