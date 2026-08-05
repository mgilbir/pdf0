package html

import (
	"strings"
	"testing"
)

// The HTML subset reader.
//
// There is no external suite here, and the reason is worth stating rather than
// leaving as an omission. html5lib-tests is the suite every HTML parser is held
// to, and almost all of it asserts what a *recovery rule* produces from
// malformed markup — which is precisely what this engine does not do. Holding a
// refusing parser to a recovering one's expectations would measure the distance
// between two deliberate choices, not correctness. What can honestly be taken
// from outside is the named character reference table, and that is taken: it is
// generated from the standard's own entities.json rather than typed.
//
// So these tests carry the weight, and they are written for it. Every assertion
// is about a consequence — the shape of the tree, or what an author is told —
// and the refusals are tested as carefully as the acceptances, because the
// subset boundary is the design.

// tree renders a document as indented text, so a table reads as the shape being
// asserted rather than as a walk over pointers.
func tree(n *Node) string {
	var b strings.Builder
	var walk func(*Node, int)
	walk = func(cur *Node, depth int) {
		indent := strings.Repeat("  ", depth)
		switch cur.Type {
		case DocumentNode:
			b.WriteString("#document\n")
		case ElementNode:
			b.WriteString(indent + "<" + cur.Name + ">")
			for _, a := range cur.Attrs {
				b.WriteString(" " + a.Name + "=" + quote(a.Value))
			}
			b.WriteString("\n")
		case TextNode:
			b.WriteString(indent + quote(cur.Text) + "\n")
		}
		next := depth + 1
		if cur.Type == DocumentNode {
			next = 0
		}
		for _, c := range cur.Children {
			walk(c, next)
		}
	}
	walk(n, 0)
	return b.String()
}

func quote(s string) string { return "\"" + s + "\"" }

// body renders just the body's contents, which is what most of these are about.
func body(t *testing.T, src string) string {
	t.Helper()
	doc, _, _ := Parse(src)
	el := doc.Element("body")
	if el == nil {
		t.Fatalf("parsing %q produced no body", src)
	}
	var b strings.Builder
	for _, c := range el.Children {
		b.WriteString(tree(c))
	}
	return b.String()
}

func mustParseHTML(t *testing.T, src string) *Node {
	t.Helper()
	doc, errs, ok := Parse(src)
	if !ok {
		t.Fatalf("%q was refused: %v", src, errs)
	}
	return doc
}

// TestDocumentSkeleton pins that every document gets the same frame, whether or
// not the author wrote one. Everything downstream indexes off <html>, <head> and
// <body>, so a fragment that produced a different shape would need every
// consumer to handle both.
func TestDocumentSkeleton(t *testing.T) {
	for _, src := range []string{
		"<p>hi</p>",
		"<html><body><p>hi</p></body></html>",
		"<!DOCTYPE html><html><head></head><body><p>hi</p></body></html>",
		"<!doctype HTML>\n<p>hi</p>\n",
	} {
		doc := mustParseHTML(t, src)
		if doc.Type != DocumentNode {
			t.Errorf("%q: the root is not the document", src)
		}
		if doc.Element("html") == nil || doc.Element("head") == nil || doc.Element("body") == nil {
			t.Errorf("%q did not produce html, head and body:\n%s", src, tree(doc))
			continue
		}
		p := doc.Element("p")
		if p == nil || p.TextContent() != "hi" {
			t.Errorf("%q lost its paragraph:\n%s", src, tree(doc))
		}
	}
}

// TestOptionalEndTags pins HTML's own optional end tags. Leaving out "</li>" is
// correct markup that every template writes, so refusing it would refuse the
// input this engine exists to read — and reading it *wrongly*, by nesting each
// item inside the last, produces a list that indents further with every entry.
func TestOptionalEndTags(t *testing.T) {
	cases := map[string]string{
		"<ul><li>a<li>b</ul>": `<ul>
  <li>
    "a"
  <li>
    "b"
`,
		"<p>a<p>b": `<p>
  "a"
<p>
  "b"
`,
		"<dl><dt>a<dd>b<dt>c</dl>": `<dl>
  <dt>
    "a"
  <dd>
    "b"
  <dt>
    "c"
`,
		"<table><tr><td>a<td>b<tr><td>c</table>": `<table>
  <tr>
    <td>
      "a"
    <td>
      "b"
  <tr>
    <td>
      "c"
`,
	}
	for src, want := range cases {
		if got := body(t, src); got != want {
			t.Errorf("%q\ngot:\n%swant:\n%s", src, got, want)
		}
	}
}

// TestParagraphClosedByBlock pins the rule with the longest list attached: a <p>
// is ended by any block-level start tag. Getting it wrong nests the rest of the
// document inside the paragraph, and every block inherits its styling.
func TestParagraphClosedByBlock(t *testing.T) {
	for _, closer := range []string{"div", "ul", "table", "h1", "blockquote", "pre", "section", "hr"} {
		// <hr> is void and takes no end tag; the rest are closed so that this
		// tests the paragraph rule and not a missing "</div>".
		src := "<p>a<" + closer + ">"
		if closer != "hr" {
			src += "</" + closer + ">"
		}
		doc := mustParseHTML(t, src)
		p := doc.Element("p")
		if p == nil {
			t.Errorf("%q lost its paragraph", src)
			continue
		}
		if p.Element(closer) != nil {
			t.Errorf("%q put the <%s> inside the paragraph:\n%s", src, closer, tree(doc))
		}
	}

	// And an inline start tag does *not* close it.
	doc := mustParseHTML(t, "<p>a<em>b</em></p>")
	if doc.Element("p").Element("em") == nil {
		t.Errorf("<em> was moved out of the paragraph:\n%s", tree(doc))
	}
}

// TestMisnestedTagsAreRefused is the boundary. A browser repairs these; this
// refuses them, because the input is a template its author can fix and a
// silently repaired tree renders almost right, which is the hardest fault to
// notice.
func TestMisnestedTagsAreRefused(t *testing.T) {
	for _, src := range []string{
		"<b><i>x</b></i>",
		"<div><span>x</div>",
		"<p>x</span>",
		"</div>",
		"<div>x",
		"<em>x",
		"<div><div>x</div>",
	} {
		_, errs, ok := Parse(src)
		if ok {
			t.Errorf("%q was accepted, and its tags do not nest", src)
			continue
		}
		if len(errs) == 0 {
			t.Errorf("%q was refused with no explanation", src)
			continue
		}
		// Misnesting is the author's to fix, so it must not be reported as a
		// limit of this engine.
		if errs[0].Unsupported {
			t.Errorf("%q was reported as unsupported (%q); it is malformed",
				src, errs[0].Message)
		}
	}
}

// TestVoidElements pins that void elements take no content and no end tag.
func TestVoidElements(t *testing.T) {
	doc := mustParseHTML(t, "<p>a<br>b<img src=x alt=y>c</p>")
	p := doc.Element("p")
	if p.Element("br") == nil || p.Element("img") == nil {
		t.Fatalf("the void elements are missing:\n%s", tree(doc))
	}
	if n := len(p.Element("br").Children); n != 0 {
		t.Errorf("<br> was given %d children", n)
	}
	if p.TextContent() != "abc" {
		t.Errorf("the text around the void elements is %q, want \"abc\"", p.TextContent())
	}
	// Both spellings of a void element are fine.
	mustParseHTML(t, "<p>a<br/>b</p>")

	// An end tag for one is not.
	if _, _, ok := Parse("<p>a</br>b</p>"); ok {
		t.Error("</br> was accepted, and a void element has no end tag")
	}
}

// TestSelfClosingNonVoidIsRefused pins the trap that moves half a page. HTML has
// no self-closing syntax outside void elements, so a browser reads "<div/>" as
// an open <div> and puts everything after it inside — which looks like a typo's
// worth of markup and is a wholesale change of structure.
func TestSelfClosingNonVoidIsRefused(t *testing.T) {
	for _, src := range []string{"<div/>", "<span/>x", "<p>a<em/>b</p>"} {
		_, errs, ok := Parse(src)
		if ok {
			t.Errorf("%q was accepted", src)
			continue
		}
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "self-closing") {
			t.Errorf("%q was refused with %v, which does not explain the trap", src, errs)
		}
	}
}

// TestScriptAndFriendsAreDropped pins §4.1 of the rendering proposal. These are
// the whole of the code-execution and remote-content surface, and a renderer
// that ignored them quietly would still be one that had read them.
func TestScriptAndFriendsAreDropped(t *testing.T) {
	cases := []struct{ src, gone string }{
		{"<p>a</p><script>var x = 1 < 2;</script><p>b</p>", "script"},
		{"<p>a</p><iframe src=http://example.com></iframe><p>b</p>", "iframe"},
		{"<p>a</p><object data=x></object><p>b</p>", "object"},
		{"<p>a</p><embed src=x><p>b</p>", "embed"},
	}
	for _, tc := range cases {
		doc, errs, ok := Parse(tc.src)
		if ok {
			t.Errorf("%q was accepted silently; a dropped element must be reported", tc.src)
		}
		if doc.Element(tc.gone) != nil {
			t.Errorf("%q kept the <%s>:\n%s", tc.src, tc.gone, tree(doc))
		}
		// Dropping is a limit of this engine, not a fault in the markup.
		if len(errs) == 0 || !errs[0].Unsupported {
			t.Errorf("%q: <%s> was not reported as unsupported: %v", tc.src, tc.gone, errs)
		}
		// The document either side of it survives.
		if got := doc.Element("body").TextContent(); got != "ab" {
			t.Errorf("%q left the body as %q, want \"ab\"", tc.src, got)
		}
	}
}

// TestScriptContentIsNotMarkup pins that a dropped script's body is skipped as
// raw text. "var x = 1 < 2" holds a "<" that is not a tag, and a reader that
// tokenized it would report a phantom error or swallow the rest of the page.
func TestScriptContentIsNotMarkup(t *testing.T) {
	// The body holds three things that would each be noticed if it were read as
	// markup: a "<" that cannot begin a tag, an apparent end tag inside a
	// string, and an apparent start tag. A body that merely *looks* like markup
	// is not enough — "a<b" tokenizes cleanly as a start tag, so a test using
	// only that passes even when the raw-text handling is removed.
	doc, errs, _ := Parse(`<p>a</p><script>if (a < 1) { x("</p><div>") }</script><p>b</p>`)
	if got := doc.Element("body").TextContent(); got != "ab" {
		t.Errorf("the body is %q, want \"ab\" — the script body was read as markup", got)
	}
	// Exactly one complaint: the dropped script. Anything else means its
	// contents were tokenized.
	if len(errs) != 1 {
		t.Errorf("got %d problems, want only the dropped <script>: %v", len(errs), errs)
	}
}

// TestUnknownElementsAreRefused pins that an element this engine cannot lay out
// is reported rather than treated as a generic box. Rendering <fancy-callout> as
// a span produces a page that looks nearly right, so nothing signals that the
// thing the author cared about was ignored.
func TestUnknownElementsAreRefused(t *testing.T) {
	for _, src := range []string{
		"<fancy-callout>x</fancy-callout>",
		"<blink>x</blink>",
		"<my-widget/>",
	} {
		_, errs, ok := Parse(src)
		if ok {
			t.Errorf("%q was accepted", src)
			continue
		}
		if len(errs) == 0 || !errs[0].Unsupported {
			t.Errorf("%q was not reported as unsupported: %v", src, errs)
		}
	}
}

// TestAttributes pins the shapes an attribute can be written in, and that the
// name is folded while the value is not.
func TestAttributes(t *testing.T) {
	doc := mustParseHTML(t,
		`<a HREF="x" title='y' data-n=3 hidden CLASS="a b">t</a>`)
	a := doc.Element("a")
	if a == nil {
		t.Fatalf("no anchor:\n%s", tree(doc))
	}
	want := map[string]string{
		"href": "x", "title": "y", "data-n": "3", "hidden": "", "class": "a b",
	}
	for name, wantV := range want {
		got, ok := a.Attr(name)
		if !ok {
			t.Errorf("the attribute %q is missing", name)
			continue
		}
		if got != wantV {
			t.Errorf("%s = %q, want %q", name, got, wantV)
		}
	}
	// The value's case is the author's; only the name is folded.
	doc = mustParseHTML(t, `<a href="CaseMatters">t</a>`)
	if v, _ := doc.Element("a").Attr("href"); v != "CaseMatters" {
		t.Errorf("the value was folded to %q", v)
	}
}

// TestDuplicateAttributeIsRefused pins that a repeated attribute is an error
// rather than something to resolve. HTML keeps the first and drops the rest, so
// a template with two class attributes silently loses one — and which one is
// lost is not visible in the output.
func TestDuplicateAttributeIsRefused(t *testing.T) {
	_, errs, ok := Parse(`<p class="a" class="b">x</p>`)
	if ok {
		t.Error("a repeated attribute was accepted")
	}
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "twice") {
		t.Errorf("got %v, want a complaint about the repetition", errs)
	}
}

// TestUnquotedAttributeValues pins that the characters which make the end of an
// unquoted value ambiguous are refused rather than guessed at.
func TestUnquotedAttributeValues(t *testing.T) {
	mustParseHTML(t, "<a href=x.html>t</a>")
	mustParseHTML(t, "<a href=/a/b>t</a>")
	mustParseHTML(t, "<a href=#frag>t</a>")

	for _, src := range []string{
		`<a href=a"b>t</a>`,
		"<a href=a'b>t</a>",
		"<a href=a<b>t</a>",
		// "=" is on the standard's own list of characters an unquoted value
		// may not hold, so a query string has to be quoted.
		"<a href=a=b>t</a>",
		"<a href=/a/b?c=1>t</a>",
		"<a href=a`b>t</a>",
	} {
		if _, _, ok := Parse(src); ok {
			t.Errorf("%q was accepted; the end of the value is ambiguous", src)
		}
	}
}

// TestCharacterReferences pins the table generated from the standard's own
// entities.json, and the numeric forms beside it.
func TestCharacterReferences(t *testing.T) {
	cases := map[string]string{
		"<p>&amp;</p>":    "&",
		"<p>&lt;&gt;</p>": "<>",
		"<p>&quot;</p>":   `"`,
		"<p>&apos;</p>":   "'",
		"<p>&nbsp;</p>":   " ",
		"<p>&copy;</p>":   "©",
		"<p>&hellip;</p>": "…",
		"<p>&mdash;</p>":  "—",
		"<p>&#65;</p>":    "A",
		"<p>&#x41;</p>":   "A",
		"<p>&#X41;</p>":   "A",
		"<p>&#8212;</p>":  "—",
		"<p>&#x2014;</p>": "—",
		"<p>a&amp;b</p>":  "a&b",
		// A name that is a prefix of a longer one resolves to itself.
		"<p>&not;</p>": "¬",
		// One of the two-code-point entries.
		"<p>&NotEqualTilde;</p>": "≂̸",
		// References do not nest: this is the text "&amp;", not an ampersand.
		"<p>&amp;amp;</p>": "&amp;",
	}
	for src, want := range cases {
		doc := mustParseHTML(t, src)
		if got := doc.Element("p").TextContent(); got != want {
			t.Errorf("%q gave %q, want %q", src, got, want)
		}
	}
}

// TestCharacterReferencesInAttributes pins that they are resolved there too,
// which is easy to forget and produces URLs with a literal "&amp;" in them.
func TestCharacterReferencesInAttributes(t *testing.T) {
	doc := mustParseHTML(t, `<a href="x?a=1&amp;b=2">t</a>`)
	if got, _ := doc.Element("a").Attr("href"); got != "x?a=1&b=2" {
		t.Errorf("href is %q, want the reference resolved", got)
	}
}

// TestBadCharacterReferencesAreRefused pins the ones that are not characters. A
// document asking for a surrogate half is a document built wrong — most often by
// something that encoded UTF-16 as if it were code points — and substituting
// U+FFFD would put a replacement character in the page with nothing to say where
// it came from.
func TestBadCharacterReferencesAreRefused(t *testing.T) {
	for _, src := range []string{
		"<p>&#xD800;</p>",
		"<p>&#55296;</p>",
		"<p>&#x110000;</p>",
		"<p>&#0;</p>",
		"<p>&#;</p>",
		"<p>&#x;</p>",
		"<p>&nosuchentity;</p>",
		"<p>&#999999999999999;</p>",
	} {
		if _, _, ok := Parse(src); ok {
			t.Errorf("%q was accepted", src)
		}
	}
}

// TestLoneAmpersandIsTextButLegacyNamesAreReported pins the one place a browser
// and this engine could silently differ. An "&" that begins nothing is literal
// in both. An "&" that begins one of the 106 historical semicolon-less names is
// a character reference in a browser and literal here, so it is reported — and
// only then, so that "?a=1&b=2" in a URL stays quiet.
func TestLoneAmpersandIsTextButLegacyNamesAreReported(t *testing.T) {
	quiet := map[string]string{
		"<p>a & b</p>":              "a & b",
		"<p>a&b</p>":                "a&b",
		`<a href="x?a=1&b=2">t</a>`: "",
		"<p>R&amp;D</p>":            "R&D",
	}
	for src, wantText := range quiet {
		doc, errs, ok := Parse(src)
		if !ok {
			t.Errorf("%q was refused: %v", src, errs)
			continue
		}
		if wantText != "" {
			if got := doc.Element("p").TextContent(); got != wantText {
				t.Errorf("%q gave %q, want %q", src, got, wantText)
			}
		}
	}

	// "&not" without its semicolon is where the two differ, so it is reported.
	_, errs, ok := Parse("<p>&notit;</p>")
	if ok {
		t.Error("\"&notit;\" was accepted silently, and a browser reads it as \"¬it;\"")
	}
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not") {
		t.Errorf("got %v, want a complaint naming the historical entity", errs)
	}
}

// TestRawTextAndRCDATA pins the two elements whose content is not markup.
func TestRawTextAndRCDATA(t *testing.T) {
	// <style> is raw: "&" and "<" mean nothing in it.
	doc := mustParseHTML(t, "<style>a > b { content: \"&amp;\" }</style><p>x</p>")
	st := doc.Element("style")
	if st == nil {
		t.Fatalf("no style element:\n%s", tree(doc))
	}
	if got := st.TextContent(); got != `a > b { content: "&amp;" }` {
		t.Errorf("the stylesheet is %q; its content was not read raw", got)
	}

	// <title> is RCDATA: no markup, but references resolve.
	doc = mustParseHTML(t, "<title>A &amp; B</title><p>x</p>")
	if got := doc.Element("title").TextContent(); got != "A & B" {
		t.Errorf("the title is %q, want \"A & B\"", got)
	}
}

// TestHeadAndBody pins where things land. A <style> is legal in the body and is
// left there, because moving it to the head would change the order stylesheets
// apply in, and so which declaration wins.
func TestHeadAndBody(t *testing.T) {
	doc := mustParseHTML(t,
		"<title>t</title><meta charset=utf-8><p>a</p><style>x{}</style>")

	head := doc.Element("head")
	if head.Element("title") == nil || head.Element("meta") == nil {
		t.Errorf("the title and meta are not in the head:\n%s", tree(doc))
	}
	bodyEl := doc.Element("body")
	if bodyEl.Element("p") == nil {
		t.Errorf("the paragraph is not in the body:\n%s", tree(doc))
	}
	if bodyEl.Element("style") == nil {
		t.Errorf("the body's <style> was moved, which would reorder the cascade:\n%s", tree(doc))
	}
}

// TestTextIsMerged pins that no element ever has two text children in a row — a
// shape every consumer would otherwise have to handle, and which a comment
// between two runs would otherwise produce.
func TestTextIsMerged(t *testing.T) {
	doc := mustParseHTML(t, "<p>a<!-- c -->b</p>")
	p := doc.Element("p")
	if len(p.Children) != 1 {
		t.Errorf("the paragraph has %d children, want one merged text node:\n%s",
			len(p.Children), tree(doc))
	}
	if got := p.TextContent(); got != "ab" {
		t.Errorf("the text is %q, want \"ab\"", got)
	}
}

// TestOffsetsPointAtTheSource is what lets a finding from layout name the markup
// that caused it, which §6 of the rendering proposal needs and which cannot be
// recovered later.
func TestOffsetsPointAtTheSource(t *testing.T) {
	const src = "<div>\n  <p class=x>hello</p>\n</div>"
	doc := mustParseHTML(t, src)

	div := doc.Element("div")
	if got := src[div.Offset : div.Offset+5]; got != "<div>" {
		t.Errorf("the div's offset points at %q", got)
	}
	p := doc.Element("p")
	if got := src[p.Offset : p.Offset+2]; got != "<p" {
		t.Errorf("the paragraph's offset points at %q", got)
	}
	if p.Offset <= div.Offset {
		t.Error("the paragraph does not begin after the div")
	}
}

// TestNestingIsBounded is the security property: a template is untrusted, and a
// few kilobytes of "<div>" must not exhaust the stack of anything that walks the
// tree.
func TestNestingIsBounded(t *testing.T) {
	deep := maxDepth * 4
	src := strings.Repeat("<div>", deep) + "x" + strings.Repeat("</div>", deep)

	doc, errs, ok := Parse(src)
	if ok {
		t.Errorf("a tree nested %d deep was accepted", deep)
	}
	if len(errs) == 0 {
		t.Error("nesting past the cap was refused with no explanation")
	}
	if got := depthOfTree(doc); got > maxDepth+4 {
		t.Errorf("built a tree %d deep, past the cap of %d", got, maxDepth)
	}
}

func depthOfTree(n *Node) int {
	if n == nil {
		return 0
	}
	best := 0
	// Iterative, because it is checking a depth bound.
	type frame struct {
		n *Node
		d int
	}
	stack := []frame{{n, 1}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.d > best {
			best = f.d
		}
		for _, c := range f.n.Children {
			stack = append(stack, frame{c, f.d + 1})
		}
	}
	return best
}

// TestParseIsTotal is the property that matters most for untrusted input: every
// byte sequence produces a result, and every one terminates.
func TestParseIsTotal(t *testing.T) {
	inputs := []string{
		"", "<", ">", "</", "<>", "</>", "<!", "<!-", "<!--", "<!---",
		"<?", "<![CDATA[", "&", "&#", "&#x", "&;", "&#;", "<p", "<p ", "<p a",
		"<p a=", "<p a='", `<p a="`, "<p/", "<style>", "<title>", "<script>",
		"\x00", "\xff\xfe", "<p>\x00</p>",
		strings.Repeat("<div>", 5000),
		strings.Repeat("</div>", 5000),
		strings.Repeat("<p>", 5000),
		strings.Repeat("&", 5000),
		strings.Repeat("&amp;", 5000),
		strings.Repeat("<", 5000),
		strings.Repeat("<li>", 5000),
	}
	for _, in := range inputs {
		doc, _, _ := Parse(in)
		if doc == nil {
			t.Errorf("%.20q produced no document at all", in)
			continue
		}
		doc.TextContent()
		doc.Walk(func(*Node) bool { return true })
	}
}

// TestErrorsAreBounded pins that a hostile document cannot produce an unbounded
// diagnostic list.
func TestErrorsAreBounded(t *testing.T) {
	_, errs, _ := Parse(strings.Repeat("<nosuchelement>", maxErrors*4))
	if len(errs) > maxErrors+1 {
		t.Errorf("got %d problems, want at most %d and a note", len(errs), maxErrors)
	}
	if len(errs) == 0 {
		t.Fatal("a document of nothing but unknown elements reported nothing")
	}
	if last := errs[len(errs)-1].Message; !strings.Contains(last, "not reported") {
		t.Errorf("the last entry is %q; a truncated list must say it was truncated", last)
	}
}

// TestWellFormedDocumentIsQuiet is the other side of refusal. A document inside
// the subset produces the tree it describes and no complaints — a reader that
// warns about correct input trains its users to ignore it.
func TestWellFormedDocumentIsQuiet(t *testing.T) {
	const src = `<!DOCTYPE html>
<html>
  <head>
    <title>A document</title>
    <meta charset="utf-8">
    <link rel="stylesheet" href="a.css">
  </head>
  <body>
    <header><h1>Title &mdash; subtitle</h1></header>
    <main>
      <p>Some <em>emphasised</em> and <strong>strong</strong> text, with a
         <a href="https://example.com/?a=1&amp;b=2">link</a> and an
         <img src="x.png" alt="image">.</p>
      <ul><li>one<li>two<li>three</ul>
      <table>
        <caption>A table</caption>
        <thead><tr><th>h1<th>h2</thead>
        <tbody><tr><td>a<td>b<tr><td>c<td>d</tbody>
      </table>
      <blockquote cite="x"><p>Quoted.</p></blockquote>
      <pre><code>1 &lt; 2</code></pre>
    </main>
    <footer><p>&copy; 2026</p></footer>
  </body>
</html>
`
	doc, errs, ok := Parse(src)
	if !ok {
		t.Fatalf("a well-formed document was refused: %v", errs)
	}
	if len(errs) != 0 {
		t.Errorf("a well-formed document reported %d problems: %v", len(errs), errs)
	}
	if got := doc.Element("title").TextContent(); got != "A document" {
		t.Errorf("the title is %q", got)
	}
	if doc.Element("h1").TextContent() != "Title — subtitle" {
		t.Errorf("the heading is %q", doc.Element("h1").TextContent())
	}
	// The list's three items are siblings, not nested.
	ul := doc.Element("ul")
	if n := len(ul.Children); n != 3 {
		t.Errorf("the list has %d children, want 3:\n%s", n, tree(ul))
	}
	if got := doc.Element("code").TextContent(); got != "1 < 2" {
		t.Errorf("the code sample is %q, want \"1 < 2\"", got)
	}
	if v, _ := doc.Element("a").Attr("href"); v != "https://example.com/?a=1&b=2" {
		t.Errorf("the link is %q", v)
	}
}
