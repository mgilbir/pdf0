package core

import (
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
)

// type4 is a type-4 function over one input and one output with the given
// program.
func type4(prog string) *object.Stream {
	st := &object.Stream{Data: []byte(prog)}
	st.Dict.Set("FunctionType", object.Integer(4))
	st.Dict.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	st.Dict.Set("Range", object.Array{object.Integer(0), object.Integer(1)})
	return st
}

func evalType4Prog(prog string) ([]float64, bool) {
	return View{Limits: DefaultLimits()}.EvalFunction(type4(prog), []float64{0.25})
}

// TestPostScriptParseIsBounded is audit 2026-09-22 C14. psExec bounded how
// deeply a program executes, but the parser recursed once per '{', so three
// million of them — 6.6 KB of Flate — was a fatal stack overflow before a
// single operator ran. Each bound is asserted as a refusal of the program.
func TestPostScriptParseIsBounded(t *testing.T) {
	cases := []struct{ name, prog string }{
		{"the audit's three million braces", strings.Repeat("{", 3_000_000) + strings.Repeat("}", 3_000_000)},
		// The nested procedure is popped unexecuted, so the program would
		// evaluate to 0.5 if it parsed: only the nesting bound refuses it.
		{"nesting past the bound, inside the length and token bounds", "{ pop 0.5 " + strings.Repeat("{ ", maxPSNesting) + strings.Repeat("} ", maxPSNesting) + "pop }"},
		{"tokens past the bound, inside the length bound", "{ pop 0.5 " + strings.Repeat("0 pop ", maxPSTokens/2) + "}"},
		{"a program past the length bound", "{ pop 0.5 %" + strings.Repeat("x", maxPSProgramBytes) + "\n}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
				if out, ok := evalType4Prog(c.prog); ok {
					t.Fatalf("a program past the parse bounds evaluated to %v", out)
				}
			})
		})
	}
}

// Inside the bounds the parser is unchanged: nested procedures parse, an
// unexecuted procedure nested deeper than psExec will go is still accepted, and
// the program runs.
func TestPostScriptParseWithinBounds(t *testing.T) {
	for _, c := range []struct {
		name string
		prog string
		want float64
	}{
		{"arithmetic", "{ 2 mul 0.25 sub }", 0.25},
		{"if", "{ 0.5 gt { 1 } { 0 } ifelse }", 0},
		{"a deep procedure that never runs", "{ pop 0.5 " + strings.Repeat("{ ", maxPSNesting-1) + strings.Repeat("} ", maxPSNesting-1) + "pop }", 0.5},
		{"a comment", "{ % a comment\n pop 0.75 }", 0.75},
	} {
		out, ok := evalType4Prog(c.prog)
		if !ok || len(out) != 1 || out[0] != c.want {
			t.Errorf("%s: got %v, %v; want [%v]", c.name, out, ok, c.want)
		}
	}
}
