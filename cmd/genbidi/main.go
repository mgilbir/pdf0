// Command genbidi turns Unicode's own character database into the bidirectional
// property tables in package internal/bidi.
//
// Which way a character runs is a property of the character and not of the font,
// the language or the block it sits in: Hebrew alef runs right to left wherever
// it is written, and a digit beside it does not. That is Unicode's Bidi_Class,
// and UAX #9 is stated entirely in terms of it — so the table is read from the
// database rather than typed out or guessed from a code range.
//
// # Why two sources for one property
//
// UnicodeData.txt field 4 is the Bidi_Class of every *assigned* character, and
// it is the normative source. It is not the whole property. An unassigned code
// point inside the Hebrew or Arabic blocks is right-to-left by default, an
// ignorable or non-character one is boundary-neutral, and a document may well
// contain either — a character not yet in the standard still has to be laid out
// the way its neighbours will be. UnicodeData.txt has no line for a code point
// nobody has assigned, so it cannot say any of that; DerivedBidiClass.txt can,
// in its "@missing" block defaults.
//
// So the derived file supplies the table and UnicodeData.txt checks it: every
// assigned character must carry the same class in both. A disagreement means the
// two files are from different versions of Unicode, and a table mixing two
// versions is wrong in a way nothing downstream would name — so it is fatal
// rather than a warning.
//
// # Why brackets, and why not mirrors
//
// Rule N0 resolves a bracket pair as a unit, so that a parenthesised Hebrew
// phrase does not come out with its parentheses pointing outwards. It needs to
// know which characters are brackets and which closes which, and that is a
// property of its own: BidiBrackets.txt.
//
// Rule L4's mirroring — the character actually *drawn* in place of a bracket in
// a right-to-left run — is not generated, and BidiMirroring.txt is therefore not
// an input. The glyph for it can only be chosen once the run has been reversed,
// which is the shaper's work and not the layout engine's, so a table here would
// be four hundred lines nothing reads.
//
// Fetch the inputs with `make bidi-tables`, then:
//
//	go run ./cmd/genbidi -ucd testdata/unicode -out internal/bidi/tables.go
//
// The output is committed and the inputs are not, on the arrangement
// cmd/genhtmlentities and cmd/gencolors already use: the table is part of the
// source, and re-deriving it needs the network, so a checkout builds without one.
//
// github.com/mgilbir/forme has a sibling generator over the same files, because
// shaping needs the same property to decide which way a run of glyphs is drawn.
// ADR 0006 records why there are two.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// maxRune is one past the last code point, which is what the tables cover.
const maxRune = 0x110000

// classNames are the Bidi_Class values UAX #9 names, in the order package bidi
// declares its constants.
//
// The generator stops if the data uses a value that is not here, and stops again
// if any of these has no character at all. Both checks guard the same thing: the
// algorithm is a switch over these twenty-three values, and one that vanished or
// was renamed upstream would leave the branch handling it silently unreachable —
// text that used to be laid out by a rule would quietly stop being.
var classNames = []string{
	"L", "R", "AL", "EN", "ES", "ET", "AN", "CS", "NSM", "BN",
	"B", "S", "WS", "ON",
	"LRE", "RLE", "LRO", "RLO", "PDF",
	"LRI", "RLI", "FSI", "PDI",
}

// longNames maps the spelled-out Bidi_Class names, which is how the "@missing"
// lines write them, to the short aliases the data lines use.
var longNames = map[string]string{
	"Left_To_Right":           "L",
	"Right_To_Left":           "R",
	"Arabic_Letter":           "AL",
	"European_Number":         "EN",
	"European_Separator":      "ES",
	"European_Terminator":     "ET",
	"Arabic_Number":           "AN",
	"Common_Separator":        "CS",
	"Nonspacing_Mark":         "NSM",
	"Boundary_Neutral":        "BN",
	"Paragraph_Separator":     "B",
	"Segment_Separator":       "S",
	"White_Space":             "WS",
	"Other_Neutral":           "ON",
	"Left_To_Right_Embedding": "LRE",
	"Right_To_Left_Embedding": "RLE",
	"Left_To_Right_Override":  "LRO",
	"Right_To_Left_Override":  "RLO",
	"Pop_Directional_Format":  "PDF",
	"Left_To_Right_Isolate":   "LRI",
	"Right_To_Left_Isolate":   "RLI",
	"First_Strong_Isolate":    "FSI",
	"Pop_Directional_Isolate": "PDI",
}

func main() {
	ucd := flag.String("ucd", "testdata/unicode", "the directory the UCD files were fetched into")
	out := flag.String("out", "internal/bidi/tables.go", "the Go file to write")
	flag.Parse()

	assigned := readUnicodeData(filepath.Join(*ucd, "UnicodeData.txt"))
	version, defaults, explicit := readDerived(filepath.Join(*ucd, "extracted", "DerivedBidiClass.txt"))
	brackets := readBrackets(filepath.Join(*ucd, "BidiBrackets.txt"))

	// The block defaults first and the derived file's own lines over them.
	// Reading them the other way round would let a block default overwrite a
	// character's stated class, which is backwards: a default is what applies
	// where nothing else does.
	classes := make([]string, maxRune)
	for i := range classes {
		classes[i] = "L"
	}
	for _, d := range defaults {
		for r := d.lo; r <= d.hi && r < maxRune; r++ {
			classes[r] = d.class
		}
	}
	for r, c := range explicit {
		classes[r] = c
	}

	// The cross-check runs this way round rather than the other because the
	// derived file says strictly more: it lists the unassigned code points that
	// are boundary-neutral for being ignorable or non-characters, and those are
	// not derivable from UnicodeData.txt at all.
	for r, c := range assigned {
		if classes[r] != c {
			log.Fatalf("U+%04X is %s in UnicodeData.txt and %s in DerivedBidiClass.txt; "+
				"the two files are not from the same version of Unicode", r, c, classes[r])
		}
	}

	known := map[string]bool{}
	for _, name := range classNames {
		known[name] = true
	}
	present := map[string]bool{}
	for r, c := range classes {
		if !known[c] {
			log.Fatalf("U+%04X has Bidi_Class %q, which UAX #9 does not name; "+
				"either the property gained a value or the wrong file was passed", r, c)
		}
		present[c] = true
	}
	for _, name := range classNames {
		if !present[name] {
			log.Fatalf("no character has Bidi_Class %s; the algorithm has a branch for "+
				"it, so either the value was renamed upstream or the wrong file was passed", name)
		}
	}

	// Collapse to ranges, dropping the ones that are the default. A class runs in
	// long blocks, and left-to-right is most of the code space — emitting it
	// would treble the table to say what its absence already says.
	type rng struct {
		lo, hi rune
		class  string
	}
	var ranges []rng
	for r := rune(0); r < maxRune; r++ {
		if classes[r] == "L" {
			continue
		}
		if n := len(ranges); n > 0 && ranges[n-1].class == classes[r] && ranges[n-1].hi+1 == r {
			ranges[n-1].hi = r
			continue
		}
		ranges = append(ranges, rng{r, r, classes[r]})
	}

	w := &bytes.Buffer{}
	fmt.Fprintf(w, `// Code generated by cmd/genbidi from Unicode's UnicodeData.txt,
// DerivedBidiClass.txt and BidiBrackets.txt. DO NOT EDIT.
//
// The data is the Unicode Character Database, Unicode %s, used under the
// Unicode terms of use: https://www.unicode.org/terms_of_use.html

package bidi

// The bidirectional character properties, Unicode %s.
//
// %d ranges cover every character that is not plain left-to-right. That value is
// the default and so is not listed at all: absence from the table is the answer
// for the great majority of the code space, and for every character an ASCII
// document contains.
//
// The ranges include the block defaults for unassigned code points, which is why
// this is generated from DerivedBidiClass.txt rather than from UnicodeData.txt
// alone — a character not yet in the standard, written inside the Hebrew or
// Arabic blocks, still has to run the way its neighbours do.

// classRange is one run of code points sharing a Bidi_Class.
type classRange struct {
	lo, hi rune
	class  Class
}

// classRanges maps a character to its Bidi_Class, sorted by code point and
// searched by bisection.
var classRanges = [...]classRange{
`, version, version, len(ranges))
	for _, r := range ranges {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, %s},\n", r.lo, r.hi, r.class)
	}
	fmt.Fprint(w, `}

// bracket is one half of a bracket pair: the character that closes or opens it,
// and which of the two this one is.
type bracket struct {
	ch, paired rune
	open       bool
}

// brackets is every character whose Bidi_Paired_Bracket_Type is Open or Close,
// sorted by code point. Rule N0 resolves a pair as a unit, so it needs both
// halves and needs to know which is which.
var brackets = [...]bracket{
`)
	for _, b := range brackets {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, %t},\n", b.ch, b.paired, b.open)
	}
	fmt.Fprintln(w, "}")

	src, err := format.Source(w.Bytes())
	if err != nil {
		log.Fatalf("formatting the generated source: %v", err)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		log.Fatalf("writing %s: %v", *out, err)
	}
	log.Printf("wrote %s: %d class ranges, %d brackets (Unicode %s)",
		*out, len(ranges), len(brackets), version)
}

// readUnicodeData parses field 4 of UnicodeData.txt, the Bidi_Class of every
// assigned character.
//
// A long block of characters is stated as a "First>"/"Last>" pair rather than as
// one line each, so the ranges are expanded: a reader taking each line as one
// character would miss every CJK ideograph and every Hangul syllable, and the
// cross-check above would then be checking almost nothing.
func readUnicodeData(path string) map[rune]string {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("%v (run `make bidi-tables`)", err)
	}
	defer f.Close()

	out := map[rune]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	pendingFirst, pendingClass := rune(-1), ""
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ";")
		if len(fields) < 5 {
			continue
		}
		cp, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil || cp >= maxRune {
			continue
		}
		class, name := strings.TrimSpace(fields[4]), strings.TrimSpace(fields[1])
		switch {
		case strings.HasSuffix(name, ", First>"):
			pendingFirst, pendingClass = rune(cp), class
		case strings.HasSuffix(name, ", Last>") && pendingFirst >= 0:
			for r := pendingFirst; r <= rune(cp); r++ {
				out[r] = pendingClass
			}
			pendingFirst = -1
		default:
			out[rune(cp)] = class
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	if len(out) == 0 {
		log.Fatalf("%s named no characters", path)
	}
	return out
}

type classRangeIn struct {
	lo, hi rune
	class  string
}

// readDerived parses DerivedBidiClass.txt: the Unicode version it declares, the
// "@missing" block defaults in the order they are stated, and the explicit
// per-character lines.
func readDerived(path string) (string, []classRangeIn, map[rune]string) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("%v (run `make bidi-tables`)", err)
	}
	defer f.Close()

	version := ""
	var defaults []classRangeIn
	explicit := map[rune]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			// The first line names the file, and with it the version:
			// "# DerivedBidiClass-17.0.0.txt".
			first = false
			if i := strings.Index(line, "DerivedBidiClass-"); i >= 0 {
				if j := strings.Index(line[i:], ".txt"); j > 0 {
					version = line[i+len("DerivedBidiClass-") : i+j]
				}
			}
		}
		if i := strings.Index(line, "@missing:"); i >= 0 {
			// "# @missing: 0590..05FF; Right_To_Left"
			fields := strings.Split(line[i+len("@missing:"):], ";")
			if len(fields) < 2 {
				continue
			}
			lo, hi, ok := parseRange(strings.TrimSpace(fields[0]))
			if !ok {
				continue
			}
			short, ok := longNames[strings.TrimSpace(fields[1])]
			if !ok {
				log.Fatalf("unknown Bidi_Class %q in an @missing line", strings.TrimSpace(fields[1]))
			}
			defaults = append(defaults, classRangeIn{lo, hi, short})
			continue
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) < 2 {
			continue
		}
		lo, hi, ok := parseRange(strings.TrimSpace(fields[0]))
		if !ok {
			continue
		}
		class := strings.TrimSpace(fields[1])
		for r := lo; r <= hi && r < maxRune; r++ {
			explicit[r] = class
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	if version == "" {
		log.Fatalf("%s does not name its Unicode version on its first line", path)
	}
	if len(defaults) < 2 {
		log.Fatalf("%s has %d @missing lines; the block defaults for unassigned code "+
			"points are only stated there, and there are several", path, len(defaults))
	}
	return version, defaults, explicit
}

type bracketIn struct {
	ch, paired rune
	open       bool
}

// readBrackets parses BidiBrackets.txt: the paired bracket, and whether this
// character opens or closes it.
func readBrackets(path string) []bracketIn {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("%v (run `make bidi-tables`)", err)
	}
	defer f.Close()

	var out []bracketIn
	var sawOpen, sawClose bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) < 3 {
			continue
		}
		ch, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil {
			continue
		}
		paired, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 32)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(fields[2]) {
		case "o":
			sawOpen = true
			out = append(out, bracketIn{rune(ch), rune(paired), true})
		case "c":
			sawClose = true
			out = append(out, bracketIn{rune(ch), rune(paired), false})
		}
	}
	if err := sc.Err(); err != nil {
		log.Fatalf("reading %s: %v", path, err)
	}
	if !sawOpen || !sawClose {
		log.Fatalf("%s named no opening or no closing brackets; rule N0 needs both, "+
			"so this is the wrong file or the format has changed", path)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ch < out[j].ch })
	return out
}

// parseRange reads "0590..05FF" or "0590".
func parseRange(s string) (rune, rune, bool) {
	lo, hi := s, s
	if i := strings.Index(s, ".."); i >= 0 {
		lo, hi = s[:i], s[i+2:]
	}
	a, err := strconv.ParseUint(strings.TrimSpace(lo), 16, 32)
	if err != nil {
		return 0, 0, false
	}
	b, err := strconv.ParseUint(strings.TrimSpace(hi), 16, 32)
	if err != nil || b < a {
		return 0, 0, false
	}
	return rune(a), rune(b), true
}
