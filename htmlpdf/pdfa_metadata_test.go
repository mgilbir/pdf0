package htmlpdf

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// described is what a written PDF/A document says about itself: its XMP
// packet, as text, and its catalog's /Lang.
func described(t *testing.T, doc *pdf0.Document) (packet, lang string) {
	t.Helper()
	back, _ := writtenBack(t, doc)
	cat := back.ResolveDict(back.Trailer.Get("Root"))
	meta, ok := back.Resolve(cat.Get("Metadata")).(*object.Stream)
	if !ok {
		t.Fatal("the document has no metadata stream")
	}
	l, _ := cat.Get("Lang").(object.String)
	for _, v := range pdf0.ValidatePDFA(back, mustClaim(t, back)) {
		t.Errorf("the document does not pass its level: %v", v)
	}
	return string(meta.Data), string(l.Value)
}

func mustClaim(t *testing.T, doc *pdf0.Document) pdfa.Level {
	t.Helper()
	level, ok := doc.Conformance()
	if !ok {
		t.Fatal("the document claims no PDF/A level")
	}
	return level
}

const (
	xmpTitle  = `<dc:title><rdf:Alt><rdf:li xml:lang="x-default">`
	xmpAuthor = `<dc:creator><rdf:Seq><rdf:li>`
)

func metadataPage(head, lang string) Input {
	return Input{HTML: `<html` + lang + `><head>` + head + `</head><body><p>x</p></body></html>`}
}

// TestAPDFARenderTakesItsMetadataFromTheDocument: with Options.PDFA naming a
// level and nothing else, the document's title, language and first author
// become the PDF's dc:title, /Lang and dc:creator, and the file still passes
// its level.
func TestAPDFARenderTakesItsMetadataFromTheDocument(t *testing.T) {
	in := metadataPage(`<title>  Factuur
  7 </title><meta name="Author" content="Boekhouding"><meta name="author" content="Second">`, ` lang="nl-BE"`)
	in.Fonts = notoSansSet(t)
	out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}})
	if err != nil {
		t.Fatal(err)
	}
	packet, lang := described(t, out.Document)
	if !strings.Contains(packet, xmpTitle+"Factuur 7<") {
		t.Errorf("dc:title is not the document's title, whitespace collapsed:\n%s", packet)
	}
	if !strings.Contains(packet, xmpAuthor+"Boekhouding<") {
		t.Errorf("dc:creator is not the first <meta name=author>:\n%s", packet)
	}
	if lang != "nl-BE" {
		t.Errorf("/Lang is %q; want the document's nl-BE", lang)
	}
	for _, f := range out.Findings {
		if f.Rule == RuleMetadataOmitted {
			t.Errorf("nothing was left out, but: %v", f)
		}
	}
}

// TestTheCallersMetadataWins: a value in Options.PDFA is the one written,
// whatever the document says.
func TestTheCallersMetadataWins(t *testing.T) {
	in := metadataPage(`<title>Document</title><meta name="author" content="Document">`, ` lang="nl"`)
	in.Fonts = notoSansSet(t)
	out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b, Title: "Caller", Author: "Caller", Language: "en"}})
	if err != nil {
		t.Fatal(err)
	}
	packet, lang := described(t, out.Document)
	if !strings.Contains(packet, xmpTitle+"Caller<") || !strings.Contains(packet, xmpAuthor+"Caller<") ||
		strings.Contains(packet, ">Document<") || lang != "en" {
		t.Errorf("/Lang %q and packet:\n%s\nwant the caller's title, author and language", lang, packet)
	}
}

// TestMetadataTheFileCannotHoldIsLeftOutAndSaid: a lang that is not a
// language tag, and a title or author with a character XMP cannot carry, are
// left out of a document that is still made, and one finding says which. An
// explicit lang="" is no language and says nothing. A policy can refuse such a
// document or silence the finding.
func TestMetadataTheFileCannotHoldIsLeftOutAndSaid(t *testing.T) {
	for _, tc := range []struct {
		name, head, lang, want string
		absent                 []string
	}{
		{"a lang that is not a tag", `<title>Fine</title>`, ` lang="nl_NL"`, `the language "nl_NL"`, nil},
		{"a C0 control in the title", `<title>a&#1;b</title>`, ``, `the title`, []string{xmpTitle}},
		{"a noncharacter in the author", `<meta name="author" content="a&#xFFFE;b">`, ``, `the author`, []string{xmpAuthor}},
	} {
		in := metadataPage(tc.head, tc.lang)
		in.Fonts = notoSansSet(t)
		out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}})
		if err != nil {
			t.Errorf("%s: %v; the document should be made without it", tc.name, err)
			continue
		}
		var found *layout.Finding
		for i, f := range out.Findings {
			if f.Rule == RuleMetadataOmitted {
				found = &out.Findings[i]
			}
		}
		if found == nil || found.Severity != layout.Warn || !strings.Contains(found.Message, tc.want) {
			t.Errorf("%s: finding %v; want a %s warning naming %s", tc.name, found, RuleMetadataOmitted, tc.want)
		}
		packet, lang := described(t, out.Document)
		if tc.lang != "" && lang != "" {
			t.Errorf("%s: /Lang %q was written", tc.name, lang)
		}
		for _, a := range tc.absent {
			if strings.Contains(packet, a) {
				t.Errorf("%s: %s was written:\n%s", tc.name, a, packet)
			}
		}
	}

	in := metadataPage(`<title>Fine</title>`, ` lang=""`)
	in.Fonts = notoSansSet(t)
	out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out.Findings {
		if f.Rule == RuleMetadataOmitted {
			t.Errorf(`lang="" was reported: %v`, f)
		}
	}
	if _, lang := described(t, out.Document); lang != "" {
		t.Errorf(`lang="" wrote /Lang %q`, lang)
	}

	bad := metadataPage(`<title>Fine</title>`, ` lang="nl_NL"`)
	bad.Fonts = notoSansSet(t)
	bad.Policy = layout.Policy{RuleMetadataOmitted: layout.Error}
	if _, err := Render(bad, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}}); !isRefused(err) {
		t.Errorf("a policy raising %s to Error rendered: %v", RuleMetadataOmitted, err)
	}
	bad.Policy = layout.Policy{RuleMetadataOmitted: layout.Ignore}
	out, err = Render(bad, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out.Findings {
		if f.Rule == RuleMetadataOmitted {
			t.Errorf("a policy ignoring %s still reported: %v", RuleMetadataOmitted, f)
		}
	}
}

func isRefused(err error) bool {
	_, ok := err.(*RefusedError)
	return ok
}

// TestOnlyAPDFARenderTakesTheDocumentsMetadata: a plain render writes no
// metadata, as it never has, and RenderInto leaves the caller's document's
// metadata as it was: one page's title is not the document's.
func TestOnlyAPDFARenderTakesTheDocumentsMetadata(t *testing.T) {
	in := metadataPage(`<title>Page title</title><meta name="author" content="Page author">`, ` lang="nl"`)
	in.Fonts = notoSansSet(t)
	out, err := Render(in, Options{})
	if err != nil {
		t.Fatal(err)
	}
	back, data := writtenBack(t, out.Document)
	if cat := back.ResolveDict(back.Trailer.Get("Root")); cat.Get("Metadata") != nil || cat.Get("Lang") != nil {
		t.Errorf("a plain render wrote /Metadata or /Lang: %v", cat)
	}
	if bytes.Contains(data, []byte("Page title")) {
		t.Error("a plain render wrote the document's title somewhere")
	}

	doc := pdfaDoc(t, pdfa.PDFA2b)
	if _, err := RenderInto(doc, in, Options{}); err != nil {
		t.Fatal(err)
	}
	packet, lang := described(t, doc)
	if strings.Contains(packet, "Page title") || strings.Contains(packet, "Page author") || lang != "" {
		t.Errorf("RenderInto changed the document's metadata: /Lang %q\n%s", lang, packet)
	}
}

// TestADocumentDescribedByItsHTMLPassesVeraPDF: the metadata taken from the
// document is metadata veraPDF accepts, at every level a PDF/A render writes.
func TestADocumentDescribedByItsHTMLPassesVeraPDF(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, l := range renderIntoLevels {
		in := metadataPage(`<title>Factuur 7</title><meta name="author" content="Boekhouding">`, ` lang="nl"`)
		in.Fonts = notoSansSet(t)
		out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: l.level}})
		if err != nil {
			t.Fatalf("%s: %v", l.level, err)
		}
		var b bytes.Buffer
		if err := out.Document.Save(&b); err != nil {
			t.Errorf("%s: Save refused it: %v", l.level, err)
			continue
		}
		files = append(files, writeFlavour(t, dir, l.flavour, b.Bytes()))
	}
	veraPDFPasses(t, files)
}

// writeFlavour writes data as dir/flavour/<flavour>.pdf, the name
// veraPDFPasses reads the flavour from.
func writeFlavour(t *testing.T, dir, flavour string, data []byte) string {
	t.Helper()
	sub := filepath.Join(dir, flavour)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, flavour+".pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestThePDFAOptionsAreCheckedBeforeLayout: an Options.PDFA that cannot make a
// document fails before the page is laid out, as it did when the document was
// made first. Under a context already cancelled, the error is the options',
// which it could not be if layout had run and stopped on the context.
func TestThePDFAOptionsAreCheckedBeforeLayout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := metadataPage(`<title>x</title>`, ``)
	in.Fonts = notoSansSet(t)
	_, err := RenderContext(ctx, in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b, Language: "not a tag"}}, RunLimits{})
	if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "Language") {
		t.Errorf("err %v; want the caller's invalid Language, reported before layout", err)
	}
}
