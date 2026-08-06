package render

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/style"
)

// The layout engine against an external oracle: the W3C Web Platform Tests.
//
// # Why these, and why they are an oracle
//
// A CSS reftest is a pair of documents with the assertion *these two render
// identically*. The pair and the claim come from the CSS Working Group, so a
// disagreement is evidence about pdf0 rather than a restatement of pdf0's own
// reading — which is the distinction ADR 0003 records this repository learning
// twice, the hard way.
//
// They are better than an ordinary expectation file for a reason worth stating:
// reftests are *constructed* so that the two documents reach the same rendering
// by different mechanisms. One uses a margin where the other uses a padding, one
// an inline-block where the other a float. So an engine bug usually moves one
// document and not the other, and shows up as a difference rather than as two
// matching wrong answers.
//
// No browser is involved. pdf0 renders both and compares.
//
// # What is compared, and why not the fragment tree
//
// §7.1 suggested comparing fragment trees. That turns out to be the wrong value,
// and for exactly the reason the reftests are good: the two documents have
// *different* structure on purpose, so their fragment trees differ even when
// they render identically. Comparing them would fail every test.
//
// The display list is the right one. It is what the two documents have in
// common — the marks on the page, with the structure that produced them gone —
// and it is a real value precisely so that something can attach here.
//
// # Vacuous passes, and the ratchet
//
// §7.1 names the trap: a reftest passes vacuously when the engine ignores a
// property in both documents. Two blank pages match. So a pass only counts
// towards the ratchet when *neither* document raised an unsupported finding —
// that is the companion signal §6.3 was designed to provide, and it is why the
// baseline below is of clean passes rather than of passes.
//
// That check was not enough on its own, and finding out why is the most useful
// thing this file has done so far. Planted layout defects — broken inheritance,
// display:none ignored, halved border widths — moved the clean-pass count by
// *nothing at all*. The reason was that the suite's .xht tests wrap their CSS in
// a CDATA section, which this engine handed to the CSS parser as a rule; the
// stylesheet then produced no declarations, so nothing could be reported
// unsupported, so every one of those tests looked clean while rendering two
// unstyled documents that matched trivially.
//
// The counting was working and it was counting nothing. Two things now guard it:
// the wrapper is stripped, and a document that paints nothing at all cannot
// count as a clean pass however quiet it was.
//
// # How much this oracle currently proves, which is less than it looks
//
// Worth stating plainly, because a suite of 1715 tests reads as more assurance
// than 39 clean passes provide.
//
// A reftest compares two documents rendered by the *same* engine, so it can only
// see a fault that moves one of them and not the other. A uniform error — every
// border half as wide, every percentage nine tenths of what it should be — shifts
// both sides equally and passes. That was measured rather than assumed: of eight
// planted layout defects, this oracle caught two. Broken inheritance, an ignored
// display:none, halved borders and a missing min-height all went through.
//
// So this is a real external check and it is not the primary one. The
// planted-defect tests next door are, and they catch the uniform faults precisely
// because they assert absolute numbers against the specification's own
// arithmetic. What the reftests add is the class those cannot reach: an
// interaction between two features that no one thought to write a test for.
//
// The ratchet's value grows with the engine. Every layout feature that lands
// moves tests out of the "something unsupported" bucket and into this one, and
// each one that arrives brings its sensitivity with it.

const wptEnv = "WPT_TESTS"

// wptCleanPassBaseline is the number of reftests that pass with nothing
// unsupported reported in either document.
//
// It is a ratchet: it may rise and must never be lowered to make a red test
// green. A drop means a layout regression, and the failing names are printed.
const wptCleanPassBaseline = 359

// linkRe finds the reference link that makes a document a reftest.
var linkRe = regexp.MustCompile(`(?i)<link\s+[^>]*rel\s*=\s*["']?(match|mismatch)["']?[^>]*>`)

// hrefRe pulls the href out of such a link.
var hrefRe = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)

// flagsRe finds the metadata that marks a test as needing something automation
// cannot give it.
var flagsRe = regexp.MustCompile(`(?i)<meta\s+name\s*=\s*["']?flags["']?\s+content\s*=\s*["']([^"']*)["']`)

func wptDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(wptEnv)
	if dir == "" {
		t.Skipf("set %s (or run `make test-wpt`) to check layout against the Web Platform Tests", wptEnv)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s=%s: %v", wptEnv, dir, err)
	}
	return dir
}

// reftest is one pair.
type reftest struct {
	name string
	test string // path to the test document
	ref  string // path to the reference
	// mismatch reverses the assertion: the two must render *differently*.
	mismatch bool
}

// findReftests walks the suite for pairs.
func findReftests(t *testing.T, root string) []reftest {
	t.Helper()
	var out []reftest

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext != ".html" && ext != ".xht" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := string(data)

		link := linkRe.FindStringSubmatch(src)
		if link == nil {
			return nil
		}
		href := hrefRe.FindStringSubmatch(link[0])
		if href == nil {
			return nil
		}
		// A reference that lives outside the sparse checkout is not a failure,
		// it is a file that was not fetched.
		ref := filepath.Join(filepath.Dir(path), href[1])
		if _, err := os.Stat(ref); err != nil {
			return nil
		}

		// Tests needing something automation cannot give are not run. "ahem"
		// needs a specific test font, and the rest need a person.
		if flags := flagsRe.FindStringSubmatch(src); flags != nil {
			for _, f := range strings.Fields(flags[1]) {
				switch strings.ToLower(f) {
				case "ahem", "animated", "interact", "paged", "userstyle", "font":
					return nil
				}
			}
		}

		name, _ := filepath.Rel(root, path)
		out = append(out, reftest{
			name: filepath.ToSlash(name), test: path, ref: ref,
			mismatch: strings.EqualFold(link[1], "mismatch"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// cdataRe matches the CDATA wrapper an XHTML document puts around its CSS.
//
// The suite's older tests are .xht, which a browser parses as XML — and XML
// strips a CDATA section before the CSS is ever seen. This engine reads HTML,
// where <style> is raw text and the wrapper would be handed to the CSS parser
// as though it were a rule.
//
// Stripping it here rather than in the engine is deliberate: the engine reads
// HTML and should not learn XML syntax to suit a test suite. This is the harness
// adapting the input, and it is the sort of adjustment that has to be visible.
//
// It was found by the vacuous-pass check failing to do its job — see the note on
// clean passes below.
var cdataRe = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)

// renderForCompare lays a document out and returns its normalised display list
// together with whether anything unsupported was reported.
func renderForCompare(path string) (list string, clean bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	src := cdataRe.ReplaceAllString(string(data), "$1")
	built := Build(Input{HTML: src})

	rec := NewRecorder(nil)
	root := Layout(built.Root, A4.Content(), nil, rec)

	clean = true
	for _, f := range built.Findings {
		if f.Unsupported() {
			clean = false
		}
	}
	for _, f := range rec.Findings() {
		if f.Unsupported() {
			clean = false
		}
	}

	out := normaliseOps(Paint(root))
	// A document that paints nothing cannot be evidence of anything. Two blank
	// pages match, which is the purest form of the vacuous pass §7.1 warns
	// about, and no amount of finding-counting detects it.
	if out == "" {
		clean = false
	}
	return out, clean, nil
}

// normaliseOps renders a display list as comparable text.
//
// The ops are sorted rather than compared in order, because the two documents
// of a reftest paint the same marks from different structures and so may emit
// them in a different sequence. Sorting loses the ability to detect a wrong
// *painting order*, which is a real fault — but painting order only matters
// where marks overlap, and a reftest that depended on it would be testing
// z-ordering rather than layout.
func normaliseOps(ops []Op) string {
	lines := make([]string, 0, len(ops))
	for _, op := range ops {
		switch v := op.(type) {
		case FillRect:
			if v.Rect.Empty() || v.Color.A == 0 {
				// A mark that paints nothing is not a difference. Two documents
				// may reach an empty box by different routes.
				continue
			}
			lines = append(lines, fmt.Sprintf("fill %s %s", rectKey(v.Rect), v.Color))
		case DrawText:
			if strings.TrimSpace(v.Text) == "" {
				// A space marks no paper. It is drawn for the sake of text
				// extraction, and two documents may legitimately have different
				// numbers of them between the same visible glyphs.
				continue
			}
			lines = append(lines, fmt.Sprintf("text %q at %s,%s size %s",
				v.Text, num(v.At.X), num(v.At.Y), num(v.Size)))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func rectKey(r Rect) string {
	return num(r.X) + "," + num(r.Y) + " " + num(r.W) + "x" + num(r.H)
}

// num renders a length to a hundredth of a pixel.
//
// Layout units are exact, so two identical renderings agree to the unit — but
// the two documents of a reftest arrive at their geometry by different
// arithmetic, and a rounding of a third of a pixel in one and not the other is
// not a rendering difference. A hundredth is far below anything visible and far
// above the noise.
func num(u style.Unit) string {
	return strconv.FormatFloat(float64(int(u.Px()*100+0.5))/100, 'f', -1, 64)
}

func TestWPTReftests(t *testing.T) {
	root := wptDir(t)
	tests := findReftests(t, root)
	if len(tests) == 0 {
		t.Fatalf("no reftests found under %s; is the sparse checkout set?", root)
	}

	var cleanPass, vacuousPass, fail, broke int
	var failed []string

	for _, rt := range tests {
		got, gotClean, err := renderForCompare(rt.test)
		if err != nil {
			broke++
			continue
		}
		want, wantClean, err := renderForCompare(rt.ref)
		if err != nil {
			broke++
			continue
		}

		same := got == want
		passed := same != rt.mismatch

		switch {
		case !passed:
			fail++
			if len(failed) < 20 {
				failed = append(failed, rt.name)
			}
		case gotClean && wantClean:
			cleanPass++
		default:
			// A pass where something was unsupported in either document. It may
			// be real and it may be two blank pages agreeing, and nothing here
			// can tell which — so it does not count.
			vacuousPass++
		}
	}

	t.Logf("%d reftests: %d passed cleanly, %d passed with something unsupported, "+
		"%d failed, %d could not be read",
		len(tests), cleanPass, vacuousPass, fail, broke)
	if len(failed) > 0 {
		t.Logf("first failures: %s", strings.Join(failed, ", "))
	}

	if cleanPass < wptCleanPassBaseline {
		t.Errorf("%d reftests pass cleanly, below the baseline of %d — this is a "+
			"layout regression, and the baseline is not to be lowered to make it green",
			cleanPass, wptCleanPassBaseline)
	}
	if cleanPass > wptCleanPassBaseline {
		t.Logf("the clean-pass baseline can be raised from %d to %d",
			wptCleanPassBaseline, cleanPass)
	}
}

// TestWPTOracleHasTeeth is the check on the check, on the model of
// TestArlingtonOracleHasTeeth and TestCSSOracleHasTeeth.
//
// An oracle whose comparison accepts everything is worse than no oracle, because
// it reads as coverage. This plants the differences a layout fault produces — a
// box in the wrong place, of the wrong size, of the wrong colour, and text at
// the wrong position — and requires the comparison to see each.
func TestWPTOracleHasTeeth(t *testing.T) {
	red := style.RGBA{R: 255, A: 1}
	blue := style.RGBA{B: 255, A: 1}
	u := func(v float64) style.Unit { r, _ := style.FromPx(v); return r }

	base := []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red}}

	cases := []struct {
		name  string
		ops   []Op
		equal bool
	}{
		{"identical", []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red}}, true},
		{"moved", []Op{FillRect{Rect: Rect{u(11), u(20), u(100), u(50)}, Color: red}}, false},
		{"resized", []Op{FillRect{Rect: Rect{u(10), u(20), u(101), u(50)}, Color: red}}, false},
		{"recoloured", []Op{FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: blue}}, false},
		{"missing", nil, false},
		{"doubled", []Op{
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
		}, false},
	}
	want := normaliseOps(base)
	for _, tc := range cases {
		if got := normaliseOps(tc.ops) == want; got != tc.equal {
			t.Errorf("%s: the comparison said equal=%v, want %v", tc.name, got, tc.equal)
		}
	}

	// A difference below a hundredth of a pixel is not a rendering difference,
	// and treating it as one would fail every test on arithmetic noise.
	near := []Op{FillRect{Rect: Rect{u(10.0001), u(20), u(100), u(50)}, Color: red}}
	if normaliseOps(near) != want {
		t.Error("a difference of a ten-thousandth of a pixel was treated as a difference")
	}

	// And the sort really does make order irrelevant, since the two documents
	// of a reftest paint from different structures.
	a := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(1), u(1)}, Color: red},
		FillRect{Rect: Rect{u(5), u(5), u(1), u(1)}, Color: blue},
	}
	b := []Op{a[1], a[0]}
	if normaliseOps(a) != normaliseOps(b) {
		t.Error("the same marks in a different order compared unequal")
	}
}

// TestWPTFindsRealPairs pins that the walker recognises reftests, so a run
// reporting zero failures because it found zero tests fails here instead.
func TestWPTFindsRealPairs(t *testing.T) {
	root := wptDir(t)
	tests := findReftests(t, root)
	if len(tests) < 50 {
		t.Errorf("found %d reftests; the sparse checkout should hold hundreds", len(tests))
	}
	for _, rt := range tests[:min(5, len(tests))] {
		if rt.test == "" || rt.ref == "" {
			t.Errorf("%s has an empty side", rt.name)
		}
		if rt.test == rt.ref {
			t.Errorf("%s compares a document with itself", rt.name)
		}
	}
}
