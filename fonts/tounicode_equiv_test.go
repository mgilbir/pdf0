package fonts

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

// The hand-written hex has to produce exactly what fmt did.
//
// A ToUnicode CMap is how text comes back out of a PDF. A digit in the wrong
// case, a missing zero pad or a mishandled surrogate pair does not show on the
// page at all — it shows when someone copies the text, which is the failure
// this format exists to prevent. So the replacement is held against the
// original rather than against a reading of what it should do.

// fmtVersion is the code this replaced, kept here as the oracle.
func fmtVersion(pairs [][2]int, codespace string) []byte {
	var b bytes.Buffer
	b.WriteString(`/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
` + codespace + `
endcodespacerange
`)
	digits := 4
	if len(codespace) > 0 && codespace[0] == '<' && len(codespace) < 12 {
		digits = 2
	}
	utf16beHex := func(r rune) string {
		if r > 0xFFFF {
			r -= 0x10000
			return fmt.Sprintf("%04X%04X", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		}
		return fmt.Sprintf("%04X", r)
	}
	for start := 0; start < len(pairs); start += 100 {
		end := start + 100
		if end > len(pairs) {
			end = len(pairs)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, p := range pairs[start:end] {
			fmt.Fprintf(&b, "<%0*X> <%s>\n", digits, p[0], utf16beHex(rune(p[1])))
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString(`endcmap
CMapName currentdict /CMap defineresource pop
end
end
`)
	return b.Bytes()
}

func TestToUnicodeCMapMatchesTheFormattedVersion(t *testing.T) {
	codespaces := []string{"<0000> <FFFF>", "<00> <FF>"}

	// The interesting values, then a lot of random ones. The edges are where a
	// hand-written hex writer goes wrong: the pad boundaries, the ends of the
	// BMP, and either side of the astral cutoff where a surrogate pair starts.
	var edge [][2]int
	for _, code := range []int{0, 1, 9, 10, 15, 16, 255, 256, 4095, 4096, 65535} {
		for _, r := range []rune{0x01, 0x09, 0x0A, 0x0F, 0x10, 0x41, 0x7F, 0xFF, 0x100,
			0x0FFF, 0x1000, 0xD7FF, 0xE000, 0xFFFD, 0xFFFF, 0x10000, 0x10001, 0x1F600, 0x10FFFF} {
			edge = append(edge, [2]int{code, int(r)})
		}
	}

	rng := rand.New(rand.NewSource(1))
	var random [][2]int
	for i := 0; i < 5000; i++ {
		random = append(random, [2]int{rng.Intn(0x10000), rng.Intn(0x110000)})
	}

	for _, cs := range codespaces {
		for name, pairs := range map[string][][2]int{
			"empty":  nil,
			"one":    {{0x41, 0x61}},
			"edges":  edge,
			"random": random,
			// Exactly at, and either side of, the 100-entry section cap.
			"99":  random[:99],
			"100": random[:100],
			"101": random[:101],
		} {
			got := buildToUnicodeCMap(pairs, cs)
			want := fmtVersion(pairs, cs)
			if !bytes.Equal(got, want) {
				// Name the first difference rather than dumping two CMaps.
				at := 0
				for at < len(got) && at < len(want) && got[at] == want[at] {
					at++
				}
				lo := at - 40
				if lo < 0 {
					lo = 0
				}
				t.Fatalf("%s/%s: differs at byte %d\n  got:  %q\n  want: %q",
					cs, name, at, snippet(got, lo), snippet(want, lo))
			}
		}
	}
}

func snippet(b []byte, from int) string {
	to := from + 80
	if to > len(b) {
		to = len(b)
	}
	if from > len(b) {
		return "<past the end>"
	}
	return string(b[from:to])
}

// TestTheHexWriterPadsAndUppercases, the two properties a reader depends on.
func TestTheHexWriterPadsAndUppercases(t *testing.T) {
	for _, tc := range []struct {
		v, n int
		want string
	}{
		{0, 4, "0000"}, {0, 2, "00"}, {10, 4, "000A"}, {255, 2, "FF"},
		{0xABCD, 4, "ABCD"}, {0xFFFF, 4, "FFFF"}, {1, 2, "01"},
	} {
		var b bytes.Buffer
		appendHex(&b, tc.v, tc.n)
		if b.String() != tc.want {
			t.Errorf("appendHex(%d, %d) = %q, want %q", tc.v, tc.n, b.String(), tc.want)
		}
	}
	// A surrogate pair is two four-digit halves, not one eight-digit number.
	var b bytes.Buffer
	appendUTF16BE(&b, 0x1F600)
	if b.String() != "D83DDE00" {
		t.Errorf("appendUTF16BE(U+1F600) = %q, want D83DDE00", b.String())
	}
}
