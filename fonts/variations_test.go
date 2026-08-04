package fonts

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/fonttest"
)

// FeatureVariations: the table that gives a feature different lookups at
// different points in a variable font's design space.
//
// Every fixture here is the same shape, and it is the shape that makes the tests
// mean something. The FeatureList's own 'ccmp' names a lookup that turns x into
// y; a FeatureVariations record names one that turns x into z. A reader that
// ignores the table sets y, a reader that applies the wrong record sets y too,
// and only a reader that reads the table *and* evaluates its conditions sets z.
// Both answers are glyphs the face has, so neither shows up as a missing
// character — which is exactly why this needs a test rather than an eyeball.

// varyingFace builds a face whose 'ccmp' names lookup 0 (x becomes y) and whose
// FeatureVariations carries the given records, which may name lookup 1 (x
// becomes z) instead.
func varyingFace(t *testing.T, records []fonttest.FeatureVariation) *Face {
	t.Helper()
	gsub := fonttest.GSUBTableVarying(
		[]fonttest.Lookup{
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scY})}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scZ})}},
		},
		[]fonttest.Feature{{Tag: "ccmp", Lookups: []int{0}}},
		map[string]fonttest.Script{"DFLT": fonttest.AllFeatures(1)},
		records,
	)
	data := fonttest.SFNT(fonttest.SFNTOptions{
		Name: "Varying",
		Glyphs: []fonttest.Glyph{
			{Rune: 'x', Advance: 500, HasShape: true},
			{Rune: 'y', Advance: 500, HasShape: true},
			{Rune: 'z', Advance: 500, HasShape: true},
		},
		Extra: map[string][]byte{"GSUB": gsub},
	})
	f, err := Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// coversDefault is a condition that holds at the default instance: the whole
// axis. Noto Sans Oriya's is [-1, 0.6848], which covers it the same way.
var coversDefault = fonttest.Condition{Axis: 0, Min: -1, Max: 1}

func TestFeatureVariationsReplaceAFeaturesLookups(t *testing.T) {
	f := varyingFace(t, []fonttest.FeatureVariation{{
		Conditions: []fonttest.Condition{coversDefault},
		Substitute: map[int][]int{0: {1}},
	}})
	if got := lastGID(t, f, "x"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d — the lookup the variation record names, not the one the feature does", got, scZ)
	}
}

// TestFeatureVariationsOnlyAtTheDefaultInstance is the half that decides which
// weight the rules are read for.
//
// Nothing here instances a variable font: the subsetter drops fvar and gvar, and
// what reaches a document is the default instance. So a record stated for the
// heavy end of the axis is a rule for a weight this module never sets, and
// applying it would set the text by rules meant for a different font.
func TestFeatureVariationsOnlyAtTheDefaultInstance(t *testing.T) {
	for _, tc := range []struct {
		why  string
		cond fonttest.Condition
		want int
	}{
		{"the whole axis covers the default", fonttest.Condition{Axis: 0, Min: -1, Max: 1}, scZ},
		{"a range ending at the default covers it", fonttest.Condition{Axis: 0, Min: -1, Max: 0}, scZ},
		{"a range starting at the default covers it", fonttest.Condition{Axis: 0, Min: 0, Max: 1}, scZ},
		{"the heavy end of the axis does not", fonttest.Condition{Axis: 0, Min: 0.25, Max: 1}, scY},
		{"the light end of the axis does not", fonttest.Condition{Axis: 0, Min: -1, Max: -0.25}, scY},
		{"an axis the font does not have reads as zero", fonttest.Condition{Axis: 7, Min: -1, Max: 1}, scZ},
		{"and so does not match a range off zero", fonttest.Condition{Axis: 7, Min: 0.5, Max: 1}, scY},
	} {
		f := varyingFace(t, []fonttest.FeatureVariation{{
			Conditions: []fonttest.Condition{tc.cond},
			Substitute: map[int][]int{0: {1}},
		}})
		if got := lastGID(t, f, "x"); got != tc.want {
			t.Errorf("%s: x shaped to glyph %d, want %d", tc.why, got, tc.want)
		}
	}
}

// TestFeatureVariationsNeedEveryCondition: a condition set is an "and", so one
// condition that does not hold stops the record.
func TestFeatureVariationsNeedEveryCondition(t *testing.T) {
	f := varyingFace(t, []fonttest.FeatureVariation{{
		Conditions: []fonttest.Condition{coversDefault, {Axis: 1, Min: 0.5, Max: 1}},
		Substitute: map[int][]int{0: {1}},
	}})
	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("x shaped to glyph %d, want %d — one condition of the set does not hold at the default", got, scY)
	}
}

// TestFeatureVariationsEmptyConditionSetHoldsEverywhere: a record with no
// conditions, or a null condition-set offset, applies unconditionally.
func TestFeatureVariationsEmptyConditionSetHoldsEverywhere(t *testing.T) {
	for _, rec := range []fonttest.FeatureVariation{
		{Substitute: map[int][]int{0: {1}}},
		{NoConditionSet: true, Substitute: map[int][]int{0: {1}}},
	} {
		f := varyingFace(t, []fonttest.FeatureVariation{rec})
		if got := lastGID(t, f, "x"); got != scZ {
			t.Errorf("x shaped to glyph %d, want %d — a record with nothing to satisfy applies", got, scZ)
		}
	}
}

// TestFeatureVariationsTakeTheFirstMatchingRecord: the records are an ordered
// search, not a set to merge, so a second matching record is not consulted.
func TestFeatureVariationsTakeTheFirstMatchingRecord(t *testing.T) {
	f := varyingFace(t, []fonttest.FeatureVariation{
		{Conditions: []fonttest.Condition{coversDefault}, Substitute: map[int][]int{0: {1}}},
		{Conditions: []fonttest.Condition{coversDefault}, Substitute: map[int][]int{0: {0}}},
	})
	if got := lastGID(t, f, "x"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d — the first matching record decides", got, scZ)
	}
	// And the other way round, so that the test cannot pass by taking the last.
	f = varyingFace(t, []fonttest.FeatureVariation{
		{Conditions: []fonttest.Condition{{Axis: 0, Min: 0.5, Max: 1}}, Substitute: map[int][]int{0: {0}}},
		{Conditions: []fonttest.Condition{coversDefault}, Substitute: map[int][]int{0: {1}}},
	})
	if got := lastGID(t, f, "x"); got != scZ {
		t.Errorf("x shaped to glyph %d, want %d — the first record whose conditions hold decides", got, scZ)
	}
}

// TestFeatureVariationsCanSilenceAFeature: a record that gives a feature an
// empty lookup list has said something, and it is not the same as saying
// nothing. The feature does nothing at this point in the design space.
func TestFeatureVariationsCanSilenceAFeature(t *testing.T) {
	f := varyingFace(t, []fonttest.FeatureVariation{{
		Conditions: []fonttest.Condition{coversDefault},
		Substitute: map[int][]int{0: {}},
	}})
	if got := lastGID(t, f, "x"); got != scX {
		t.Errorf("x shaped to glyph %d, want %d — the record gives 'ccmp' no lookups here", got, scX)
	}
}

// TestFeatureVariationsIgnoreAConditionTheyCannotRead: the specification has
// added condition formats since, and a corrupt font produces unreadable ones
// too. Either way the record is not applied — a rule applied on a guess is a
// rule applied at the wrong weight.
func TestFeatureVariationsIgnoreAConditionTheyCannotRead(t *testing.T) {
	f := varyingFace(t, []fonttest.FeatureVariation{{
		Conditions: []fonttest.Condition{{Axis: 0, Min: -1, Max: 1, Format: 4}},
		Substitute: map[int][]int{0: {1}},
	}})
	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("x shaped to glyph %d, want %d — a condition this cannot evaluate does not hold", got, scY)
	}
}

// TestFeatureVariationsSurviveTruncation: a font is untrusted input, and a
// FeatureVariations table cut anywhere must truncate the walk rather than reach
// outside it — and must leave the run shaped by what the FeatureList itself
// says, rather than not shaped at all.
func TestFeatureVariationsSurviveTruncation(t *testing.T) {
	gsub := fonttest.GSUBTableVarying(
		[]fonttest.Lookup{
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scY})}},
			{Type: 1, Subtables: [][]byte{fonttest.SingleSubst([]int{scX}, []int{scZ})}},
		},
		[]fonttest.Feature{{Tag: "ccmp", Lookups: []int{0}}},
		map[string]fonttest.Script{"DFLT": fonttest.AllFeatures(1)},
		[]fonttest.FeatureVariation{{
			Conditions: []fonttest.Condition{coversDefault, {Axis: 1, Min: -1, Max: 1}},
			Substitute: map[int][]int{0: {1}, 1: {0}},
		}},
	)
	for n := 0; n <= len(gsub); n++ {
		truncated := append([]byte(nil), gsub[:n]...)
		if len(truncated) >= 10 {
			readFeatureVariations(truncated)
		}
	}
}

// TestFeatureVariationsBoundTheirWalk: a font is untrusted input, and the record
// count is 32 bits wide — a font can claim four billion records in four bytes.
// The walk stops at maxVariationRecords, so a record past it is not reached.
//
// The fixture puts the only matching record one past the cap. Two things have to
// hold for that to mean anything, and both are checked: the cap has to be far
// below what a font can claim, and the walk has to stop at it.
func TestFeatureVariationsBoundTheirWalk(t *testing.T) {
	if maxVariationRecords > 1024 {
		t.Fatalf("maxVariationRecords is %d, which is not a bound on anything", maxVariationRecords)
	}
	records := make([]fonttest.FeatureVariation, 0, maxVariationRecords+1)
	for i := 0; i < maxVariationRecords; i++ {
		records = append(records, fonttest.FeatureVariation{
			Conditions: []fonttest.Condition{{Axis: 0, Min: 0.5, Max: 1}},
			Substitute: map[int][]int{0: {0}},
		})
	}
	records = append(records, fonttest.FeatureVariation{
		Conditions: []fonttest.Condition{coversDefault},
		Substitute: map[int][]int{0: {1}},
	})
	f := varyingFace(t, records)
	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("x shaped to glyph %d, want %d — the record past the cap must not be reached", got, scY)
	}
}

// TestFeatureVariationsBoundAConditionSet: the same, for the conditions of one
// record. A set longer than the cap does not hold — half a condition set is not a
// weaker condition set, it is no knowledge of what the record was for.
func TestFeatureVariationsBoundAConditionSet(t *testing.T) {
	if maxConditions > 1024 {
		t.Fatalf("maxConditions is %d, which is not a bound on anything", maxConditions)
	}
	conds := make([]fonttest.Condition, maxConditions+1)
	for i := range conds {
		conds[i] = coversDefault
	}
	f := varyingFace(t, []fonttest.FeatureVariation{{
		Conditions: conds,
		Substitute: map[int][]int{0: {1}},
	}})
	if got := lastGID(t, f, "x"); got != scY {
		t.Errorf("x shaped to glyph %d, want %d — a condition set past the cap does not hold", got, scY)
	}
}
