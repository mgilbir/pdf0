package render

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/pdf0/css"
	"github.com/mgilbir/pdf0/fonts"
)

// The checks on @font-face, which is the one feature in this package that hands
// attacker-controlled bytes to a parser of attacker-controlled structure.
//
// Every cap in fontface.go has a test here that fires it, because a cap nobody
// has watched trip is one nobody knows works — and the caps are the whole of the
// security argument for the feature. They are variables so that these tests can
// lower them and watch the boundary rather than allocating their way to it.

// realFont is a font program these tests can hand the engine.
//
// It is forme's bundled Noto Sans, which is already a dependency of this
// repository through pdf0/fonts, so nothing new is vendored and no corpus is
// needed — these tests run in a bare checkout. It is loaded once because it is
// two megabytes and parsing it per test would be paid for a dozen times.
func realFont() []byte {
	realFontOnce.Do(func() { realFontData = notosans.Regular() })
	return realFontData
}

var (
	realFontOnce sync.Once
	realFontData []byte
)

// fileResolver serves bytes by name and records what it was asked for, so a
// test can prove a refusal happened *before* the resolver rather than inside it.
type fileResolver struct {
	files map[string][]byte
	asked []string
}

func (f *fileResolver) Resolve(ref string) ([]byte, error) {
	f.asked = append(f.asked, ref)
	data, ok := f.files[ref]
	if !ok {
		return nil, fmt.Errorf("no such file %q", ref)
	}
	return data, nil
}

// docWithFontFace wraps a stylesheet in the smallest document that uses it.
func docWithFontFace(sheet string) string {
	return "<style>" + sheet + "</style><p id=p style=\"font-family: Trial\">x</p>"
}

// TestFontFaceLoadsFromTheDocument is the feature: a document names a font, the
// font arrives through the resolver, and the family it declared resolves to it.
//
// The face is checked by measuring rather than by being non-nil, because a
// fallback is also non-nil. Noto Sans and Helvetica do not measure the same
// string to the same width, which is what makes the assertion about *which*
// face arrived.
func TestFontFaceLoadsFromTheDocument(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	built := Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
		Resources: res,
	})

	face, ok := built.Fonts.Face("Trial", false, false)
	if !ok || face == nil {
		t.Fatalf("the document's own font-family did not resolve; findings: %v", built.Findings)
	}
	if _, standard := StandardFonts().Face("Trial", false, false); standard {
		t.Fatal(`the standard set answers for "Trial", so this proves nothing`)
	}
	want, err := loadRealFace()
	if err != nil {
		t.Fatalf("loading the reference face: %v", err)
	}
	if got, wantW := face.Measure("Hamburgefonstiv", 20), want.Measure("Hamburgefonstiv", 20); got != wantW {
		t.Errorf("the loaded face measures %v where the font measures %v", got, wantW)
	}
	// And a family the document did not declare still goes to the caller's set.
	if _, ok := built.Fonts.Face("Helvetica", false, false); !ok {
		t.Error("wrapping the caller's set lost the families it had")
	}
	for _, f := range built.Findings {
		if f.Unsupported() {
			t.Errorf("loading a plain @font-face reported %s: %s", f.Rule, f.Message)
		}
	}
}

// loadRealFace parses the reference font a second time, so that the measurement
// above compares the face the engine loaded against the file rather than
// against itself.
func loadRealFace() (*fonts.Face, error) { return fonts.Load(realFont()) }

// TestFontFaceIsNoLongerAnUnsupportedAtRule is the report half, and it is what
// the WPT harness's stripped link was standing in for.
//
// The cascade reports every at-rule it does not apply. @font-face is applied
// now, so it must not be reported — while every other at-rule still is, because
// a change that silenced all of them would look identical in the one document
// that only has this one.
func TestFontFaceIsNoLongerAnUnsupportedAtRule(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	built := Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
		Resources: res,
	})
	for _, f := range built.Findings {
		if f.Rule == RuleUnsupportedAtRule {
			t.Errorf("@font-face is still reported as an at-rule that is not applied: %s", f.Message)
		}
	}

	other := Build(Input{HTML: `<style>@media print { p { color: red } }</style><p>x</p>`})
	found := false
	for _, f := range other.Findings {
		if f.Rule == RuleUnsupportedAtRule {
			found = true
		}
	}
	if !found {
		t.Error("@media stopped being reported too, so the change was not specific to @font-face")
	}
}

// TestFontFaceSrcIsTriedInOrder pins the fallback chain: an entry that cannot be
// loaded is the next entry's turn, not a failure of the rule.
func TestFontFaceSrcIsTriedInOrder(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"second.ttf": realFont()}}
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial;
			src: url(missing.ttf) format("truetype"), url(second.ttf); }`),
		Resources: res,
	})
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Fatalf("the second src was not tried; findings: %v", built.Findings)
	}
	if len(res.asked) != 2 || res.asked[0] != "missing.ttf" || res.asked[1] != "second.ttf" {
		t.Errorf("the resolver was asked for %v, want missing.ttf then second.ttf", res.asked)
	}
	// A rule that ended up with a font reports nothing: three alternatives with
	// two unused is what an author writes on purpose.
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked || f.Rule == RuleFontUndecodable {
			t.Errorf("a src that fell through to a working entry reported %s: %s", f.Rule, f.Message)
		}
	}
}

// TestFontFaceLocalIsNotAFailure pins the other half of the chain. local() names
// a face the reader may have; not having it is the ordinary case, and reporting
// it would put a finding on every well-written stylesheet on the web.
func TestFontFaceLocalIsNotAFailure(t *testing.T) {
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial;
			src: local("Nonesuch Sans"), local(Helvetica); }`),
	})
	face, ok := built.Fonts.Face("Trial", false, false)
	if !ok {
		t.Fatalf("local(Helvetica) did not resolve against the standard set; findings: %v",
			built.Findings)
	}
	std, _ := StandardFonts().Face("Helvetica", false, false)
	if face.Measure("Hamburgefonstiv", 20) != std.Measure("Hamburgefonstiv", 20) {
		t.Error("local(Helvetica) resolved to something that is not Helvetica")
	}
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked || f.Rule == RuleFontUndecodable {
			t.Errorf("a local() the set does not have reported %s: %s", f.Rule, f.Message)
		}
	}
	// No resolver was configured and none was needed: local() reads nothing.
	if _, ok := built.Fonts.(*documentFonts); !ok {
		t.Errorf("the document's set is %T, want the document's own faces", built.Fonts)
	}
}

// TestFontFaceReportsWhenNothingLoads is the finding that keeps a missing font
// from being silent: the family is simply not there afterwards, and a page set
// in something else looks like a page.
func TestFontFaceReportsWhenNothingLoads(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{}}
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial;
			src: local(Nonesuch), url(missing.ttf); }`),
		Resources: res,
	})
	if _, ok := built.Fonts.(*documentFonts); ok {
		t.Error("a rule that loaded nothing still produced a document font set")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "loaded no font")
	fired[RuleResourceBlocked] = true
}

// TestFontFaceNeedsAResolver is the deny-by-default guarantee for fonts. It is
// the same promise resource.go makes for images and stylesheets, checked
// separately because a second loading path is always the one missing a check.
func TestFontFaceNeedsAResolver(t *testing.T) {
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
	})
	if _, ok := built.Fonts.Face("Trial", false, false); ok {
		t.Error("a font was loaded with no resolver configured")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, ErrNoResolver.Error())
	fired[RuleResourceBlocked] = true
}

// TestFontFaceRefusesSchemes is the no-network guarantee. The resolver here
// would serve the font if it were ever asked, so a scheme that got through would
// show up in what it was asked for as well as in the face.
func TestFontFaceRefusesSchemes(t *testing.T) {
	for _, ref := range []string{
		"http://example.invalid/x.ttf",
		"https://example.invalid/x.ttf",
		"file:///etc/x.ttf",
		"ftp://example.invalid/x.ttf",
		"c:/windows/fonts/x.ttf",
	} {
		res := &fileResolver{files: map[string][]byte{ref: realFont()}}
		built := Build(Input{
			HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url("` + ref + `"); }`),
			Resources: res,
		})
		if _, ok := built.Fonts.Face("Trial", false, false); ok {
			t.Errorf("%s was fetched", ref)
		}
		if len(res.asked) != 0 {
			t.Errorf("%s reached the resolver as %v; the refusal must happen first", ref, res.asked)
		}
		requireFinding(t, built.Findings, RuleResourceBlocked, "resolves no URLs")
	}
	fired[RuleResourceBlocked] = true
}

// TestFontFaceRefusesEscapingTheResolver pins that a font goes through
// DirResolver's containment like everything else. The engine's policy lives in
// one place and this is the check that fonts did not get a second one.
func TestFontFaceRefusesEscapingTheResolver(t *testing.T) {
	dir := t.TempDir()
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	defer res.Close()
	for _, ref := range []string{"../outside.ttf", "/etc/fonts/x.ttf"} {
		built := Build(Input{
			HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url("` + ref + `"); }`),
			Resources: res,
		})
		if _, ok := built.Fonts.Face("Trial", false, false); ok {
			t.Errorf("%s was loaded", ref)
		}
		requireFinding(t, built.Findings, RuleResourceBlocked, "was not loaded")
	}
	fired[RuleResourceBlocked] = true
}

// TestFontFaceDataURI pins that the one exception to "no scheme" behaves like
// the exception it is: the bytes were in the document already, and they go
// through the same capped decoder as an image's.
func TestFontFaceDataURI(t *testing.T) {
	uri := "data:font/ttf;base64," + base64.StdEncoding.EncodeToString(realFont())
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: url("` + uri + `"); }`),
	})
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Fatalf("a data: font did not load; findings: %v", built.Findings)
	}

	// And the data URI cap is the same one, at the same place.
	saved := maxDataURIBytes
	maxDataURIBytes = 16
	defer func() { maxDataURIBytes = saved }()
	built = Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: url("` + uri + `"); }`),
	})
	if _, ok := built.Fonts.Face("Trial", false, false); ok {
		t.Error("a data: font past the cap was still decoded")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "loaded no font")
}

// TestFontFileCapFires is the per-file cap. The font is a real one and the cap
// is lowered under it, so what is being watched is the comparison rather than an
// allocation — and the refusal happens before fonts.Load, which is the point of
// having it at all.
func TestFontFileCapFires(t *testing.T) {
	saved := maxFontBytes
	maxFontBytes = len(realFont()) - 1
	defer func() { maxFontBytes = saved }()

	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	built := Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
		Resources: res,
	})
	if _, ok := built.Fonts.Face("Trial", false, false); ok {
		t.Error("a font larger than the cap was parsed")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "this engine will parse")

	// One byte the other way and it loads, so the cap is a boundary rather than
	// a refusal of everything.
	maxFontBytes = len(realFont())
	built = Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
		Resources: res,
	})
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Errorf("a font of exactly the cap was refused; findings: %v", built.Findings)
	}
	fired[RuleResourceBlocked] = true
}

// TestDocumentFontByteBudgetFires is the document-wide budget: a per-file cap
// does not bound a document, and this is what does.
func TestDocumentFontByteBudgetFires(t *testing.T) {
	saved := maxDocumentFontBytes
	maxDocumentFontBytes = len(realFont()) + 1
	defer func() { maxDocumentFontBytes = saved }()

	res := &fileResolver{files: map[string][]byte{
		"one.ttf": realFont(), "two.ttf": realFont(),
	}}
	built := Build(Input{
		HTML: `<style>
			@font-face { font-family: One; src: url(one.ttf); }
			@font-face { font-family: Two; src: url(two.ttf); }
		</style><p>x</p>`,
		Resources: res,
	})
	if _, ok := built.Fonts.Face("One", false, false); !ok {
		t.Errorf("the first font was refused; findings: %v", built.Findings)
	}
	if _, ok := built.Fonts.Face("Two", false, false); ok {
		t.Error("the second font was parsed past the document's byte budget")
	}
	requireFinding(t, built.Findings, RuleLimit, "for one document")
	requireFinding(t, built.Findings, RuleResourceBlocked, "budget was already spent")
	fired[RuleLimit] = true
	fired[RuleResourceBlocked] = true
}

// TestDocumentFaceCountCapFires is the count. It bounds how many font programs
// one document can make this engine parse, which the byte budget alone does not:
// a thousand small fonts are a thousand parses and few bytes.
func TestDocumentFaceCountCapFires(t *testing.T) {
	saved := maxDocumentFaces
	maxDocumentFaces = 2
	defer func() { maxDocumentFaces = saved }()

	files := map[string][]byte{}
	var sheet strings.Builder
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("f%d.ttf", i)
		files[name] = realFont()
		fmt.Fprintf(&sheet, "@font-face { font-family: F%d; src: url(%s); }\n", i, name)
	}
	res := &fileResolver{files: files}
	built := Build(Input{HTML: "<style>" + sheet.String() + "</style><p>x</p>", Resources: res})

	var have int
	for i := 0; i < 4; i++ {
		if _, ok := built.Fonts.Face(fmt.Sprintf("F%d", i), false, false); ok {
			have++
		}
	}
	if have != maxDocumentFaces {
		t.Errorf("%d of 4 faces loaded, want the cap of %d", have, maxDocumentFaces)
	}
	requireFinding(t, built.Findings, RuleLimit, "font faces this engine will parse")
	fired[RuleLimit] = true
}

// TestFontFaceRuleCapFires is the cap that answers a page declaring a thousand
// @font-face rules.
//
// The other two caps do not: a rule whose file is missing costs a resolver call
// and no bytes and no face, so a thousand of them would be a thousand system
// calls with every other guard still showing green. The files here are all
// missing for exactly that reason.
func TestFontFaceRuleCapFires(t *testing.T) {
	saved := maxFontFaceRules
	maxFontFaceRules = 3
	defer func() { maxFontFaceRules = saved }()

	var sheet strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sheet, "@font-face { font-family: F%d; src: url(f%d.ttf); }\n", i, i)
	}
	res := &fileResolver{files: map[string][]byte{}}
	built := Build(Input{HTML: "<style>" + sheet.String() + "</style><p>x</p>", Resources: res})

	if len(res.asked) != maxFontFaceRules {
		t.Errorf("the resolver was asked %d times for 10 rules with a cap of %d",
			len(res.asked), maxFontFaceRules)
	}
	requireFinding(t, built.Findings, RuleLimit, "this engine will look at")
	requireFinding(t, built.Findings, RuleResourceBlocked, "was not read")
	fired[RuleLimit] = true
	fired[RuleResourceBlocked] = true
}

// TestFontSrcListCapFires bounds one rule's fallback chain, so that a single
// @font-face cannot be a loop.
func TestFontSrcListCapFires(t *testing.T) {
	saved := maxFontSources
	maxFontSources = 2
	defer func() { maxFontSources = saved }()

	var srcs []string
	for i := 0; i < 8; i++ {
		srcs = append(srcs, fmt.Sprintf("url(f%d.ttf)", i))
	}
	res := &fileResolver{files: map[string][]byte{}}
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: ` +
			strings.Join(srcs, ", ") + `; }`),
		Resources: res,
	})
	if len(res.asked) != maxFontSources {
		t.Errorf("the resolver was asked %d times for 8 sources with a cap of %d",
			len(res.asked), maxFontSources)
	}
	requireFinding(t, built.Findings, RuleLimit, "src entries")
	fired[RuleLimit] = true
}

// TestFontUndecodable separates "the file did not arrive" from "the file
// arrived and was not a font", which is the distinction the rule exists for.
func TestFontUndecodable(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{
		"junk.ttf":  []byte("this is not a font program at all"),
		"empty.ttf": {},
	}}
	built := Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(junk.ttf); }`),
		Resources: res,
	})
	requireFinding(t, built.Findings, RuleResourceBlocked, "not one this engine can read")
	fired[RuleFontUndecodable] = true

	built = Build(Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(empty.ttf); }`),
		Resources: res,
	})
	requireFinding(t, built.Findings, RuleResourceBlocked, "is empty")

	// A format hint for a container this engine does not unwrap is refused
	// before the read, which is why the resolver is never asked for it.
	res = &fileResolver{files: map[string][]byte{"x.woff2": realFont()}}
	built = Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial;
			src: url(x.woff2) format("woff2"); }`),
		Resources: res,
	})
	if len(res.asked) != 0 {
		t.Errorf("a woff2 was fetched before its format hint was read: %v", res.asked)
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "which this engine does not read")
}

// TestFontUndecodableIsItsOwnFinding checks the rule reaches the report rather
// than only the message inside the rule-level one. The per-entry reason is
// folded into the @font-face finding, so the loader is driven directly.
func TestFontUndecodableIsItsOwnFinding(t *testing.T) {
	l := &fontFaceLoader{
		rec: NewRecorder(nil), base: StandardFonts(),
		res:    &fileResolver{files: map[string][]byte{"junk.ttf": []byte("nope")}},
		loaded: map[string]*fonts.Face{}, failed: map[string]bool{},
		budget: maxDocumentFontBytes,
	}
	_, fail := l.load(pendingFontFace{}, fontFaceRule{family: "Trial"},
		fontSource{ref: "junk.ttf"})
	if fail == nil || fail.rule != RuleFontUndecodable {
		t.Fatalf("a font program that does not parse gave %+v, want %s", fail, RuleFontUndecodable)
	}
	fired[RuleFontUndecodable] = true
}

// TestFontFaceWeightAndStyleChoose pins that the descriptors decide which of a
// family's faces a box gets.
//
// The two faces are the same file under two names, so nothing about the *font*
// can be what distinguishes them — only the descriptors, which is what is being
// tested. Which face came back is read off the reference the loader recorded.
func TestFontFaceWeightAndStyleChoose(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{
		"regular.ttf": realFont(), "bold.ttf": realFont(),
		"italic.ttf": realFont(),
	}}
	built := Build(Input{
		HTML: `<style>
			@font-face { font-family: Trial; src: url(regular.ttf); }
			@font-face { font-family: Trial; font-weight: 700; src: url(bold.ttf); }
			@font-face { font-family: Trial; font-style: italic; src: url(italic.ttf); }
		</style><p>x</p>`,
		Resources: res,
	})
	set, ok := built.Fonts.(*documentFonts)
	if !ok {
		t.Fatalf("the document's set is %T; findings: %v", built.Fonts, built.Findings)
	}
	refOf := func(bold, italic bool) string {
		face, ok := set.Face("Trial", bold, italic)
		if !ok {
			t.Fatalf("Trial did not resolve for bold=%v italic=%v", bold, italic)
		}
		for _, df := range set.faces {
			if df.face == face {
				return df.ref
			}
		}
		return "<unknown>"
	}
	// Three files, one face each: the loader shares a face between references
	// only when the reference is the same, so the identity really distinguishes
	// them.
	if len(set.faces) != 3 {
		t.Fatalf("%d faces loaded, want 3", len(set.faces))
	}
	for _, tc := range []struct {
		bold, italic bool
		want         string
	}{
		{false, false, "regular.ttf"},
		{true, false, "bold.ttf"},
		{false, true, "italic.ttf"},
		// Bold italic: nothing is both, and the slant is the more visible
		// difference, so the italic face wins over the bold one.
		{true, true, "italic.ttf"},
	} {
		if got := refOf(tc.bold, tc.italic); got != tc.want {
			t.Errorf("bold=%v italic=%v chose %s, want %s", tc.bold, tc.italic, got, tc.want)
		}
	}
}

// TestWeightRankFollowsTheSpecification pins the one part of font matching that
// is not "closest number", because it is the part that would look like a bug.
func TestWeightRankFollowsTheSpecification(t *testing.T) {
	// At 400, a 500 face beats a 300 one even though 300 is the same distance.
	if weightRank(400, 500) >= weightRank(400, 300) {
		t.Error("at a desired weight of 400, a 500 face must be preferred to a 300 one")
	}
	// At 400, anything above 500 loses to anything below 400.
	if weightRank(400, 600) <= weightRank(400, 100) {
		t.Error("at 400, a 600 face must lose to a 100 one")
	}
	// Above 500 it is heavier-first.
	if weightRank(700, 900) >= weightRank(700, 600) {
		t.Error("at 700, a 900 face must be preferred to a 600 one")
	}
	// Below 400 it is lighter-first.
	if weightRank(300, 100) >= weightRank(300, 400) {
		t.Error("at 300, a 100 face must be preferred to a 400 one")
	}
	// An exact match always wins.
	for _, d := range []float64{100, 400, 500, 700, 900} {
		if weightRank(d, d) != 0 {
			t.Errorf("an exact match at %v ranked %v", d, weightRank(d, d))
		}
	}
	// A range covers everything inside it at no cost.
	if clampWeight(650, 100, 900) != 650 {
		t.Error("a weight inside a declared range was not matched exactly")
	}
	if clampWeight(900, 100, 400) != 400 {
		t.Error("a weight above a declared range did not clamp to its top")
	}
}

// TestFontFaceUnicodeRangeIsReported is the honest half of a descriptor this
// engine parses and cannot honour.
//
// The full range restricts nothing and must be silent, or every well-written
// stylesheet on the web would carry a finding. A restricted one is reported,
// because the face is then used for characters the author excluded from it.
func TestFontFaceUnicodeRangeIsReported(t *testing.T) {
	load := func(rng string) Built {
		res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
		return Build(Input{
			HTML: docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf);
				unicode-range: ` + rng + `; }`),
			Resources: res,
		})
	}

	built := load("U+0-10FFFF")
	for _, f := range built.Findings {
		if f.Property == "unicode-range" {
			t.Errorf("a unicode-range covering the whole of Unicode was reported: %s", f.Message)
		}
	}
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Error("a face with a full unicode-range was not registered")
	}

	built = load("U+0025-00FF, U+4??")
	requireFinding(t, built.Findings, RuleUnsupportedValue, "U+0025-00FF, U+0400-04FF")
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Error("a face with a restricted unicode-range was dropped rather than used whole")
	}

	// A range this engine cannot read is a descriptor error, and the default —
	// the whole of Unicode — is what is used, which is what CSS says to do with
	// an invalid descriptor.
	built = load("U+ZZZZ")
	requireFinding(t, built.Findings, RuleInvalidCSS, "unicode-range")
	if _, ok := built.Fonts.Face("Trial", false, false); !ok {
		t.Error("an unreadable unicode-range took the whole rule down with it")
	}
	fired[RuleInvalidCSS] = true
	fired[RuleUnsupportedValue] = true
}

// TestUnicodeRangeGrammar exercises the three forms and the refusals, because
// the descriptor is reconstructed from tokens that were never meant to carry it
// and a parser that quietly accepted everything would report nothing.
func TestUnicodeRangeGrammar(t *testing.T) {
	parse := func(s string) ([]unicodeSpan, bool) {
		vals, _ := css.ParseComponentValues(s)
		return parseUnicodeRange(vals)
	}
	for _, tc := range []struct {
		in   string
		want []unicodeSpan
	}{
		{"U+26", []unicodeSpan{{0x26, 0x26}}},
		{"u+A5", []unicodeSpan{{0xA5, 0xA5}}},
		{"U+0-10FFFF", []unicodeSpan{{0, 0x10FFFF}}},
		{"U+4??", []unicodeSpan{{0x400, 0x4FF}}},
		{"U+???", []unicodeSpan{{0, 0xFFF}}},
		{"U+0025-00FF, U+4??", []unicodeSpan{{0x25, 0xFF}, {0x400, 0x4FF}}},
	} {
		got, ok := parse(tc.in)
		if !ok {
			t.Errorf("%q did not parse", tc.in)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q gave %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q gave %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
	for _, bad := range []string{
		"", "26", "U+", "U+ZZ", "U+1234567", "U+100-50", "U+4?0",
		"U+110000", "U+0-110000", "U+26,", "url(x)",
	} {
		if got, ok := parse(bad); ok {
			t.Errorf("%q was accepted as %v", bad, got)
		}
	}
	// Coverage is a union rather than a single span, so two halves that meet
	// cover everything and two that do not, do not.
	if !coversAllOfUnicode([]unicodeSpan{{0, 0xFFFF}, {0x10000, 0x10FFFF}}) {
		t.Error("two spans that meet were not seen to cover Unicode")
	}
	if coversAllOfUnicode([]unicodeSpan{{0, 0xFFFF}, {0x10001, 0x10FFFF}}) {
		t.Error("two spans with a gap between them were seen to cover Unicode")
	}
	if coversAllOfUnicode([]unicodeSpan{{0, 0x10FFFE}}) {
		t.Error("a span one short of the end was seen to cover Unicode")
	}
}

// TestFontFaceDescriptorsThatChangeMetricsAreReported pins that a descriptor
// this engine drops is not dropped silently. size-adjust and the override
// descriptors move the text on the page.
func TestFontFaceDescriptorsThatChangeMetricsAreReported(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	built := Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf);
			size-adjust: 120%; ascent-override: 90%; }`),
		Resources: res,
	})
	requireFinding(t, built.Findings, RuleUnsupportedProperty, "size-adjust")
	requireFinding(t, built.Findings, RuleUnsupportedProperty, "ascent-override")
	fired[RuleUnsupportedProperty] = true

	// font-display is not one of them: it says what to show while a font is
	// downloading, and nothing here downloads.
	built = Build(Input{
		HTML: docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf);
			font-display: swap; }`),
		Resources: res,
	})
	for _, f := range built.Findings {
		if f.Property == "font-display" {
			t.Errorf("font-display was reported: %s", f.Message)
		}
	}
}

// TestFontFaceWithoutFamilyOrSrc pins that a rule which cannot mean anything is
// reported as malformed rather than as a feature this engine lacks. An author
// sent to the wrong one of those looks for the wrong thing.
func TestFontFaceWithoutFamilyOrSrc(t *testing.T) {
	built := Build(Input{HTML: docWithFontFace(`@font-face { src: url(x.ttf); }`)})
	requireFinding(t, built.Findings, RuleInvalidCSS, "names no font-family")

	built = Build(Input{HTML: docWithFontFace(`@font-face { font-family: Trial; }`)})
	requireFinding(t, built.Findings, RuleInvalidCSS, "names no usable src")

	built = Build(Input{HTML: docWithFontFace(`@font-face { font-family: Trial; src: none; }`)})
	requireFinding(t, built.Findings, RuleInvalidCSS, "names no usable src")
	fired[RuleInvalidCSS] = true
}

// TestFontFaceOneReadPerReference pins that a document naming one file in
// several rules reads and parses it once. Otherwise the caps above would be
// bypassable by repetition, which is how a cache becomes an amplifier.
func TestFontFaceOneReadPerReference(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	built := Build(Input{
		HTML: `<style>
			@font-face { font-family: One; src: url(trial.ttf); }
			@font-face { font-family: Two; src: url(trial.ttf); }
			@font-face { font-family: Three; src: url(trial.ttf); }
		</style><p>x</p>`,
		Resources: res,
	})
	if len(res.asked) != 1 {
		t.Errorf("one file named by three rules was read %d times", len(res.asked))
	}
	set, ok := built.Fonts.(*documentFonts)
	if !ok {
		t.Fatalf("the document's set is %T", built.Fonts)
	}
	for _, name := range []string{"One", "Two", "Three"} {
		if _, ok := set.Face(name, false, false); !ok {
			t.Errorf("%s did not resolve", name)
		}
	}
	// And a failed reference is likewise attempted once.
	res = &fileResolver{files: map[string][]byte{}}
	Build(Input{
		HTML: `<style>
			@font-face { font-family: One; src: url(gone.ttf); }
			@font-face { font-family: Two; src: url(gone.ttf); }
		</style><p>x</p>`,
		Resources: res,
	})
	if len(res.asked) != 1 {
		t.Errorf("one missing file named by two rules was attempted %d times", len(res.asked))
	}
}

// TestFontFaceLastDeclarationWins pins the tie-break, which is the cascade's
// last term applied to a rule that is not in the cascade: a document that
// redeclares a family later means the later one.
func TestFontFaceLastDeclarationWins(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{
		"first.ttf": realFont(), "second.ttf": realFont(),
	}}
	built := Build(Input{
		HTML: `<style>
			@font-face { font-family: Trial; src: url(first.ttf); }
			@font-face { font-family: Trial; src: url(second.ttf); }
		</style><p>x</p>`,
		Resources: res,
	})
	set, ok := built.Fonts.(*documentFonts)
	if !ok {
		t.Fatalf("the document's set is %T", built.Fonts)
	}
	face, _ := set.Face("Trial", false, false)
	for _, df := range set.faces {
		if df.face == face && df.ref != "second.ttf" {
			t.Errorf("the family resolved to %s, want the later declaration", df.ref)
		}
	}
}

// TestFontFaceKeepsTheFallbackInterface pins that wrapping the caller's set does
// not lose what it could do. A FallbackFontSet that stopped answering the
// coverage question would leave every script the standard faces lack reporting a
// missing glyph, which is a large and silent regression.
func TestFontFaceKeepsTheFallbackInterface(t *testing.T) {
	res := &fileResolver{files: map[string][]byte{"trial.ttf": realFont()}}
	in := Input{
		HTML:      docWithFontFace(`@font-face { font-family: Trial; src: url(trial.ttf); }`),
		Resources: res,
	}

	built := Build(in)
	if _, ok := built.Fonts.(FallbackFontSet); ok {
		t.Error("wrapping a plain set invented a fallback it cannot answer")
	}

	in.Fonts = fallbackOnly{StandardFonts()}
	built = Build(in)
	fb, ok := built.Fonts.(FallbackFontSet)
	if !ok {
		t.Fatal("wrapping a fallback set lost the interface")
	}
	if _, ok := fb.FaceFor("anything", false, false); !ok {
		t.Error("the wrapped set did not pass the coverage question through")
	}
}

// fallbackOnly is a FontSet that also answers the coverage question, for the
// wrapping test above.
type fallbackOnly struct{ FontSet }

func (f fallbackOnly) FaceFor(text string, bold, italic bool) (*fonts.Face, bool) {
	return f.FontSet.Face("sans-serif", false, false)
}
