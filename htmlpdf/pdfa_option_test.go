package htmlpdf

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/internal/xmp"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// issue357 is the page #357 asks a PDF/A-2b render of: embedded subset
// TrueType text through @font-face, a sans-serif and a monospace run, an RGB
// fill with opacity below 1, and a PNG with alpha.
func issue357(t *testing.T) Input {
	t.Helper()
	return Input{
		HTML: notoFace + `<p style="font-family: Doc">Embedded subset TrueType, through @font-face.</p>
<p style="font-family: sans-serif">A sans-serif run, <b>bold</b>.</p>
<p style="font-family: monospace">A monospace run 0123</p>
<div style="width: 200px; height: 40px; background: rgb(200, 30, 30); opacity: 0.5"></div>
<img src="a.png" style="width: 120px">`,
		Resources: bytesResolver{"noto.ttf": notoSansBytes(t), "a.png": pngBytes(t)},
		// The generic families answered by an embedded face, as a caller
		// rendering for PDF/A supplies them.
		Fonts: notoSansSet(t),
	}
}

var issue357Created = time.Date(2026, 10, 27, 9, 30, 0, 0, time.FixedZone("", 3600))

func issue357Options() Options {
	return Options{Page: A4, PDFA: pdfa.SkeletonOptions{
		Level: pdfa.PDFA2b, Title: "Factuur 2026-0042", Author: "Boekhouding",
		Language: "nl", Created: issue357Created,
	}}
}

// writtenBack is doc written and read back, which is what a reader and a
// validator see.
func writtenBack(t *testing.T, doc *pdf0.Document) (*pdf0.Document, []byte) {
	t.Helper()
	data := bytesOf(t, doc)
	back, err := pdf0.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return back, data
}

// lastPageContent is the decoded content stream of a document's last page: what is
// drawn, and where.
func lastPageContent(t *testing.T, doc *pdf0.Document) string {
	t.Helper()
	pages := doc.PageList()
	stream, ok := doc.Resolve(pages[len(pages)-1].Get("Contents")).(*object.Stream)
	if !ok {
		t.Fatal("the page's /Contents is not a stream")
	}
	data, err := doc.StreamData(stream)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestIssue357APDFA2bRender is #357's acceptance, through Options.PDFA and
// through RenderInto. The document passes ValidatePDFA at PDF/A-2b with no
// violation, and veraPDF --flavour 2b when PDF0_VERAPDF names it. Its metadata
// is what was asked for, and its page is drawn exactly as the plain render's,
// so the layout is unchanged.
func TestIssue357APDFA2bRender(t *testing.T) {
	plain, err := Render(issue357(t), Options{Page: A4})
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed := plain.Document.Conformance(); claimed {
		t.Fatal("a render with no PDFA claims a level")
	}
	plainBack, _ := writtenBack(t, plain.Document)

	viaOption, err := Render(issue357(t), issue357Options())
	if err != nil {
		t.Fatalf("Render with PDFA: %v", err)
	}
	viaInto, err := pdf0.NewPDFADocumentWith(issue357Options().PDFA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RenderInto(viaInto, issue357(t), Options{Page: A4}); err != nil {
		t.Fatalf("RenderInto: %v", err)
	}

	dir := t.TempDir()
	var files []string
	for name, doc := range map[string]*pdf0.Document{"option": viaOption.Document, "into": viaInto} {
		back, data := writtenBack(t, doc)
		if level, ok := back.Conformance(); !ok || level != pdfa.PDFA2b {
			t.Errorf("%s: claims %v (%v); want PDF/A-2b", name, level, ok)
		}
		if back.Version != "1.7" {
			t.Errorf("%s: PDF %s; PDF/A-2 is PDF 1.7", name, back.Version)
		}
		for _, v := range pdf0.ValidatePDFA(back, pdfa.PDFA2b) {
			t.Errorf("%s: %v", name, v)
		}

		cat := back.ResolveDict(back.Trailer.Get("Root"))
		if lang, _ := cat.Get("Lang").(object.String); string(lang.Value) != "nl" {
			t.Errorf("%s: /Lang %v; want nl", name, cat.Get("Lang"))
		}
		meta, _ := back.Resolve(cat.Get("Metadata")).(*object.Stream)
		p, err := xmp.Parse(meta.Data)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := p.Text(xmp.NSXMP, "CreateDate"); got != issue357Created.Format(time.RFC3339) {
			t.Errorf("%s: xmp:CreateDate %q; want %s", name, got, issue357Created.Format(time.RFC3339))
		}
		if got := string(meta.Data); !bytes.Contains([]byte(got), []byte("Factuur 2026-0042")) {
			t.Errorf("%s: the XMP does not carry the title", name)
		}

		if lastPageContent(t, back) != lastPageContent(t, plainBack) {
			t.Errorf("%s: the page is drawn differently from the plain render's; the layout is to be unchanged", name)
		}

		path := filepath.Join(dir, name, "2b.pdf")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	veraPDFPasses(t, files)
}

// TestOptionsPDFAIsAllOrNothing: a PDFA with fields set and no Level is an
// error rather than a plain document; a level RenderInto does not write is
// refused; RenderInto refuses a PDFA, whose document is the caller's; and a
// PDFA left with its creation date zero is dated now, as the skeleton dates it.
func TestOptionsPDFAIsAllOrNothing(t *testing.T) {
	in := Input{HTML: "<p>x</p>", Fonts: notoSansSet(t)}
	for _, partial := range []pdfa.SkeletonOptions{
		{Title: "no level"}, {Language: "nl"}, {FileID: []byte("0123456789abcdef")},
		{Created: issue357Created},
	} {
		if _, err := Render(in, Options{PDFA: partial}); !errors.Is(err, ErrPDFAWithoutLevel) {
			t.Errorf("PDFA %+v: err %v; want ErrPDFAWithoutLevel", partial, err)
		}
	}
	for _, level := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a} {
		if _, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: level}}); !errors.Is(err, ErrUnsupportedLevel) {
			t.Errorf("%s: err %v; want ErrUnsupportedLevel", level, err)
		}
	}
	doc := pdf0.NewDocument()
	before := bytesOf(t, doc)
	if _, err := RenderInto(doc, in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}}); err == nil {
		t.Error("RenderInto accepted an Options.PDFA")
	}
	if !bytes.Equal(before, bytesOf(t, doc)) {
		t.Error("RenderInto refused a PDFA and changed the document anyway")
	}

	before357 := time.Now().Truncate(time.Second)
	out, err := Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA4}})
	if err != nil {
		t.Fatal(err)
	}
	back, _ := writtenBack(t, out.Document)
	cat := back.ResolveDict(back.Trailer.Get("Root"))
	meta, _ := back.Resolve(cat.Get("Metadata")).(*object.Stream)
	p, err := xmp.Parse(meta.Data)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := p.Text(xmp.NSXMP, "CreateDate")
	when, err := time.Parse(time.RFC3339, got)
	if err != nil || when.Before(before357) || when.After(time.Now()) {
		t.Errorf("a PDFA with no Created is dated %q; want the time of the render", got)
	}
}
