package render

import (
	"fmt"
	"image"
	"image/color"
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
// # How much this oracle proves
//
// A reftest compares two documents rendered by the *same* engine, so in
// principle it can only see a fault that moves one of them and not the other. A
// uniform error — every border half as wide — shifts both sides equally and
// passes. That is the standing objection to reftests as a guard, and it was
// measured rather than assumed: against the earlier marks-based comparison, of
// eight planted layout defects this oracle caught two, and halved borders,
// broken inheritance, an ignored display:none and a missing min-height all went
// through.
//
// Resolving occlusion changed that more than expected. Re-measured against five
// planted defects, all five are now caught, halved borders among them — from
// 1569 clean passes down to 1029. The reason is that the reference documents in
// this suite are not built symmetrically with the tests: they draw the expected
// result directly, often with a plain image or a single filled box, so a fault in
// the engine's box model moves the test and leaves the reference standing. The
// marks-based comparison could not see that, because the pair disagreed about red
// rectangles before it ever got to the geometry.
//
// It remains a secondary guard. The planted-defect tests next door assert
// absolute numbers against the specification's own arithmetic, and they are what
// catches a fault that is uniform across both documents. What the reftests add is
// the class those cannot reach: an interaction between two features that no one
// thought to write a test for.
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
//
// Replaced elements are worth recording here, because that number went *down*
// before it went up. Loading images unmasked 172 passes that had been counting
// nothing: the suite's references draw their expected picture with an <img>, so
// before images loaded those documents painted only their instruction paragraph
// — and so did the tests beside them, whose own square was drawn with a display
// value this engine does not implement. Two documents agreeing on a paragraph is
// not evidence about layout, and the "paints nothing at all" guard could not see
// it because both painted the paragraph.
//
// So a seventh of the baseline at the time was measuring the absence of a
// feature. That is the same lesson the CDATA wrapper taught above and the
// occlusion-blind comparison taught below, and it is the one this file keeps
// having to relearn: a pass is only evidence if something could have made it
// fail.
//
// The text properties — text-decoration, text-transform, box-sizing, visibility,
// text-indent, letter-spacing and word-spacing — took it from 1899 to 1916, and
// the headline understates them: failures went from 2017 to 1995, and most of
// the tests that stopped failing moved into the *vacuous* bucket rather than
// this one, where they wait on something else that is still unimplemented. That
// is the ratchet working as designed — a test counts here only once nothing in
// either document is missing.
const wptCleanPassBaseline = 1981

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

// pageClip is the area a rendering is compared over.
//
// It stands in for the viewport a browser would have shown the reftest in: a
// mark outside it is not part of the picture, exactly as content scrolled off
// the page is not. Absolutely positioned boxes land outside it routinely.
func pageClip() Rect {
	sz := A4.Content()
	return Rect{W: sz.W, H: sz.H}
}

// renderForCompare lays a document out and returns its display list together
// with whether anything unsupported was reported.
//
// The ops are returned rather than a canonical string because the comparison
// resolves occlusion, and occlusion depends on paint order — see picture_test.go.
func renderForCompare(path string) (ops []Op, clean bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	src := cdataRe.ReplaceAllString(string(data), "$1")

	// The suite's documents refer to real files beside them — most of the
	// references draw their expected picture with
	// "<img src=support/blue15x15.png width=5 height=96>" — so the harness
	// roots a resolver at the directory the document was read from. That is the
	// caller opting in, which is the only way an image is ever loaded: the
	// engine's own default is to load nothing, and nothing here changes it.
	//
	// A reference that leaves that directory is refused by the resolver exactly
	// as it would be for any other caller, so a test document cannot read the
	// checkout.
	res, err := NewDirResolver(filepath.Dir(path))
	if err != nil {
		return nil, false, err
	}
	defer res.Close()

	built := Build(Input{HTML: src, Resources: res})

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

	ops = Paint(root)
	// A document that paints nothing cannot be evidence of anything. Two blank
	// pages match, which is the purest form of the vacuous pass §7.1 warns
	// about, and no amount of finding-counting detects it.
	if normaliseOps(ops) == "" {
		clean = false
	}
	return ops, clean, nil
}

// normaliseOps renders a display list as comparable text.
//
// This is no longer what decides whether two renderings agree — pictureEqual is,
// because it can see that one mark covers another and this cannot. What is left
// for it is the blank-page check above, where only the presence of marks matters
// and their order does not.
//
// It is worth recording why the sorted form was wrong as a comparison, since the
// reasoning that justified it sounded solid: sorting loses the ability to detect
// a wrong painting order, and painting order only matters where marks overlap,
// so a test that depended on it would be testing z-ordering rather than layout.
// The last step is the false one. Nearly every CSS 2.1 test paints a red box and
// covers it with a green one, and you pass by showing no red — which is an
// overlap, and which no comparison of unordered marks can ever satisfy, because
// the test has a red rectangle in it and the reference does not.
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
		case DrawImage:
			if v.Rect.Empty() {
				continue
			}
			// A picture of one colour, drawn over a rectangle, puts exactly the
			// same ink on exactly the same paper as a fill of that colour over
			// that rectangle. Saying so here is not a concession to make tests
			// pass — it is what makes this comparison a faithful proxy for the
			// thing a reftest actually asserts, which is that the two documents
			// *render* identically.
			//
			// It matters because of how the suite is written. Its references
			// draw their expected picture with a solid PNG —
			// "<img src=black96x96.png width=96 height=96>" — while the test
			// draws the same square with a background colour or a border. A
			// comparison that could not equate the two would rule that every
			// one of those pairs differs, which is a statement about the
			// comparison and not about the engine.
			//
			// The check is a real one and not an assumption: every pixel is
			// read, and an image with two colours in it, or any transparency at
			// all, is compared as an image. TestWPTOracleHasTeeth plants both.
			if c, ok := uniformColor(v.Image); ok {
				lines = append(lines, fmt.Sprintf("fill %s %s", rectKey(v.Rect), c))
				continue
			}
			// Otherwise the source rather than the pixels: two documents
			// drawing the same file draw the same key, and comparing decoded
			// images pixel by pixel would make this a rasterizer.
			lines = append(lines, fmt.Sprintf("image %s %s", v.Key, rectKey(v.Rect)))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// uniformColours memoizes the scan below, which is over every pixel of every
// image in four thousand documents rendered twice.
var uniformColours = map[image.Image]struct {
	colour style.RGBA
	ok     bool
}{}

// uniformColor reports whether every pixel of an image is the same opaque
// colour, and what that colour is.
//
// Opaque is required rather than merely uniform: a picture that is half
// transparent black does not put the same ink on the page as a fill of black,
// and treating the two as equal would hide exactly the kind of difference this
// comparison exists to find.
func uniformColor(img image.Image) (style.RGBA, bool) {
	if img == nil {
		return style.RGBA{}, false
	}
	if got, ok := uniformColours[img]; ok {
		return got.colour, got.ok
	}
	colour, ok := scanUniform(img)
	uniformColours[img] = struct {
		colour style.RGBA
		ok     bool
	}{colour, ok}
	return colour, ok
}

func scanUniform(img image.Image) (style.RGBA, bool) {
	b := img.Bounds()
	if b.Empty() {
		return style.RGBA{}, false
	}
	r0, g0, b0, a0 := img.At(b.Min.X, b.Min.Y).RGBA()
	if a0 != 0xFFFF {
		return style.RGBA{}, false
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if r != r0 || g != g0 || bl != b0 || a != a0 {
				return style.RGBA{}, false
			}
		}
	}
	// The same 0-255 scale style.ParseColor produces, so a fill written by a
	// stylesheet and a fill derived from a pixel are the same string.
	return style.RGBA{
		R: float64(r0 >> 8), G: float64(g0 >> 8), B: float64(b0 >> 8), A: 1,
	}, true
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

		same := pictureEqual(got, want, pageClip())
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
		// Painting the same opaque rectangle twice produces the same page. The
		// marks-based comparison called this a difference, which was over-strict
		// in the direction that costs real passes: a reference is free to reach
		// its picture with a different number of marks than the test.
		{"doubled", []Op{
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
		}, true},
		// Covering the mark completely with another colour is a different page,
		// and is the case the whole suite turns on.
		{"covered", []Op{
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: red},
			FillRect{Rect: Rect{u(10), u(20), u(100), u(50)}, Color: blue},
		}, false},
	}
	clip := pageClip()
	for _, tc := range cases {
		if got := pictureEqual(tc.ops, base, clip); got != tc.equal {
			t.Errorf("%s: the comparison said equal=%v, want %v", tc.name, got, tc.equal)
		}
	}

	// A difference far below a pixel is not a rendering difference, and treating
	// it as one would fail every test on arithmetic noise.
	near := []Op{FillRect{Rect: Rect{u(10.0001), u(20), u(100), u(50)}, Color: red}}
	if !pictureEqual(near, base, clip) {
		t.Error("a difference of a ten-thousandth of a pixel was treated as a difference")
	}

	// The uniform-image equivalence, which is the one place this comparison
	// says two different ops are the same mark. It has to be exactly as strict
	// as that claim: a solid opaque picture is a fill of its colour, and
	// anything else is not.
	fill4x4 := func(f func(x, y int) color.NRGBA) image.Image {
		img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				img.SetNRGBA(x, y, f(x, y))
			}
		}
		return img
	}
	solid := fill4x4(func(int, int) color.NRGBA { return color.NRGBA{R: 255, A: 255} })
	speckled := fill4x4(func(x, y int) color.NRGBA {
		if x == 3 && y == 3 {
			// One pixel of a different colour, which is a picture and not a
			// rectangle however uniform the rest of it is.
			return color.NRGBA{G: 255, A: 255}
		}
		return color.NRGBA{R: 255, A: 255}
	})
	// Half-transparent full red, which *premultiplies* to exactly the opaque
	// dark red below. That is the whole point of this one: a test using a
	// translucent colour that premultiplied to something else would be caught
	// by the colour comparison and would pass with the opacity check deleted,
	// which was found by planting exactly that.
	translucent := fill4x4(func(int, int) color.NRGBA { return color.NRGBA{R: 255, A: 128} })
	darkRed := style.RGBA{R: 128, A: 1}

	rect := Rect{u(10), u(20), u(100), u(50)}
	if !pictureEqual([]Op{DrawImage{Rect: rect, Image: solid, Key: "k"}}, base, clip) {
		t.Error("a solid red image over the same rectangle did not compare equal to a red fill")
	}
	if pictureEqual([]Op{DrawImage{Rect: rect, Image: speckled, Key: "k"}}, base, clip) {
		t.Error("an image with a pixel of another colour compared equal to a plain fill")
	}
	dark := []Op{FillRect{Rect: rect, Color: darkRed}}
	if pictureEqual([]Op{DrawImage{Rect: rect, Image: translucent, Key: "k"}}, dark, clip) {
		t.Error("a half-transparent image compared equal to the opaque fill it premultiplies to")
	}
	// And two different pictures at the same place are still different.
	if pictureEqual(
		[]Op{DrawImage{Rect: rect, Image: speckled, Key: "one"}},
		[]Op{DrawImage{Rect: rect, Image: speckled, Key: "two"}}, clip) {
		t.Error("two different image sources compared equal")
	}

	// Marks that do not overlap may be emitted in either order, since the two
	// documents of a reftest paint from different structures.
	a := []Op{
		FillRect{Rect: Rect{u(0), u(0), u(1), u(1)}, Color: red},
		FillRect{Rect: Rect{u(5), u(5), u(1), u(1)}, Color: blue},
	}
	b := []Op{a[1], a[0]}
	if !pictureEqual(a, b, clip) {
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
