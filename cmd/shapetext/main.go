// Command shapetext shapes lines of text with a font and prints what it got.
//
// It exists so that something outside this module can ask this module what it
// does with a piece of text — which is what the differential fuzzer in
// testdata/harfbuzz needs, and what nothing else could do: the shaping API is a
// Go API, and HarfBuzz is reached through Python.
//
// The output is the format testdata/harfbuzz/shape.py writes, so the two can be
// compared line for line:
//
//	<glyph>,<advance>[,<dx>,<dy>] <glyph>,<advance> ...
//
// with the offsets left off where they are zero. Advances and offsets are in
// font units, which is what HarfBuzz reports; this package works in thousandths
// of an em, so the conversion happens here rather than in the comparison.
//
//	go run ./cmd/shapetext <font.ttf> < lines.txt
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/mgilbir/pdf0/fonts"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: shapetext <font.ttf> < lines.txt")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	face, err := fonts.Load(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	upm := face.UnitsPerEm()

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	for in.Scan() {
		line := in.Text()
		if line == "" {
			continue
		}
		glyphs, _ := face.ShapeGlyphs(line)
		var parts []string
		for _, g := range glyphs {
			adv := units(g.XAdvance, upm)
			dx, dy := units(g.XOffset, upm), units(g.YOffset, upm)
			if dx != 0 || dy != 0 {
				parts = append(parts, fmt.Sprintf("%d,%d,%d,%d", g.GID, adv, dx, dy))
				continue
			}
			parts = append(parts, fmt.Sprintf("%d,%d", g.GID, adv))
		}
		fmt.Fprintln(out, strings.Join(parts, " "))
	}
	if err := in.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// units converts thousandths of an em back to the font's own units, which is
// what the other side reports.
//
// It rounds rather than truncating, and rounds half away from zero, because a
// value that came *from* font units and was scaled by 1000/upm has to land back
// on the integer it started at. Truncation loses it whenever the division was
// not exact.
func units(v float64, upm int) int {
	scaled := v * float64(upm) / 1000
	if scaled < 0 {
		return -int(-scaled + 0.5)
	}
	return int(scaled + 0.5)
}
