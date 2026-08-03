// Command genbidi generates the Unicode bidirectional character properties from
// Unicode's own UnicodeData.txt, DerivedBidiClass.txt, BidiBrackets.txt and
// BidiMirroring.txt.
//
// Which way a character runs is a property of the character, not of the font or
// of the language: Hebrew alef is right-to-left wherever it appears, and a digit
// beside it is not. That is Unicode's Bidi_Class, and the bidirectional
// algorithm is written entirely in terms of it, so it is read from the UCD
// rather than guessed from a script or a code block.
//
// # Why two sources for one property
//
// UnicodeData.txt field 4 is the Bidi_Class of every *assigned* character, and
// it is the normative source. It is not the whole property: an unassigned code
// point inside the Hebrew or Arabic blocks is right-to-left by default, one that
// is ignorable or a non-character is boundary-neutral, and a character not yet
// in the standard still has to be laid out the way its neighbours will be once
// it is. UnicodeData.txt has no line for a code point nobody has assigned, so it
// cannot say any of that; DerivedBidiClass.txt can, and does.
//
// So the derived file supplies the table — its "@missing" block defaults
// underneath its own explicit lines — and UnicodeData.txt is read to check it:
// every assigned character must have the same class in both. A disagreement is a
// fatal error rather than a warning, because it means the files are from
// different versions of Unicode and the table would be a mixture of both.
//
// # Brackets and mirrors
//
// Rule N0 of UAX #9 resolves a bracket pair as a unit, so that "(‏עברית‏)" does
// not come out with its parentheses pointing the wrong way; it needs to know
// which characters are brackets and which close which, and that is
// BidiBrackets.txt. Rule L4 then draws the character that mirrors it, which is
// BidiMirroring.txt. Neither is derivable from the other: every bracket
// mirrors, but not everything that mirrors is a bracket.
//
//	go run ./cmd/genbidi <UnicodeData.txt> <DerivedBidiClass.txt> <BidiBrackets.txt> <BidiMirroring.txt> > fonts/bidiclass.go
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strconv"
	"strings"
)

// maxRune is one past the last code point, which is what the tables cover.
const maxRune = 0x110000

// classNames are the Bidi_Class values the algorithm names, in the order
// fonts/bidi.go declares its constants.
//
// The generator fails if the data does not use one of them. That is the check
// that matters here: the algorithm is a switch over these twenty-three values,
// and a value that disappeared or was renamed upstream would leave the branch
// that handles it silently unreachable — text that used to be laid out by a rule
// would quietly stop being, with nothing to notice it.
var classNames = []string{
	"L", "R", "AL", "EN", "ES", "ET", "AN", "CS", "NSM", "BN",
	"B", "S", "WS", "ON",
	"LRE", "RLE", "LRO", "RLO", "PDF",
	"LRI", "RLI", "FSI", "PDI",
}

// longNames maps the long Bidi_Class names, which is how the "@missing" lines
// spell them, to the short aliases the data lines use.
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
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: genbidi <UnicodeData.txt> <DerivedBidiClass.txt> <BidiBrackets.txt> <BidiMirroring.txt>")
		os.Exit(2)
	}
	assigned := readUnicodeData(os.Args[1])
	version, defaults, explicit := readDerived(os.Args[2])
	brackets := readBrackets(os.Args[3])
	mirrors := readMirroring(os.Args[4])

	// The block defaults first, then the derived file's own lines over them.
	// Reading them in the other order would let a block default overwrite a
	// character's stated class, which is backwards: the default is what applies
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

	// Now the cross-check. Every character UnicodeData.txt assigns must have the
	// class the derived file gives it: the two are generated from the same
	// database, so they agree or they are not the same version of it — and a
	// table mixing two versions is wrong in a way no test downstream would name.
	//
	// The check runs this way round rather than the other because the derived
	// file says strictly more. It lists the unassigned code points that default
	// to boundary-neutral by being ignorable or non-characters, and those are not
	// derivable from UnicodeData.txt at all, which has no line for a code point
	// nobody has assigned.
	for r, c := range assigned {
		if classes[r] != c {
			fmt.Fprintf(os.Stderr, "genbidi: U+%04X is %s in UnicodeData.txt and %s in DerivedBidiClass.txt;\n"+
				"the two files are not from the same version of Unicode\n", r, c, classes[r])
			os.Exit(1)
		}
	}

	// Every value the algorithm names must be in the data.
	present := map[string]bool{}
	for _, c := range classes {
		present[c] = true
	}
	for _, name := range classNames {
		if !present[name] {
			fmt.Fprintf(os.Stderr, "genbidi: no character has Bidi_Class %s;\n"+
				"the algorithm has a branch for it, so either the value was renamed\n"+
				"upstream or the wrong file was passed\n", name)
			os.Exit(1)
		}
	}

	index := map[string]int{}
	for i, name := range classNames {
		index[name] = i
	}

	// Collapse to ranges, and drop the ones that are the default. A class runs
	// in long blocks, and left-to-right is most of the code space — emitting it
	// would double the table to say what its absence already says.
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
// DerivedBidiClass.txt, BidiBrackets.txt and BidiMirroring.txt. DO NOT EDIT.

package fonts

// The bidirectional character properties. Unicode %s.
//
// %d ranges cover every character that is not plain left-to-right, which is the
// default and so is not listed: absence from the table is the answer for the
// great majority of the code space. The ranges include the block defaults for
// unassigned code points — a character not yet in the standard, written inside
// the Hebrew or Arabic blocks, still runs right to left — because a document
// may well contain one and it has to be laid out the way its neighbours are.

type bidiClassRange struct {
	lo, hi rune
	class  bidiClass
}

// bidiClassRanges maps a character to its Bidi_Class, sorted by code point.
var bidiClassRanges = [...]bidiClassRange{
`, version, len(ranges))
	for _, r := range ranges {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, bidi%s},\n", r.lo, r.hi, r.class)
	}
	fmt.Fprint(w, `}

// bidiBracket is one half of a bracket pair: the character that closes or opens
// it, and which of the two this is.
type bidiBracket struct {
	ch, paired rune
	open       bool
}

// bidiBrackets is every character with a Bidi_Paired_Bracket_Type of Open or
// Close, sorted by code point. Rule N0 resolves a pair as a unit, so it needs
// both halves and needs to know which is which.
var bidiBrackets = [...]bidiBracket{
`)
	for _, b := range brackets {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, %t},\n", b.ch, b.paired, b.open)
	}
	fmt.Fprint(w, `}

// bidiMirrorPair is a character and the one drawn in its place in a
// right-to-left run.
type bidiMirrorPair struct{ from, to rune }

// bidiMirrors is the Bidi_Mirroring_Glyph property, sorted by code point: rule
// L4. Only the characters that have a mirror are listed; everything else is
// drawn as it is written.
var bidiMirrors = [...]bidiMirrorPair{
`)
	for _, m := range mirrors {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X},\n", m.from, m.to)
	}
	fmt.Fprintln(w, "}")

	src, err := format.Source(w.Bytes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "genbidi:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(src); err != nil {
		fmt.Fprintln(os.Stderr, "genbidi:", err)
		os.Exit(1)
	}
}

// readUnicodeData parses field 4 of UnicodeData.txt, the Bidi_Class of every
// assigned character.
//
// The file states a long block of characters as a "First>"/"Last>" pair rather
// than one line each, so the ranges have to be expanded; a reader that took each
// line as one character would miss every CJK ideograph and every Hangul
// syllable.
func readUnicodeData(path string) map[rune]string {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	out := map[rune]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	pendingFirst := rune(-1)
	pendingClass := ""
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ";")
		if len(fields) < 5 {
			continue
		}
		cp, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil || cp >= maxRune {
			continue
		}
		class := strings.TrimSpace(fields[4])
		name := strings.TrimSpace(fields[1])
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return out
}

type classRange struct {
	lo, hi rune
	class  string
}

// readDerived parses DerivedBidiClass.txt: the Unicode version it declares, the
// "@missing" block defaults in the order they are stated, and the explicit
// per-character lines used to cross-check UnicodeData.txt.
func readDerived(path string) (string, []classRange, map[rune]string) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	version := "unknown"
	var defaults []classRange
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
				fmt.Fprintf(os.Stderr, "genbidi: unknown Bidi_Class %q in an @missing line\n", strings.TrimSpace(fields[1]))
				os.Exit(1)
			}
			defaults = append(defaults, classRange{lo, hi, short})
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(defaults) == 0 {
		fmt.Fprintln(os.Stderr, "genbidi: DerivedBidiClass.txt has no @missing lines;\n"+
			"the block defaults for unassigned code points are only stated there")
		os.Exit(1)
	}
	return version, defaults, explicit
}

type bracket struct {
	ch, paired rune
	open       bool
}

// readBrackets parses BidiBrackets.txt: the paired bracket and whether this
// character opens or closes it.
func readBrackets(path string) []bracket {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	var out []bracket
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
			out = append(out, bracket{rune(ch), rune(paired), true})
		case "c":
			sawClose = true
			out = append(out, bracket{rune(ch), rune(paired), false})
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !sawOpen || !sawClose {
		fmt.Fprintln(os.Stderr, "genbidi: BidiBrackets.txt named no opening or no closing brackets;\n"+
			"rule N0 needs both, so this is the wrong file or a changed format")
		os.Exit(1)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ch < out[j].ch })
	return out
}

type mirrorPair struct{ from, to rune }

// readMirroring parses BidiMirroring.txt: the character drawn in place of this
// one in a right-to-left run.
func readMirroring(path string) []mirrorPair {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	var out []mirrorPair
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) < 2 {
			continue
		}
		from, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil {
			continue
		}
		to, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 16, 32)
		if err != nil {
			continue
		}
		out = append(out, mirrorPair{rune(from), rune(to)})
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "genbidi: BidiMirroring.txt named no mirrored characters")
		os.Exit(1)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].from < out[j].from })
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
	if err != nil {
		return 0, 0, false
	}
	return rune(a), rune(b), true
}
