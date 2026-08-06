package html

import (
	"strings"
	"testing"
	"time"
)

// TestParseIsLinearInDocumentSize guards against a shape of bug this parser had
// and nothing else could see.
//
// The tokenizer used to ask "does the rest of the file start with <!doctype?" by
// lowercasing the rest of the file — at every "<". That is quadratic, and it was
// invisible in every correctness test because it produces the right answer: a
// megabyte of small elements simply took sixty-six seconds. Anyone able to hand
// this engine a document could hang it.
//
// The guard is a wall-clock bound, which is not the kind of assertion to reach
// for lightly. It is the right one here because the quantity under test *is*
// time and because the margin is three orders of magnitude: the document below
// parses in about forty milliseconds and took over a minute before the fix. A
// bound of five seconds cannot be tripped by a slow machine and cannot be
// survived by a reintroduction.
func TestParseIsLinearInDocumentSize(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40000; i++ {
		b.WriteString("<div><span>x</span></div>")
	}
	src := b.String()

	start := time.Now()
	doc, _, _ := Parse(src)
	elapsed := time.Since(start)

	if doc == nil {
		t.Fatal("the document produced no tree")
	}
	if elapsed > 5*time.Second {
		t.Errorf("parsing %d bytes of small elements took %v; it is linear work and "+
			"should take tens of milliseconds, so this is the quadratic scan coming back",
			len(src), elapsed)
	}
}

// TestCaseInsensitiveSearchIsCorrect pins the two helpers the fix introduced,
// because a faster search that is also wrong would be worse than the slow one.
func TestCaseInsensitiveSearchIsCorrect(t *testing.T) {
	for _, tc := range []struct {
		s, prefix string
		want      bool
	}{
		{"<!DOCTYPE html>", "<!doctype", true},
		{"<!DoCtYpE html>", "<!doctype", true},
		{"<!doctype html>", "<!doctype", true},
		{"<!docty", "<!doctype", false},
		{"<div>", "<!doctype", false},
		{"", "", true},
	} {
		if got := hasPrefixFold(tc.s, tc.prefix); got != tc.want {
			t.Errorf("hasPrefixFold(%q, %q) = %v, want %v", tc.s, tc.prefix, got, tc.want)
		}
	}

	for _, tc := range []struct {
		s, sub string
		want   int
	}{
		{"abc</STYLE>", "</style", 3},
		{"abc</style>", "</style", 3},
		{"</styl", "</style", -1},
		{"xx</Style></style>", "</style", 2},
		{"nothing", "</style", -1},
		// The needle's own case must not matter either, since findEndTag builds
		// it from a tag name that has already been folded once.
		{"abc</style>", "</STYLE", 3},
	} {
		if got := indexFold(tc.s, tc.sub); got != tc.want {
			t.Errorf("indexFold(%q, %q) = %d, want %d", tc.s, tc.sub, got, tc.want)
		}
	}
}
