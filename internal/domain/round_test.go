package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRoundIsValid(t *testing.T) {
	cases := []struct {
		r    Round
		want bool
	}{
		{RoundQualifiers, true},
		{RoundWarmup, true},
		{RoundAssault, true},
		{RoundProtracted, true},
		{RoundFinals, true},
		{RoundEliminated, true},
		{Round(""), false},
		{Round("unknown"), false},
		{Round("Quarterfinal"), false}, // case-sensitive on canonical path
		{Round("QUALIFIERS"), false},   // intentionally upper-case is not canonical
	}
	for _, c := range cases {
		if got := c.r.IsValid(); got != c.want {
			t.Errorf("Round(%q).IsValid() = %v, want %v", string(c.r), got, c.want)
		}
	}
}

func TestRoundIsPlayable(t *testing.T) {
	cases := []struct {
		r    Round
		want bool
	}{
		{RoundQualifiers, false}, // QualifierService not yet implemented
		{RoundWarmup, true},
		{RoundAssault, true},
		{RoundProtracted, true},
		{RoundFinals, true}, // Finals decides podium but still counts as "playable"
		{RoundEliminated, false},
		{Round(""), false},
		{Round("bogus"), false},
	}
	for _, c := range cases {
		if got := c.r.IsPlayable(); got != c.want {
			t.Errorf("Round(%q).IsPlayable() = %v, want %v", string(c.r), got, c.want)
		}
	}
}

func TestRoundString(t *testing.T) {
	if got := RoundWarmup.String(); got != "warmup" {
		t.Errorf("RoundWarmup.String() = %q, want \"warmup\"", got)
	}
	if got := Round("").String(); got != "" {
		t.Errorf("Round(\"\").String() = %q, want \"\"", got)
	}
	if got := Round("bogus").String(); !strings.Contains(got, "bogus") {
		t.Errorf("Round(\"bogus\").String() = %q, want it to contain the raw value", got)
	}
}

func TestFromLegacyString(t *testing.T) {
	cases := []struct {
		in   string
		want Round
	}{
		// Empty input → zero value.
		{"", ""},

		// Canonical values pass through.
		{"qualifiers", RoundQualifiers},
		{"warmup", RoundWarmup},
		{"assault", RoundAssault},
		{"protracted", RoundProtracted},
		{"finals", RoundFinals},
		{"eliminated", RoundEliminated},

		// Legacy aliases map to Finals.
		{"quarterfinal", RoundFinals},
		{"quarterfinals", RoundFinals},
		{"semifinal", RoundFinals},
		{"semifinals", RoundFinals},
		{"QUARTERFINAL", RoundFinals}, // case-insensitive for legacy only
		{"Semifinal", RoundFinals},

		// Unknown values fall back to Eliminated.
		{"grand_final", RoundEliminated},
		{"round_of_16", RoundEliminated},
		{"swiss_r1", RoundEliminated},
		{"xyz", RoundEliminated},

		// Canonical values are case-sensitive (per IsValid). "WARMUP" is
		// not a valid Round constant, so it routes through the legacy
		// alias map (no match) and falls back to Eliminated.
		{"WARMUP", RoundEliminated},
	}
	for _, c := range cases {
		got := FromLegacyString(c.in)
		if got != c.want {
			t.Errorf("FromLegacyString(%q) = %q, want %q", c.in, string(got), string(c.want))
		}
	}
}

func TestRoundBSONMarshalUnmarshal_RoundTrip(t *testing.T) {
	// Wrap Round in a struct because bson.Marshal requires a document at
	// the top level (a bare BSON value is only legal as a field value).
	type wrapper struct {
		Round Round `bson:"round"`
	}
	for _, r := range []Round{RoundQualifiers, RoundWarmup, RoundAssault, RoundProtracted, RoundFinals, RoundEliminated} {
		t.Run(string(r), func(t *testing.T) {
			data, err := bson.Marshal(wrapper{Round: r})
			if err != nil {
				t.Fatalf("bson.Marshal: %v", err)
			}
			var got wrapper
			if err := bson.Unmarshal(data, &got); err != nil {
				t.Fatalf("bson.Unmarshal: %v", err)
			}
			if got.Round != r {
				t.Errorf("round-trip got %q, want %q", string(got.Round), string(r))
			}
		})
	}
}

func TestRoundBSONUnmarshal_LegacyValues(t *testing.T) {
	// Hand-craft a BSON document with `round: "quarterfinal"` (legacy
	// string) and verify it deserializes into RoundFinals, not the
	// invalid Round("quarterfinal"). The wrapper struct lets us drive
	// bson.Marshal/Unmarshal the standard way.
	type wrapper struct {
		Round Round `bson:"round"`
	}
	cases := []struct {
		rawValue string
		want     Round
	}{
		{"quarterfinal", RoundFinals},
		{"semifinal", RoundFinals},
		{"swiss_r1", RoundEliminated},
		{"warmup", RoundWarmup},
	}
	for _, c := range cases {
		t.Run(c.rawValue, func(t *testing.T) {
			data, err := bson.Marshal(wrapper{Round: Round(c.rawValue)})
			if err != nil {
				t.Fatalf("bson.Marshal: %v", err)
			}
			var got wrapper
			if err := bson.Unmarshal(data, &got); err != nil {
				t.Fatalf("bson.Unmarshal: %v", err)
			}
			if got.Round != c.want {
				t.Errorf("legacy %q unmarshaled to %q, want %q", c.rawValue, string(got.Round), string(c.want))
			}
		})
	}
}

func TestRoundJSONMarshalUnmarshal(t *testing.T) {
	// Round-trip canonical values via JSON.
	for _, r := range []Round{RoundWarmup, RoundAssault, RoundFinals} {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		var got Round
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if got != r {
			t.Errorf("json round-trip: got %q, want %q", string(got), string(r))
		}
	}
}

func TestRoundJSONUnmarshal_LegacyAndNull(t *testing.T) {
	cases := []struct {
		in   string
		want Round
	}{
		{`"quarterfinal"`, RoundFinals},
		{`"semifinals"`, RoundFinals},
		{`"warmup"`, RoundWarmup},
		{`"unknown_thing"`, RoundEliminated},
		{`null`, ""}, // null → zero value, no error
	}
	for _, c := range cases {
		var got Round
		if err := json.Unmarshal([]byte(c.in), &got); err != nil {
			t.Errorf("json.Unmarshal(%s): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("json.Unmarshal(%s) = %q, want %q", c.in, string(got), string(c.want))
		}
	}
}

func TestRoundJSONMarshal_ZeroValue(t *testing.T) {
	// The zero Round should serialize to JSON null (not empty string)
	// so GraphQL can distinguish "unset" from "empty string".
	var r Round
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("zero Round JSON = %s, want null", string(data))
	}
}
