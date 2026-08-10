package render

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Following a <link rel=stylesheet>, and the policy that says what it may reach.
//
// The refusals are the substance of this file, and each is written so that the
// thing being refused *exists*: a test that links "../secret.css" in a directory
// with no secret in it would pass against an engine that read whatever it was
// pointed at and found nothing. Every case below plants a real stylesheet with a
// visible effect on the page, and then requires that the effect does not appear.
//
// That is the same argument resource_test.go makes about images, and it is the
// reason there is one resolver rather than two.

// cssDir prepares a directory of stylesheets and a document that links them.
func cssDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeCSS puts a stylesheet on disk.
func writeCSS(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// buildLinking renders a document with its stylesheets resolved from dir.
func buildLinking(t *testing.T, dir, htmlSrc string) Built {
	t.Helper()
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })
	return Build(Input{HTML: htmlSrc, Resources: res})
}

// colourOf returns the computed colour of the element with the given id, which
// is what every case here uses to tell "the sheet applied" from "it did not".
func colourOf(t *testing.T, built Built, id string) string {
	t.Helper()
	return findBox(t, built.Root, id).Style["color"]
}

const wantColour = "rgb(1, 2, 3)"
const notColour = "rgb(9, 9, 9)"

// TestLinkedStylesheetApplies is the positive case every refusal below is
// measured against. Without it they would all be satisfied by an engine that
// followed no link at all — which is precisely what this engine used to be.
func TestLinkedStylesheetApplies(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "sheet.css"), "p { color: rgb(1, 2, 3) }")

	built := buildLinking(t, dir, `<link rel=stylesheet href=sheet.css><p id=p>x</p>`)
	if got := colourOf(t, built, "p"); got != wantColour {
		t.Errorf("the linked stylesheet did not apply: colour is %q, want %q; findings: %v",
			got, wantColour, built.Findings)
	}
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked {
			t.Errorf("a stylesheet that loaded still raised %s", f.Error())
		}
	}
}

// TestLinkedStylesheetNeedsAResolver is the deny-by-default guarantee: the
// engine's own default loads nothing, and says so.
//
// The saying-so is half the test. A silently ignored link is what this engine
// did before, and it produces an unstyled page that nothing distinguishes from a
// styled one — which is the failure the whole finding vocabulary exists for.
func TestLinkedStylesheetNeedsAResolver(t *testing.T) {
	built := Build(Input{HTML: `<link rel=stylesheet href=sheet.css><p id=p>x</p>`})
	if got := colourOf(t, built, "p"); got == wantColour {
		t.Error("a stylesheet was loaded with no resolver configured")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, ErrNoResolver.Error())
	fired[RuleResourceBlocked] = true
}

// countingResolver records what it was asked for, so a test can prove a refusal
// happened *before* the resolver rather than inside it.
//
// That distinction is the one the scheme refusal rests on: a caller may write a
// resolver that hands the reference to something that does fetch URLs, and the
// engine's promise is that such a resolver is never given the chance.
type countingResolver struct {
	asked []string
	body  string
}

func (c *countingResolver) Resolve(ref string) ([]byte, error) {
	c.asked = append(c.asked, ref)
	if c.body == "" {
		return nil, errors.New("nothing here")
	}
	return []byte(c.body), nil
}

// TestLinkedStylesheetRefusesSchemes is the no-network guarantee.
//
// The resolver would serve the stylesheet if it were ever asked, so a scheme
// that got through would show up twice over: in the page's colour and in the
// list of things the resolver was asked for. Both are checked, because a test
// that only looked at the colour would pass against an engine that fetched the
// sheet and then dropped it for some other reason.
func TestLinkedStylesheetRefusesSchemes(t *testing.T) {
	for _, href := range []string{
		"http://example.invalid/sheet.css",
		"https://example.invalid/sheet.css",
		"file:///etc/sheet.css",
		"ftp://example.invalid/sheet.css",
		// A Windows drive letter parses as a scheme, and being refused as one
		// is the right outcome for it too.
		"c:/windows/sheet.css",
	} {
		res := &countingResolver{body: "p { color: rgb(1, 2, 3) }"}
		built := Build(Input{
			HTML:      `<link rel=stylesheet href="` + href + `"><p id=p>x</p>`,
			Resources: res,
		})
		if got := colourOf(t, built, "p"); got == wantColour {
			t.Errorf("%s: the stylesheet was applied", href)
		}
		if len(res.asked) != 0 {
			t.Errorf("%s: the resolver was asked for %q; a scheme must be refused "+
				"before any resolver sees it", href, res.asked)
		}
		requireFinding(t, built.Findings, RuleResourceBlocked, "scheme")
	}
}

// TestLinkedStylesheetIsContained plants a stylesheet outside the rooted
// directory and requires that no spelling reaches it.
//
// The planted sheet sets a colour, so "the containment held" and "the file was
// not there" are different observable outcomes. Each case also names the reason
// it expects: several of these are refused twice over — once by resourcePath's
// name check and once by os.Root at the system call — and a test that only asked
// whether the page was unstyled would pass with either half deleted.
func TestLinkedStylesheetIsContained(t *testing.T) {
	base := t.TempDir()
	writeCSS(t, filepath.Join(base, "outside.css"), "p { color: rgb(1, 2, 3) }")
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "outside.css"),
		filepath.Join(root, "escape.css")); err != nil {
		t.Skipf("this filesystem does not support symbolic links: %v", err)
	}

	for _, tc := range []struct{ href, reason string }{
		{"../outside.css", "leaves the directory"},
		{"a/../../outside.css", "leaves the directory"},
		{filepath.Join(base, "outside.css"), "absolute path"},
		{"/outside.css", "absolute path"},
		// Per-cent escaped, so the name check sees no ".." until it has
		// decoded — which it does before looking.
		{"%2e%2e/outside.css", "leaves the directory"},
		// The symlink is the case a string comparison cannot catch: the name
		// has no ".." in it at all, and only the kernel knows where it goes.
		{"escape.css", "escapes from parent"},
	} {
		built := buildLinking(t, root,
			`<link rel=stylesheet href="`+tc.href+`"><p id=p>x</p>`)
		if got := colourOf(t, built, "p"); got == wantColour {
			t.Errorf("%s: the stylesheet outside the root was applied", tc.href)
		}
		requireFinding(t, built.Findings, RuleResourceBlocked, tc.reason)
	}
}

// TestLinkedStylesheetSizeCapIsCrossedNotApproached pins the per-sheet cap at
// its boundary: a sheet exactly at it loads and one byte more does not.
//
// The boundary is what makes this a test of the comparison rather than of the
// existence of a cap. A test with a small sheet and an enormous one would pass
// against a cap ten times too large, or one written the wrong way round.
func TestLinkedStylesheetSizeCapIsCrossedNotApproached(t *testing.T) {
	dir := cssDir(t)
	rule := "p { color: rgb(1, 2, 3) }"
	old := maxStylesheetBytes
	defer func() { maxStylesheetBytes = old }()

	// Exactly at the cap.
	maxStylesheetBytes = len(rule)
	writeCSS(t, filepath.Join(dir, "sheet.css"), rule)
	built := buildLinking(t, dir, `<link rel=stylesheet href=sheet.css><p id=p>x</p>`)
	if got := colourOf(t, built, "p"); got != wantColour {
		t.Errorf("a stylesheet of exactly the cap was refused: %v", built.Findings)
	}

	// One byte over, with the *same* file: only the cap moved.
	maxStylesheetBytes = len(rule) - 1
	built = buildLinking(t, dir, `<link rel=stylesheet href=sheet.css><p id=p>x</p>`)
	if got := colourOf(t, built, "p"); got == wantColour {
		t.Error("a stylesheet one byte over the cap was read")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "more than the")
}

// TestLinkedStylesheetCountCapFires is the bound on the *document* rather than
// on a sheet.
//
// The per-sheet cap is not a bound on anything by itself: a page with a thousand
// links is a thousand legal reads and a thousand parses. This plants one more
// sheet than the cap allows, requires that the last one does not apply and that
// the ones inside the cap do — a cap that refused everything would satisfy half
// this test — and requires both of the findings it raises.
func TestLinkedStylesheetCountCapFires(t *testing.T) {
	dir := cssDir(t)
	old := maxDocumentStylesheets
	defer func() { maxDocumentStylesheets = old }()
	maxDocumentStylesheets = 3

	var doc strings.Builder
	for i := 0; i <= maxDocumentStylesheets; i++ {
		name := fmt.Sprintf("s%d.css", i)
		writeCSS(t, filepath.Join(dir, name), fmt.Sprintf("#e%d { color: rgb(1, 2, 3) }", i))
		fmt.Fprintf(&doc, `<link rel=stylesheet href=%s>`, name)
	}
	for i := 0; i <= maxDocumentStylesheets; i++ {
		fmt.Fprintf(&doc, `<p id=e%d>x</p>`, i)
	}
	built := buildLinking(t, dir, doc.String())

	for i := 0; i < maxDocumentStylesheets; i++ {
		id := fmt.Sprintf("e%d", i)
		if got := colourOf(t, built, id); got != wantColour {
			t.Errorf("sheet %d is inside the cap and did not apply: %q", i, got)
		}
	}
	last := fmt.Sprintf("e%d", maxDocumentStylesheets)
	if got := colourOf(t, built, last); got == wantColour {
		t.Errorf("the sheet past the cap of %d was loaded", maxDocumentStylesheets)
	}
	requireFinding(t, built.Findings, RuleLimit, "stylesheets this engine will load")
	requireFinding(t, built.Findings, RuleResourceBlocked, "already")
	fired[RuleLimit] = true
}

// TestLinkedStylesheetCountCapCountsRepeats is the other half of the count cap,
// and it exists because the read cache is exactly the thing that could defeat
// it.
//
// A sheet read once and applied a thousand times is a thousand parses and a
// thousand passes of the cascade over every element, which is the cost the cap
// is there to bound — so the cap counts sheets *applied*, not files read. This
// links one file more times than the cap allows and requires it to trip.
func TestLinkedStylesheetCountCapCountsRepeats(t *testing.T) {
	old := maxDocumentStylesheets
	defer func() { maxDocumentStylesheets = old }()
	maxDocumentStylesheets = 3

	res := &countingResolver{body: "p { color: rgb(1, 2, 3) }"}
	built := Build(Input{
		HTML: strings.Repeat(`<link rel=stylesheet href=one.css>`,
			maxDocumentStylesheets+2) + `<p id=p>x</p>`,
		Resources: res,
	})
	if len(res.asked) != 1 {
		t.Errorf("one file was read %d times: %q", len(res.asked), res.asked)
	}
	requireFinding(t, built.Findings, RuleLimit, "stylesheets this engine will load")
}

// TestRepeatedLinkKeepsItsPlaceInTheCascade is why the loader caches the text of
// a sheet rather than suppressing the second <link> to it.
//
// Suppressing the repeat is the obvious way to avoid reading a file twice, and
// it is wrong: a stylesheet linked twice is a stylesheet at both points in
// document order, so a <style> written between them loses the tie to the second
// one and wins against the first. Collapsing them silently reverses that.
func TestRepeatedLinkKeepsItsPlaceInTheCascade(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "link.css"), "p { color: rgb(1, 2, 3) }")

	built := buildLinking(t, dir,
		`<link rel=stylesheet href=link.css>`+
			`<style>p { color: rgb(9, 9, 9) }</style>`+
			`<link rel=stylesheet href=link.css>`+
			`<p id=p>x</p>`)
	if got := colourOf(t, built, "p"); got != wantColour {
		t.Errorf("the second <link> to one file lost its place in the cascade: "+
			"colour is %q, want %q", got, wantColour)
	}
}

// TestLinkedStylesheetCascadesInDocumentOrder is the ordering guarantee, and it
// is a real bug waiting to happen rather than a formality: the cascade's last
// tie-break is which rule was written later, so collecting every <style> and
// then every <link> would give the wrong page for a document that alternates
// them.
//
// Both directions are checked. One of them would pass against an implementation
// that appended all linked sheets at the end, and the other against one that put
// them all first.
func TestLinkedStylesheetCascadesInDocumentOrder(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "link.css"), "p { color: rgb(1, 2, 3) }")

	linkLast := buildLinking(t, dir,
		`<style>p { color: rgb(9, 9, 9) }</style>`+
			`<link rel=stylesheet href=link.css>`+
			`<p id=p>x</p>`)
	if got := colourOf(t, linkLast, "p"); got != wantColour {
		t.Errorf("a <link> after a <style> did not win: %q, want %q", got, wantColour)
	}

	styleLast := buildLinking(t, dir,
		`<link rel=stylesheet href=link.css>`+
			`<style>p { color: rgb(9, 9, 9) }</style>`+
			`<p id=p>x</p>`)
	if got := colourOf(t, styleLast, "p"); got != notColour {
		t.Errorf("a <style> after a <link> did not win: %q, want %q", got, notColour)
	}

	// And the caller's own sheets still come after everything the document
	// carries, which is the origin ordering Build documents.
	res, err := NewDirResolver(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	withCaller := Build(Input{
		HTML:      `<link rel=stylesheet href=link.css><p id=p>x</p>`,
		Resources: res,
		CSS:       []Stylesheet{{Name: "caller", Source: "p { color: rgb(9, 9, 9) }"}},
	})
	if got := colourOf(t, withCaller, "p"); got != notColour {
		t.Errorf("the caller's stylesheet did not come after the document's: %q", got)
	}
}

// TestLinkedStylesheetImportIsReported records what this engine does *not* do.
//
// @import is not followed, and the reason is not effort. The engine is handed a
// reference exactly as the document wrote it and has no notion of a base URL —
// that join is the caller's resolver's job, and it is the *document's* directory
// that a resolver knows about. An @import inside "support/theme.css" is relative
// to support/, which nothing here can express, so following it would resolve
// half of them against the wrong directory and silently load the wrong file or
// nothing at all.
//
// So it is refused and reported, through the at-rule path every unimplemented
// at-rule already uses. The test is here rather than left implicit because a
// dropped @import is exactly as invisible as a dropped <link> was.
func TestLinkedStylesheetImportIsReported(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "inner.css"), "p { color: rgb(1, 2, 3) }")
	writeCSS(t, filepath.Join(dir, "outer.css"), `@import url("inner.css");`)

	built := buildLinking(t, dir, `<link rel=stylesheet href=outer.css><p id=p>x</p>`)
	if got := colourOf(t, built, "p"); got == wantColour {
		t.Error("@import was followed; nothing here resolves one")
	}
	requireFinding(t, built.Findings, RuleUnsupportedAtRule, "@import")
	fired[RuleUnsupportedAtRule] = true
}

// TestLinkedStylesheetRelIsATokenList checks which links are stylesheets at all.
func TestLinkedStylesheetRelIsATokenList(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "sheet.css"), "p { color: rgb(1, 2, 3) }")

	for _, tc := range []struct {
		rel   string
		apply bool
	}{
		{"stylesheet", true},
		{"STYLESHEET", true},
		{"  stylesheet  ", true},
		{"preload stylesheet", true},
		// An alternate stylesheet is one the reader may pick, and no browser
		// applies one unless they do. There is nobody here to ask.
		{"alternate stylesheet", false},
		{"stylesheet alternate", false},
		// A substring is not a token.
		{"no-stylesheet", false},
		{"stylesheets", false},
		{"help", false},
		{"", false},
	} {
		built := buildLinking(t, dir,
			`<link rel="`+tc.rel+`" href=sheet.css><p id=p>x</p>`)
		applied := colourOf(t, built, "p") == wantColour
		if applied != tc.apply {
			t.Errorf("rel=%q applied=%v, want %v", tc.rel, applied, tc.apply)
		}
	}
}

// TestLinkedStylesheetMedia checks the one media decision this engine can make
// and the reporting of the ones it cannot.
//
// A page is printed, so "print" and "all" apply and "screen" does not — that is
// a correct answer rather than a gap, and it raises nothing. A media *query*, on
// the other hand, is something this engine cannot evaluate at all, and applying
// it or dropping it silently would both be guesses.
func TestLinkedStylesheetMedia(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "sheet.css"), "p { color: rgb(1, 2, 3) }")

	for _, tc := range []struct {
		media    string
		apply    bool
		reported bool
	}{
		{"", true, false},
		{"all", true, false},
		{"print", true, false},
		{"Print", true, false},
		{"screen, print", true, false},
		{"screen", false, false},
		{"tty", false, false},
		{"screen and (min-width: 40em)", false, true},
		{"only print", false, true},
		{"not screen", false, true},
	} {
		built := buildLinking(t, dir,
			`<link rel=stylesheet media="`+tc.media+`" href=sheet.css><p id=p>x</p>`)
		applied := colourOf(t, built, "p") == wantColour
		if applied != tc.apply {
			t.Errorf("media=%q applied=%v, want %v", tc.media, applied, tc.apply)
		}
		reported := false
		for _, f := range built.Findings {
			if f.Rule == RuleUnsupportedValue && f.Property == "media" {
				reported = true
			}
		}
		if reported != tc.reported {
			t.Errorf("media=%q reported=%v, want %v; findings: %v",
				tc.media, reported, tc.reported, built.Findings)
		}
	}
	fired[RuleUnsupportedValue] = true
}

// TestLinkedStylesheetDataURI is the one reference with a scheme that is
// honoured, and it is not an exception to anything: its bytes are in the
// document already, so reading it reads nothing the caller did not supply.
func TestLinkedStylesheetDataURI(t *testing.T) {
	built := Build(Input{
		HTML: `<link rel=stylesheet href="data:text/css,p%20%7B%20color%3A%20rgb(1,%202,%203)%20%7D">` +
			`<p id=p>x</p>`,
	})
	if got := colourOf(t, built, "p"); got != wantColour {
		t.Errorf("a data: stylesheet did not apply: %q; findings: %v", got, built.Findings)
	}

	// And it is bounded by the same cap as everything else, with no resolver
	// anywhere in sight.
	old := maxDataURIBytes
	defer func() { maxDataURIBytes = old }()
	maxDataURIBytes = 4
	built = Build(Input{
		HTML: `<link rel=stylesheet href="data:text/css,p%20%7B%20color%3A%20rgb(1,%202,%203)%20%7D">` +
			`<p id=p>x</p>`,
	})
	if got := colourOf(t, built, "p"); got == wantColour {
		t.Error("a data: stylesheet past the cap was read")
	}
	requireFinding(t, built.Findings, RuleResourceBlocked, "more than the")
}

// TestLinkedStylesheetReadOnce requires that a document naming one sheet many
// times reads it once.
//
// It is a performance bound and a correctness one at the same time: the count
// cap is a bound on a document only if repeats are free, and a document with
// twenty links to one file must not spend its whole allowance on it.
func TestLinkedStylesheetReadOnce(t *testing.T) {
	res := &countingResolver{body: "p { color: rgb(1, 2, 3) }"}
	built := Build(Input{
		HTML:      strings.Repeat(`<link rel=stylesheet href=sheet.css>`, 5) + `<p id=p>x</p>`,
		Resources: res,
	})
	if got := colourOf(t, built, "p"); got != wantColour {
		t.Fatalf("the stylesheet did not apply: %q", got)
	}
	if len(res.asked) != 1 {
		t.Errorf("the resolver was asked %d times for one file: %q", len(res.asked), res.asked)
	}
}

// TestLinkedStylesheetEmptyHrefNamesNothing: a link with no href is not a broken
// stylesheet, it is an element that refers to nothing, and there is no refusal
// to report.
func TestLinkedStylesheetEmptyHrefNamesNothing(t *testing.T) {
	res := &countingResolver{}
	built := Build(Input{
		HTML:      `<link rel=stylesheet href=""><link rel=stylesheet><p id=p>x</p>`,
		Resources: res,
	})
	if len(res.asked) != 0 {
		t.Errorf("a link with no href was resolved: %q", res.asked)
	}
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked {
			t.Errorf("a link naming nothing raised %s", f.Error())
		}
	}
}

// TestLinkedStylesheetErrorIsReported covers the ordinary missing file, which is
// the case a caller sees most and the one that must not be silent.
func TestLinkedStylesheetErrorIsReported(t *testing.T) {
	dir := cssDir(t)
	built := buildLinking(t, dir, `<link rel=stylesheet href=absent.css><p id=p>x</p>`)
	requireFinding(t, built.Findings, RuleResourceBlocked, "absent.css")

	// An empty file is a stylesheet with no rules, which is legal and is not a
	// refusal of anything.
	writeCSS(t, filepath.Join(dir, "empty.css"), "")
	built = buildLinking(t, dir, `<link rel=stylesheet href=empty.css><p id=p>x</p>`)
	for _, f := range built.Findings {
		if f.Rule == RuleResourceBlocked {
			t.Errorf("an empty stylesheet raised %s", f.Error())
		}
	}
}

// TestLinkedStylesheetInvalidCSSNamesItsSheet: a parse error in a linked sheet
// has to say which file it was in, or an author with four stylesheets is sent
// looking through all of them.
func TestLinkedStylesheetInvalidCSSNamesItsSheet(t *testing.T) {
	dir := cssDir(t)
	writeCSS(t, filepath.Join(dir, "broken.css"), "p { color: rgb(1, 2, 3) } @@@")

	built := buildLinking(t, dir, `<link rel=stylesheet href=broken.css><p id=p>x</p>`)
	var named bool
	for _, f := range built.Findings {
		if f.Rule == RuleInvalidCSS && f.Source.Sheet == "broken.css" {
			named = true
		}
	}
	if !named {
		t.Errorf("a parse error in a linked sheet did not name it; findings: %v", built.Findings)
	}
}
