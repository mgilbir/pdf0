package htmlpdf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfua"
)

// taggedPage is a page with a heading, a paragraph with an emphasised word,
// text in another language and a link in it, a decorative and a described
// picture, a list, and a table whose data cells name their headers.
func taggedPage(t *testing.T) Input {
	t.Helper()
	return Input{
		HTML: `<!doctype html><html lang="en"><head><title>Quarterly report</title></head><body>
<h1 style="border-bottom: 2px solid red">Results</h1>
<p>Revenue grew in <em>every</em> quarter, <span lang="fr">bien sûr</span>; see <a href="https://example.com/report">the report</a>.</p>
<p><img src="a.png" alt="" style="width: 20px"> <img src="a.png" alt="A rising line" style="width: 40px"></p>
<ul><li>First</li><li>Second</li></ul>
<table><tr><th id="q">Quarter</th><th id="r">Revenue</th></tr><tr><td headers="q">Q1</td><td headers="r">100</td></tr></table>
</body></html>`,
		Resources: bytesResolver{"a.png": pngBytes(t)},
		Fonts:     notoSansSet(t),
	}
}

// hardPage is what reorders the painting: a float, an absolutely positioned
// box, a nested list, generated content, a text shadow, a filter, an inline
// SVG, a display: contents element with a language and a link, a hidden
// paragraph, and a table with spanning and empty cells.
const hardPage = `<!doctype html><html lang="en"><head><title>Hard page</title><style>
.f{float:right;width:120px} .abs{position:absolute;top:400px;left:300px}
h2::before{content:"§ "} .sh{text-shadow:1px 1px 0 gray} .flt{filter:opacity(50%)}
.dc{display:contents}
</style></head><body>
<div class="f"><p>Floated aside text.</p></div>
<h1>Hard page</h1><div style="position:absolute;top:520px;left:20px"><p>Early in the document, painted late</p></div><h2>Section <span class="sh">shadowed</span></h2>
<p>Before the float in flow. <span class="dc" lang="de">Guten Tag</span> and <a class="dc" href="https://e.org">contents <b>link</b></a>.</p>
<ol><li>One<ul><li>Nested</li></ul></li><li>Two</li></ol>
<blockquote><p>A quotation.</p></blockquote>
<figure><svg width="30" height="30" role="img"><title>A square</title><rect width="30" height="30"/></svg><figcaption>The square</figcaption></figure>
<div class="flt"><p class="sh">Filtered text</p></div>
<p aria-hidden="true">Hidden from readers</p>
<table><tr><th rowspan="2">Region</th><th colspan="2">Sales</th></tr><tr><th>H1</th><th>H2</th></tr>
<tr><td>North</td><td>1</td><td></td></tr></table>
<div class="abs"><p>Positioned</p></div>
</body></html>`

// outline is a structure tree as text: one line per element, indented, with
// its alt text, language, ID and attributes, then its kids in order — "#" for
// a span of marked content, "<Tag>" for a child element, "OBJR" for an
// annotation — and the child elements on the lines below.
func outline(doc *pdf0.Document) string {
	var sb strings.Builder
	var walk func(o object.Object, depth int)
	walk = func(o object.Object, depth int) {
		v := doc.ResolveDict(o)
		if v == nil {
			return
		}
		line := strings.Repeat("  ", depth) + string(v.Get("S").(object.Name))
		for _, key := range []object.Name{"Alt", "Lang", "ID"} {
			if s, ok := doc.Resolve(v.Get(key)).(object.String); ok {
				line += fmt.Sprintf(" %s=%q", key, s.Value)
			}
		}
		for _, a := range attributeDicts(doc, v.Get("A")) {
			for k, val := range a.All() {
				if k != "O" {
					line += fmt.Sprintf(" %s:%s=%v", a.Get("O"), k, doc.Resolve(val))
				}
			}
		}
		var kids object.Array
		switch k := doc.Resolve(v.Get("K")).(type) {
		case object.Array:
			kids = k
		case nil:
		default:
			kids = object.Array{v.Get("K")}
		}
		line += " :"
		for _, k := range kids {
			line += kidMark(doc, k)
		}
		sb.WriteString(line + "\n")
		for _, k := range kids {
			if isElem(doc, k) {
				walk(k, depth+1)
			}
		}
	}
	root := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("StructTreeRoot"))
	if root != nil {
		switch k := doc.Resolve(root.Get("K")).(type) {
		case object.Array:
			for _, x := range k {
				walk(x, 0)
			}
		default:
			walk(root.Get("K"), 0)
		}
	}
	return sb.String()
}

func kidMark(doc *pdf0.Document, k object.Object) string {
	switch v := doc.Resolve(k).(type) {
	case object.Integer:
		return " #"
	case *object.Dictionary:
		if v.Get("Type") == object.Name("OBJR") {
			return " OBJR"
		}
		if s, ok := v.Get("S").(object.Name); ok {
			return " <" + string(s) + ">"
		}
	}
	return " ?"
}

func isElem(doc *pdf0.Document, k object.Object) bool {
	d := doc.ResolveDict(k)
	return d != nil && d.Get("S") != nil
}

func attributeDicts(doc *pdf0.Document, a object.Object) []*object.Dictionary {
	switch v := doc.Resolve(a).(type) {
	case *object.Dictionary:
		return []*object.Dictionary{v}
	case object.Array:
		var out []*object.Dictionary
		for _, x := range v {
			out = append(out, doc.ResolveDict(x))
		}
		return out
	}
	return nil
}

// TestATaggedPageIsTheDocumentsStructure: the tree is the HTML's, each element
// the type it plays, its kids in the order the document reads them — the
// paragraph's text, the Span the <em> is, more text, the Span with the
// language the French words are in, the Link with its annotation — the
// decorative picture left out and the described one a Figure with its alt
// text, list items with their labels, and table cells with their scope and
// the headers they sit under.
func TestATaggedPageIsTheDocumentsStructure(t *testing.T) {
	res, err := Render(taggedPage(t), Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writtenBack(t, res.Document)
	want := `Document : <H1> <P> <P> <L> <Table>
  H1 : #
  P : # # # # # # <Span> # # # <Span> # # # # <Link> #
    Span : #
    Span /Lang="fr" : # # #
    Link : # # # OBJR
  P : # <Figure>
    Figure /Alt="A rising line" : #
  L /List:/ListNumbering=/Disc : <LI> <LI>
    LI : <Lbl> <LBody>
      Lbl : #
      LBody : #
    LI : <Lbl> <LBody>
      Lbl : #
      LBody : #
  Table : <TR> <TR>
    TR : <TH> <TH>
      TH /ID="q" /Table:/Scope=/Column : #
      TH /ID="r" /Table:/Scope=/Column : #
    TR : <TD> <TD>
      TD /Table:/Headers=[(q)] : #
      TD /Table:/Headers=[(r)] : #
`
	got := outline(back)
	if got != want {
		t.Errorf("the structure tree:\n%s\nwant:\n%s", got, want)
	}
	// The link says what it leads to, in its own words (PDF/UA 7.18.5).
	annots, err := back.PageAnnotations(back.PageRefs()[0])
	if err != nil || len(annots) != 1 {
		t.Fatalf("annotations %v, %v; want the link's one", annots, err)
	}
	if c, _ := back.Resolve(back.ResolveDict(annots[0]).Get("Contents")).(object.String); string(c.Value) != "the report" {
		t.Errorf("the link's /Contents = %v, want its text", back.ResolveDict(annots[0]).Get("Contents"))
	}
}

// paintingOps are the content-stream operators that put ink on the page.
var paintingOps = map[string]bool{
	"f": true, "F": true, "f*": true, "B": true, "B*": true, "b": true, "b*": true,
	"S": true, "s": true, "Tj": true, "TJ": true, "'": true, `"`: true, "Do": true, "sh": true, "BI": true,
}

// unmarkedPainting is every painting operator of a content stream that is in
// no marked-content sequence, and every one inside both a tagged span and an
// artifact, which ISO 14289 forbids either way round (PDF/UA-1 7.1).
func unmarkedPainting(t *testing.T, data []byte) []string {
	t.Helper()
	type open struct{ artifact, tagged bool }
	var stack []open
	var bad []string
	var lastName string
	lx := core.NewContentLexer(core.Canceler{}, data)
	var tok core.ContentTok
	sawDict := false
	for lx.Next(&tok) {
		switch tok.Kind {
		case core.ContentName:
			lastName = tok.Name()
			continue
		case core.ContentDictStart:
			// A property list: tagged content is one with an MCID. One
			// with only /ActualText (a shadow says nothing) is not.
			depth := 1
			for depth > 0 && lx.Next(&tok) {
				switch tok.Kind {
				case core.ContentDictStart:
					depth++
				case core.ContentDictEnd:
					depth--
				case core.ContentName:
					if tok.Name() == "MCID" {
						sawDict = true
					}
				}
			}
			continue
		case core.ContentOperator:
		default:
			continue
		}
		op := string(tok.Raw)
		switch op {
		case "BMC", "BDC":
			o := open{artifact: lastName == "Artifact", tagged: op == "BDC" && sawDict && lastName != "Artifact"}
			if len(stack) > 0 {
				o.artifact = o.artifact || stack[len(stack)-1].artifact
				o.tagged = o.tagged || stack[len(stack)-1].tagged
			}
			stack = append(stack, o)
		case "EMC":
			stack = stack[:len(stack)-1]
		default:
			if paintingOps[op] {
				switch {
				case len(stack) == 0 || !(stack[len(stack)-1].artifact || stack[len(stack)-1].tagged):
					bad = append(bad, op+" outside any marked content")
				case stack[len(stack)-1].artifact && stack[len(stack)-1].tagged:
					bad = append(bad, op+" inside both a tagged span and an artifact")
				}
			}
		}
		sawDict = false
	}
	return bad
}

// TestEverythingDrawnIsContentOrArtifact: on a tagged page every mark is in a
// tagged span or an artifact, and never both, on the plain page and the one
// whose painting is reordered.
func TestEverythingDrawnIsContentOrArtifact(t *testing.T) {
	for name, in := range map[string]Input{"plain": taggedPage(t), "hard": {HTML: hardPage, Fonts: notoSansSet(t)}} {
		res, err := Render(in, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
		if err != nil {
			t.Fatal(err)
		}
		back, _ := writtenBack(t, res.Document)
		if bad := unmarkedPainting(t, []byte(lastPageContent(t, back))); len(bad) != 0 {
			t.Errorf("%s: %v", name, bad)
		}
		// A form is drawn inside a span of the page's, and is part of it:
		// nothing in it is marked again.
		for _, form := range formsOf(t, back, back.ResolveDict(back.PageList()[0].Get("Resources")), 0) {
			if marks := markedSequences(form); len(marks) != 0 {
				t.Errorf("%s: a form drawn on a tagged page marks %v", name, marks)
			}
		}
	}
}

// formsOf is the decoded content of every form XObject a resource dictionary
// names, and those their own resources name.
func formsOf(t *testing.T, doc *pdf0.Document, res *object.Dictionary, depth int) [][]byte {
	t.Helper()
	if res == nil || depth > 8 {
		return nil
	}
	var out [][]byte
	xo := doc.ResolveDict(res.Get("XObject"))
	if xo == nil {
		return nil
	}
	for v := range xo.Values() {
		st, ok := doc.Resolve(v).(*object.Stream)
		if !ok || st.Dict.Get("Subtype") != object.Name("Form") {
			continue
		}
		data, err := doc.StreamData(st)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, data)
		out = append(out, formsOf(t, doc, doc.ResolveDict(st.Dict.Get("Resources")), depth+1)...)
	}
	return out
}

// markedSequences is each Artifact sequence and each one with an MCID that
// a content stream opens.
func markedSequences(data []byte) []string {
	var out []string
	var lastName string
	mcid := false
	lx := core.NewContentLexer(core.Canceler{}, data)
	var tok core.ContentTok
	for lx.Next(&tok) {
		switch tok.Kind {
		case core.ContentName:
			lastName = tok.Name()
			if lastName == "MCID" {
				mcid = true
			}
		case core.ContentOperator:
			switch string(tok.Raw) {
			case "BMC", "BDC":
				if lastName == "Artifact" || mcid {
					out = append(out, string(tok.Raw)+" "+lastName)
				}
			}
			mcid = false
		}
	}
	return out
}

// uaFindings are a validator's findings that say the document is wrong.
func uaFindings(vs []pdfua.Violation) []pdfua.Violation {
	var out []pdfua.Violation
	for _, v := range vs {
		if !pdf0.IsCheckerFinding(v) {
			out = append(out, v)
		}
	}
	return out
}

// TestATaggedPageIsAccessible: a tagged render, declared PDF/UA-2, has nothing
// wrong with it by ValidatePDFUA2; a tagged PDF/A-2a render passes
// ValidatePDFA at 2a and, declared PDF/UA-1, ValidatePDFUA. When PDF0_VERAPDF
// names veraPDF, it passes them as PDF/UA-2, PDF/A-2a and PDF/UA-1.
func TestATaggedPageIsAccessible(t *testing.T) {
	var ua2, a2a []string
	dir := t.TempDir()
	for name, in := range map[string]Input{"plain": taggedPage(t), "hard": {HTML: hardPage, Fonts: notoSansSet(t)}} {
		res, err := Render(in, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Document.DeclarePDFUA(2); err != nil {
			t.Fatal(err)
		}
		back, data := writtenBack(t, res.Document)
		if v := uaFindings(pdf0.ValidatePDFUA2(back)); len(v) != 0 {
			t.Errorf("%s: ValidatePDFUA2: %v", name, v)
		}
		ua2 = append(ua2, writeTo(t, dir, name+"-ua2.pdf", data))

		a, err := Render(in, Options{Page: A4, Tagged: true,
			PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2a, Created: issue357Created}})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Document.DeclarePDFUA(1); err != nil {
			t.Fatal(err)
		}
		back, data = writtenBack(t, a.Document)
		for _, v := range pdf0.ValidatePDFA(back, pdfa.PDFA2a) {
			if v.Rule != "limit" {
				t.Errorf("%s: ValidatePDFA 2a: %v", name, v)
			}
		}
		if v := uaFindings(pdf0.ValidatePDFUA(back)); len(v) != 0 {
			t.Errorf("%s: ValidatePDFUA: %v", name, v)
		}
		a2a = append(a2a, writeTo(t, dir, name+"-a2a.pdf", data))
	}
	if os.Getenv("PDF0_VERAPDF") == "" {
		return
	}
	veraPDFPassesAs(t, "ua2", ua2)
	veraPDFPassesAs(t, "2a", a2a)
	veraPDFPassesAs(t, "ua1", a2a)
}

func writeTo(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestATaggedDocumentHasTheHTMLsTitleAndLanguage: Render's tagged document is
// titled and in the language the HTML says; a lang that is not a language tag
// is left out and reported. An untagged one gets neither, as before.
func TestATaggedDocumentHasTheHTMLsTitleAndLanguage(t *testing.T) {
	res, err := Render(taggedPage(t), Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	cat := res.Document.ResolveDict(res.Document.Trailer.Get("Root"))
	if s, _ := res.Document.Resolve(cat.Get("Lang")).(object.String); string(s.Value) != "en" {
		t.Errorf("/Lang = %v, want (en)", cat.Get("Lang"))
	}
	info := res.Document.ResolveDict(res.Document.Trailer.Get("Info"))
	if title, _ := res.Document.Resolve(info.Get("Title")).(object.String); string(title.Value) != "Quarterly report" {
		t.Errorf("/Title = %v, want the <title>", info.Get("Title"))
	}

	plain, err := Render(taggedPage(t), Options{Page: A4, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	if pc := plain.Document.ResolveDict(plain.Document.Trailer.Get("Root")); pc.Get("Lang") != nil || pc.Get("StructTreeRoot") != nil {
		t.Error("an untagged render got a /Lang or a structure tree")
	}

	bad := Input{HTML: `<html lang="not a tag"><p>x</p></html>`, Fonts: notoSansSet(t)}
	res, err = Render(bad, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasRule(res.Findings, RuleMetadataOmitted, Warn) {
		t.Errorf("a lang that is no language tag was not reported: %v", res.Findings)
	}
	if c := res.Document.ResolveDict(res.Document.Trailer.Get("Root")); c.Get("Lang") != nil {
		t.Errorf("/Lang = %v was written from a lang that is no language tag", c.Get("Lang"))
	}
}

// TestTaggedRenderInto: a tagged page goes into a document with no structure
// tree, with its tree and its link's annotation in it as that document names
// them; a document that has a tree is refused; and an a-level PDF/A document
// takes a tagged page and refuses an untagged one.
func TestTaggedRenderInto(t *testing.T) {
	doc := pdf0.NewDocument()
	if _, err := RenderInto(doc, taggedPage(t), Options{Page: A4, EmbedAllFonts: true}); err != nil {
		t.Fatal(err) // an untagged first page
	}
	res, err := RenderInto(doc, taggedPage(t), Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writtenBack(t, res.Document)
	if got := outline(back); !strings.Contains(got, "OBJR") || !strings.HasPrefix(got, "Document") {
		t.Errorf("the tree RenderInto wrote:\n%s", got)
	}
	refs := back.PageRefs()
	if pg := back.ResolveDict(back.ResolveDict(back.ResolveDict(back.Trailer.Get("Root")).Get("StructTreeRoot")).Get("K")).Get("Pg"); pg != refs[1] {
		t.Errorf("the Document's /Pg = %v, want the second page %v", pg, refs[1])
	}
	if _, err := RenderInto(doc, taggedPage(t), Options{Page: A4, Tagged: true, EmbedAllFonts: true}); !errors.Is(err, ErrTaggedTarget) {
		t.Errorf("a tagged page into a document with a tree: %v, want ErrTaggedTarget", err)
	}

	a, err := pdf0.NewPDFADocumentWith(pdfa.SkeletonOptions{Level: pdfa.PDFA2a, Created: issue357Created, Title: "T", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderInto(a, taggedPage(t), Options{Page: A4}); !errors.Is(err, ErrUnsupportedLevel) {
		t.Errorf("an untagged page into a PDF/A-2a document: %v, want ErrUnsupportedLevel", err)
	}
	if _, err := RenderInto(a, taggedPage(t), Options{Page: A4, Tagged: true}); err != nil {
		t.Errorf("a tagged page into a PDF/A-2a document: %v", err)
	}
	if _, err := Render(taggedPage(t), Options{Page: A4, PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2a}}); !errors.Is(err, ErrUnsupportedLevel) {
		t.Errorf("an untagged PDF/A-2a render: %v, want ErrUnsupportedLevel", err)
	}
}

// veraPDFPassesAs runs veraPDF at one flavour over files, when PDF0_VERAPDF
// names it.
func veraPDFPassesAs(t *testing.T, flavour string, files []string) {
	t.Helper()
	vera := os.Getenv("PDF0_VERAPDF")
	for _, f := range files {
		out, err := exec.Command(vera, "--flavour", flavour, "--format", "text", f).CombinedOutput()
		if !strings.Contains(string(out), "PASS ") {
			t.Errorf("veraPDF --flavour %s does not pass %s (%v):\n%s", flavour, filepath.Base(f), err, out)
		}
	}
}

// TestAReorderedPageIsReadInTheDocumentsOrder: the float, painted first, and
// the positioned boxes, painted last, are where the document has them — the
// one early in the document is read after the heading it follows; ::before
// content leads its heading; a display: contents element keeps its language
// and its link; the hidden paragraph is not in the tree; the table keeps its
// spans and its empty cell; and what a filter draws is one span of the
// filtered element's.
func TestAReorderedPageIsReadInTheDocumentsOrder(t *testing.T) {
	res, err := Render(Input{HTML: hardPage, Fonts: notoSansSet(t)}, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writtenBack(t, res.Document)
	want := `Document : <Div> <H1> <Div> <H2> <P> <L> <BlockQuote> <Div> <Div> <Table> <Div>
  Div : <P>
    P : # # # #
  H1 : # # #
  Div : <P>
    P : # # # # # # # # # # #
  H2 : # # # # <Span>
    Span : #
  P : # # # # # # # # # # <Span> # # # <Link> #
    Span /Lang="de" : # # #
    Link : # # <Span> OBJR OBJR
      Span : #
  L /List:/ListNumbering=/Decimal : <LI> <LI>
    LI : <Lbl> <LBody>
      Lbl : #
      LBody : # <L>
        L /List:/ListNumbering=/Disc : <LI>
          LI : <Lbl> <LBody>
            Lbl : #
            LBody : #
    LI : <Lbl> <LBody>
      Lbl : #
      LBody : #
  BlockQuote : <P>
    P : # # #
  Div : <Figure> <P>
    Figure /Alt="A square" : #
    P : # # #
  Div : <P>
    P : #
  Table : <TR> <TR> <TR>
    TR : <TH> <TH>
      TH /Table:/Scope=/Column /Table:/RowSpan=2 : #
      TH /Table:/Scope=/Column /Table:/ColSpan=2 : #
    TR : <TH> <TH>
      TH /Table:/Scope=/Column : #
      TH /Table:/Scope=/Column : #
    TR : <TD> <TD> <TD>
      TD : #
      TD : #
      TD :
  Div : <P>
    P : #
`
	if got := outline(back); got != want {
		t.Errorf("the structure tree:\n%s\nwant:\n%s", got, want)
	}
}

// TestTextInAGroupingElementIsAParagraph: text a document puts straight into
// a <div>, a <section>, an <article>, a <blockquote> or the <body> is a P in
// the tree — the anonymous block a browser lays it out in — because ISO 32005
// has those hold blocks and no text or Span of their own (veraPDF fails
// Table 5 Document-content-1, Sect-Span-1 and the like otherwise). Text in a
// list item or a table cell stays where it is.
func TestTextInAGroupingElementIsAParagraph(t *testing.T) {
	wrap := func(body string) string {
		return `<html lang="en"><title>x</title><body>` + body + `</body></html>`
	}
	cases := []struct {
		name, html, want string
	}{
		{"div", wrap(`<div>Plain <b>bold</b></div>`), "Document : <Div>\n  Div : <P>\n    P : # # <Span>\n      Span : #\n"},
		{"section", wrap(`<section>Plain <b>bold</b></section>`), "Document : <Sect>\n  Sect : <P>\n    P : # # <Span>\n      Span : #\n"},
		{"article", wrap(`<article>Plain</article>`), "Document : <Art>\n  Art : <P>\n    P : #\n"},
		{"blockquote", wrap(`<blockquote>Plain <b>bold</b></blockquote>`), "Document : <BlockQuote>\n  BlockQuote : <P>\n    P : # # <Span>\n      Span : #\n"},
		{"body", wrap(`Plain <b>bold</b>`), "Document : <P>\n  P : # # <Span>\n    Span : #\n"},
		{"between blocks", wrap(`<div>Lead <p>Para</p> tail</div>`), "Document : <Div>\n  Div : <P> <P> <P>\n    P : #\n    P : #\n    P : #\n"},
		{"li", wrap(`<ul><li>Plain <b>bold</b></li></ul>`), "Document : <L>\n  L /List:/ListNumbering=/Disc : <LI>\n    LI : <Lbl> <LBody>\n      Lbl : #\n      LBody : # # <Span>\n        Span : #\n"},
	}
	var files []string
	dir := t.TempDir()
	for _, c := range cases {
		res, err := Render(Input{HTML: c.html, Fonts: notoSansSet(t)}, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
		if err != nil {
			t.Fatal(err)
		}
		back, _ := writtenBack(t, res.Document)
		if got := outline(back); got != c.want {
			t.Errorf("%s: the structure tree:\n%s\nwant:\n%s", c.name, got, c.want)
		}
		if err := res.Document.DeclarePDFUA(2); err != nil {
			t.Fatal(err)
		}
		files = append(files, writeTo(t, dir, strings.ReplaceAll(c.name, " ", "-")+".pdf", bytesOf(t, res.Document)))
	}
	if os.Getenv("PDF0_VERAPDF") != "" {
		veraPDFPassesAs(t, "ua2", files)
	}
}

// TestHeaderCells: a header cell's scope is what its scope attribute says, and
// otherwise Column in a header row and Row at the start of a row of data; a
// data cell's headers are the IDs of header cells, each in the document
// once, that it names.
func TestHeaderCells(t *testing.T) {
	html := `<html lang="en"><title>x</title><table>
<tr><th id="c1">Region</th><th id="c2" scope="row">Odd</th><th id="dup">A</th></tr>
<tr><th id="r1">North</th><td headers="c1 r1 dup gone">1</td><td id="dup">2</td></tr></table>`
	res, err := Render(Input{HTML: html, Fonts: notoSansSet(t)}, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writtenBack(t, res.Document)
	want := "Document : <Table>\n" +
		"  Table : <TR> <TR>\n" +
		"    TR : <TH> <TH> <TH>\n" +
		"      TH /ID=\"c1\" /Table:/Scope=/Column : #\n" +
		"      TH /Table:/Scope=/Row : #\n" +
		"      TH /Table:/Scope=/Column : #\n" +
		"    TR : <TH> <TD> <TD>\n" +
		"      TH /ID=\"r1\" /Table:/Scope=/Row : #\n" +
		"      TD /Table:/Headers=[(c1) (r1)] : #\n" +
		"      TD : #\n"
	if got := outline(back); got != want {
		t.Errorf("the structure tree:\n%s\nwant:\n%s", got, want)
	}
}

// TestTextIsReadAroundAnInlineStackingContext: an inline box that makes a
// stacking context — transformed, or positioned with a z-index — is painted
// after the text on both sides of it, and is still read between them: the
// paragraph's own text is placed by the text node it came from.
func TestTextIsReadAroundAnInlineStackingContext(t *testing.T) {
	for _, style := range []string{"transform: rotate(10deg); display: inline-block", "position: relative; z-index: 2"} {
		html := `<html lang="en"><title>x</title><p>a <span style="` + style + `">b</span> c</p>`
		res, err := Render(Input{HTML: html, Fonts: notoSansSet(t)}, Options{Page: A4, Tagged: true, EmbedAllFonts: true})
		if err != nil {
			t.Fatal(err)
		}
		back, _ := writtenBack(t, res.Document)
		root := back.ResolveDict(back.ResolveDict(back.Trailer.Get("Root")).Get("StructTreeRoot"))
		p := back.ResolveDict(back.ResolveDict(root.Get("K")).Get("K"))
		var got []string
		for _, k := range back.Resolve(p.Get("K")).(object.Array) {
			if span := back.ResolveDict(k); span != nil {
				got = append(got, fmt.Sprintf("Span(%v)", span.Get("K")))
			} else {
				got = append(got, fmt.Sprint(k))
			}
		}
		// "a", " ", " ", "c" are painted first (0-3), "b" last (4).
		if want := "[0 1 Span(4) 2 3]"; fmt.Sprint(got) != want {
			t.Errorf("%s: the paragraph's kids %v, want %s", style, got, want)
		}
	}
}
