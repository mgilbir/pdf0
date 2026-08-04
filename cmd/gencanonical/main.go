// Command gencanonical generates the canonical equivalence data a shaper needs
// from Unicode's own UnicodeData.txt and CompositionExclusions.txt.
//
// Unicode writes the same text in more than one way. "é" is one character or
// two, and a nukta and a virama on the same consonant may be written in either
// order; the standard says all of these mean the same thing. A font does not:
// its rules are written against one spelling, and text written the other way
// misses them — a letter drawn as .notdef where the font could have drawn it, or
// a conjunct that quietly does not form. So a shaper normalises before it asks
// the font anything, and to do that it needs three things Unicode publishes:
//
//   - the canonical combining class, which is what "canonical order" orders by;
//   - the canonical decomposition of each character, one step at a time;
//   - which of those decompositions may be composed back, which is not simply
//     the reverse — some are excluded, and this file works out which.
//
// It also needs the general category, to know which characters are combining
// marks: a cluster is a base and the marks that follow it, and that boundary is
// by category rather than by combining class (a Devanagari vowel sign is a
// spacing mark whose combining class is zero).
//
//	go run ./cmd/gencanonical <UnicodeData.txt> <CompositionExclusions.txt> > fonts/canonical.go
//
// # Which compositions are excluded
//
// CompositionExclusions.txt lists two of the four sources of the standard's
// Full_Composition_Exclusion property: the script-specific exclusions, and the
// characters added after a composition version froze. The other two are derived
// here, from UnicodeData.txt, because they are properties of the decomposition
// itself:
//
//   - a singleton decomposition, one character to one character, has no pair to
//     compose from, so it never enters the table at all;
//   - a non-starter decomposition — one whose first character has a non-zero
//     combining class, or whose own character does — is excluded, because
//     composing it would produce a character that canonical ordering may then
//     move, and the two operations would not commute.
//
// Deriving them rather than reading a third file keeps the generator to the two
// files the data actually comes from, and the derivation is checked: every code
// point the exclusions file names must have a canonical decomposition here, or
// the two files are not from the same version of Unicode.
//
// # Hangul
//
// Hangul is not in the tables. Its decomposition and composition are arithmetic
// — the standard states them as formulas over the syllable and jamo blocks
// rather than as data — and fonts/normalize.go computes them. What this checks
// is that the blocks those formulas assume are still where they were.
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

// maxRune is one past the last code point.
const maxRune = 0x110000

// The Hangul blocks fonts/normalize.go computes over. They are checked rather
// than read: the formulas are written in terms of these constants, so a block
// that moved would leave the code computing over the wrong characters with
// nothing to notice it.
const (
	hangulSBase  = 0xAC00
	hangulSCount = 19 * 21 * 28
	hangulLBase  = 0x1100
	hangulLCount = 19
	hangulVBase  = 0x1161
	hangulVCount = 21
	hangulTBase  = 0x11A7
	hangulTCount = 28
)

// reorderedClasses are the combining classes fonts/normalize.go names in its
// reordering table, which permutes some of them so that marks come out in the
// order a font draws them rather than the order Unicode numbers them.
//
// The generator fails if the data no longer gives any character one of these.
// That is the check that matters here, and it is the same one genbidi makes: an
// entry for a class nothing has is a rule that can never fire, and a class
// renamed or retired upstream would leave it silently dead — text that used to
// be reordered would quietly stop being.
var reorderedClasses = []int{
	7, 9, // nukta, virama
	10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, // Hebrew
	27, 28, 29, 30, 31, 32, 33, 34, 35, // Arabic
	36,     // Syriac
	84, 91, // Telugu length marks
	103, 107, // Thai
	118, 122, // Lao
	129, 130, 132, // Tibetan
}

type charData struct {
	ccc      int
	mark     bool
	decomp   []rune // canonical only, empty when there is none
	assigned bool
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gencanonical <UnicodeData.txt> <CompositionExclusions.txt>")
		os.Exit(2)
	}
	chars := readUnicodeData(os.Args[1])
	version, excluded := readExclusions(os.Args[2])

	checkHangul(chars)

	// Every class the reordering names must still be somewhere in the data.
	present := map[int]bool{}
	for _, c := range chars {
		present[c.ccc] = true
	}
	for _, ccc := range reorderedClasses {
		if !present[ccc] {
			fmt.Fprintf(os.Stderr, "gencanonical: no character has combining class %d;\n"+
				"fonts/normalize.go reorders it, so either the class was retired upstream\n"+
				"or the wrong file was passed\n", ccc)
			os.Exit(1)
		}
	}

	// The cross-file check: the exclusions name characters by their
	// decomposition, so every one of them must have a canonical decomposition
	// here. A disagreement means the two files are from different versions of
	// Unicode, and a table mixing two versions is wrong in a way no test
	// downstream would name.
	for _, r := range excluded {
		if len(chars[r].decomp) == 0 {
			fmt.Fprintf(os.Stderr, "gencanonical: U+%04X is a composition exclusion but has no canonical\n"+
				"decomposition in UnicodeData.txt; the two files are not from the same\n"+
				"version of Unicode\n", r)
			os.Exit(1)
		}
	}

	classes := collapseClasses(chars)
	decomps := collectDecompositions(chars)
	comps := collectCompositions(chars, excluded)

	if len(classes) == 0 || len(decomps) == 0 || len(comps) == 0 {
		fmt.Fprintln(os.Stderr, "gencanonical: one of the three tables came out empty, which cannot be right")
		os.Exit(1)
	}

	w := &bytes.Buffer{}
	fmt.Fprintf(w, `// Code generated by cmd/gencanonical from Unicode's UnicodeData.txt and
// CompositionExclusions.txt. DO NOT EDIT.

package fonts

// Canonical equivalence: what makes two spellings of the same text the same.
// Unicode %s.
//
// %d ranges carry the two properties a cluster is read by — the canonical
// combining class, and whether the character is a combining mark. A character no
// range names is an unmarked starter, which is the great majority of the code
// space, so absence from the table is the answer for it.
//
// %d canonical decompositions, one step at a time as Unicode states them, and
// %d compositions, which are the decompositions that may be put back together:
// the singletons and the excluded ones are not here. Hangul is in neither, being
// arithmetic rather than data — see fonts/normalize.go.

// charClass is a run of code points sharing a combining class and a category.
type charClass struct {
	lo, hi rune
	ccc    uint8
	mark   bool
}

// charClasses is sorted by code point, so a lookup can binary-search.
var charClasses = [...]charClass{
`, version, len(classes), len(decomps), len(comps))
	for _, c := range classes {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, %d, %t},\n", c.lo, c.hi, c.ccc, c.mark)
	}
	fmt.Fprint(w, `}

// canonicalDecomposition is one character and what it is written as: a pair, or
// a single character with a zero second.
type canonicalDecomposition struct{ r, a, b rune }

// canonicalDecompositions is sorted by code point.
var canonicalDecompositions = [...]canonicalDecomposition{
`)
	for _, d := range decomps {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, 0x%04X},\n", d.r, d.a, d.b)
	}
	fmt.Fprint(w, `}

// canonicalComposition is a pair and the character it composes to.
type canonicalComposition struct{ a, b, ab rune }

// canonicalCompositions is sorted by the pair, so a lookup can binary-search.
var canonicalCompositions = [...]canonicalComposition{
`)
	for _, c := range comps {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, 0x%04X},\n", c.a, c.b, c.ab)
	}
	fmt.Fprintln(w, "}")

	src, err := format.Source(w.Bytes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "gencanonical:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(src); err != nil {
		fmt.Fprintln(os.Stderr, "gencanonical:", err)
		os.Exit(1)
	}
}

// checkHangul verifies the blocks fonts/normalize.go computes Hangul over.
//
// The syllables must be assigned across the whole range and carry no
// decomposition of their own — the standard states theirs as arithmetic — and
// the three jamo blocks must be assigned throughout.
func checkHangul(chars map[rune]charData) {
	fail := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "gencanonical: "+format+"\n"+
			"fonts/normalize.go computes Hangul from these blocks, so it would now\n"+
			"be computing over the wrong characters\n", args...)
		os.Exit(1)
	}
	for _, b := range []struct {
		name        string
		base, count rune
	}{
		{"syllable", hangulSBase, hangulSCount},
		{"leading jamo", hangulLBase, hangulLCount},
		{"vowel jamo", hangulVBase, hangulVCount},
		// The trailing jamo block starts one above its base: the base is the
		// value a syllable with no trailing consonant encodes.
		{"trailing jamo", hangulTBase + 1, hangulTCount - 1},
	} {
		for r := b.base; r < b.base+b.count; r++ {
			if !chars[r].assigned {
				fail("U+%04X is unassigned, inside the Hangul %s block", r, b.name)
			}
		}
	}
	for r := rune(hangulSBase); r < hangulSBase+hangulSCount; r++ {
		if len(chars[r].decomp) != 0 {
			fail("U+%04X, a Hangul syllable, has a canonical decomposition in the file", r)
		}
	}
}

type classRange struct {
	lo, hi rune
	ccc    int
	mark   bool
}

// collapseClasses turns the per-character properties into runs, dropping the
// ones that are the default: an unmarked starter is what absence from the table
// means, and emitting those would be most of the code space.
func collapseClasses(chars map[rune]charData) []classRange {
	var out []classRange
	for r := rune(0); r < maxRune; r++ {
		c := chars[r]
		if c.ccc == 0 && !c.mark {
			continue
		}
		if n := len(out); n > 0 && out[n-1].hi+1 == r && out[n-1].ccc == c.ccc && out[n-1].mark == c.mark {
			out[n-1].hi = r
			continue
		}
		out = append(out, classRange{r, r, c.ccc, c.mark})
	}
	return out
}

type decomposition struct{ r, a, b rune }

// collectDecompositions takes the canonical decompositions as Unicode states
// them: one step, and never more than two characters.
//
// A canonical decomposition of more than two characters does not exist in the
// standard — the standard guarantees it — and a file that had one would be a
// file this does not understand, so it is a failure rather than a truncation.
func collectDecompositions(chars map[rune]charData) []decomposition {
	var out []decomposition
	for r, c := range chars {
		switch len(c.decomp) {
		case 0:
			continue
		case 1:
			out = append(out, decomposition{r, c.decomp[0], 0})
		case 2:
			out = append(out, decomposition{r, c.decomp[0], c.decomp[1]})
		default:
			fmt.Fprintf(os.Stderr, "gencanonical: U+%04X has a canonical decomposition of %d characters;\n"+
				"the standard allows at most two, so this is a file this does not understand\n",
				r, len(c.decomp))
			os.Exit(1)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].r < out[j].r })
	return out
}

type composition struct{ a, b, ab rune }

// collectCompositions inverts the two-character decompositions, less the ones
// the standard excludes from composition.
func collectCompositions(chars map[rune]charData, excluded []rune) []composition {
	isExcluded := map[rune]bool{}
	for _, r := range excluded {
		isExcluded[r] = true
	}
	var out []composition
	for r, c := range chars {
		if len(c.decomp) != 2 || isExcluded[r] {
			continue
		}
		// A non-starter decomposition: composing it would produce a character
		// canonical ordering may then move, so the two would not commute.
		if c.ccc != 0 || chars[c.decomp[0]].ccc != 0 {
			continue
		}
		out = append(out, composition{c.decomp[0], c.decomp[1], r})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].a != out[j].a {
			return out[i].a < out[j].a
		}
		return out[i].b < out[j].b
	})
	// Two characters may not compose to two different ones: the composition
	// would then depend on which entry a lookup found first.
	for i := 1; i < len(out); i++ {
		if out[i].a == out[i-1].a && out[i].b == out[i-1].b {
			fmt.Fprintf(os.Stderr, "gencanonical: U+%04X and U+%04X compose to both U+%04X and U+%04X\n",
				out[i].a, out[i].b, out[i-1].ab, out[i].ab)
			os.Exit(1)
		}
	}
	return out
}

// readUnicodeData parses the fields normalisation needs: the general category,
// the canonical combining class, and the canonical decomposition.
//
// A long block of characters is stated as a "First>"/"Last>" pair rather than one
// line each, so the ranges have to be expanded; a reader that took each line as
// one character would miss every CJK ideograph and every Hangul syllable.
func readUnicodeData(path string) map[rune]charData {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	out := map[rune]charData{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	pendingFirst := rune(-1)
	var pending charData
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ";")
		if len(fields) < 6 {
			continue
		}
		cp, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 16, 32)
		if err != nil || cp >= maxRune {
			continue
		}
		ccc, err := strconv.Atoi(strings.TrimSpace(fields[3]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "gencanonical: U+%04X has no combining class\n", cp)
			os.Exit(1)
		}
		c := charData{ccc: ccc, assigned: true}
		switch strings.TrimSpace(fields[2]) {
		case "Mn", "Mc", "Me":
			c.mark = true
		}
		// A decomposition beginning with a tag in angle brackets is a
		// compatibility one, which says how a character may be *approximated*
		// rather than what it is written as. Only the canonical ones are wanted:
		// a compatibility decomposition changes what the text says.
		if d := strings.TrimSpace(fields[5]); d != "" && !strings.HasPrefix(d, "<") {
			for _, s := range strings.Fields(d) {
				v, err := strconv.ParseUint(s, 16, 32)
				if err != nil {
					fmt.Fprintf(os.Stderr, "gencanonical: U+%04X has an unreadable decomposition %q\n", cp, d)
					os.Exit(1)
				}
				c.decomp = append(c.decomp, rune(v))
			}
		}
		name := strings.TrimSpace(fields[1])
		switch {
		case strings.HasSuffix(name, ", First>"):
			pendingFirst, pending = rune(cp), c
		case strings.HasSuffix(name, ", Last>") && pendingFirst >= 0:
			for r := pendingFirst; r <= rune(cp); r++ {
				out[r] = pending
			}
			pendingFirst = -1
		default:
			out[rune(cp)] = c
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "gencanonical: UnicodeData.txt named no characters")
		os.Exit(1)
	}
	return out
}

// readExclusions parses CompositionExclusions.txt: the Unicode version it
// declares, and the code points it lists.
func readExclusions(path string) (string, []rune) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	version := "unknown"
	var out []rune
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			// The first line names the file, and with it the version:
			// "# CompositionExclusions-17.0.0.txt".
			first = false
			if i := strings.Index(line, "CompositionExclusions-"); i >= 0 {
				if j := strings.Index(line[i:], ".txt"); j > 0 {
					version = line[i+len("CompositionExclusions-") : i+j]
				}
			}
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := strconv.ParseUint(strings.Fields(line)[0], 16, 32)
		if err != nil || v >= maxRune {
			continue
		}
		out = append(out, rune(v))
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if version == "unknown" {
		fmt.Fprintln(os.Stderr, "gencanonical: CompositionExclusions.txt does not name its version in the first line")
		os.Exit(1)
	}
	if len(out) == 0 {
		fmt.Fprintln(os.Stderr, "gencanonical: CompositionExclusions.txt listed no characters")
		os.Exit(1)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return version, out
}
