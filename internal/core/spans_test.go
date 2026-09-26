package core

import (
	"math"
	"math/rand"
	"testing"
)

// TestResolveSpansMatchesExpansion checks ResolveSpans against the expansion
// it replaces — every range written into a map in definition order, the last
// write (or the first) winning — over random overlapping ranges, and at the
// ends of the code space, where Hi+1 overflows 32 bits.
func TestResolveSpansMatchesExpansion(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 500; iter++ {
		n := rng.Intn(12)
		var ranges []Span
		for i := 0; i < n; i++ {
			lo := uint32(rng.Intn(64) + 3)
			hi := lo + uint32(rng.Intn(20)) - 3 // sometimes inverted
			ranges = append(ranges, Span{Lo: lo, Hi: hi})
		}
		for _, lastWins := range []bool{false, true} {
			want := map[uint32]int{}
			for i, r := range ranges {
				if r.Hi < r.Lo {
					continue
				}
				for c := r.Lo; c <= r.Hi; c++ {
					if _, set := want[c]; set && !lastWins {
						continue
					}
					want[c] = i
				}
			}
			s := ResolveSpans(ranges, lastWins)
			for c := uint32(0); c < 100; c++ {
				got, ok := s.Find(c)
				w, wok := want[c]
				if ok != wok || (ok && got.Idx != w) {
					t.Fatalf("ranges %v lastWins=%v: code %d → (%v,%v), want (%d,%v)", ranges, lastWins, c, got, ok, w, wok)
				}
			}
			for i := 1; i < len(s); i++ {
				if s[i].Lo <= s[i-1].Hi {
					t.Fatalf("segments overlap: %v", s)
				}
			}
		}
	}

	top := ResolveSpans([]Span{{Lo: math.MaxUint32 - 1, Hi: math.MaxUint32}, {Lo: 0, Hi: 0}}, true)
	if sp, ok := top.Find(math.MaxUint32); !ok || sp.Idx != 0 {
		t.Errorf("the last code: %v %v", sp, ok)
	}
	if sp, ok := top.Find(0); !ok || sp.Idx != 1 {
		t.Errorf("code 0: %v %v", sp, ok)
	}
}

// TestResolveSpansIsLinearithmic: 2,000 copies of the whole 16-bit code space
// resolve to one segment, without touching the codes.
func TestResolveSpansIsLinearithmic(t *testing.T) {
	var ranges []Span
	for i := 0; i < 2000; i++ {
		ranges = append(ranges, Span{Lo: 0, Hi: 0xFFFF})
	}
	s := ResolveSpans(ranges, true)
	if len(s) != 1 || s[0].Idx != 1999 {
		t.Fatalf("got %v", s)
	}
}
