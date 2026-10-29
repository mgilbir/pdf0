package htmlpdf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/forme/shape"
	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// The levels RenderInto writes pages for, with the name veraPDF's --flavour
// gives each.
var renderIntoLevels = []struct {
	level   pdfa.Level
	flavour string
}{
	{pdfa.PDFA2b, "2b"}, {pdfa.PDFA2u, "2u"}, {pdfa.PDFA3b, "3b"}, {pdfa.PDFA3u, "3u"},
	{pdfa.PDFA4, "4"}, {pdfa.PDFA4E, "4e"}, {pdfa.PDFA4F, "4f"},
}

// pdfaDoc is a document claiming level, dated so that two of them are the
// same bytes.
func pdfaDoc(t *testing.T, level pdfa.Level) *pdf0.Document {
	t.Helper()
	doc, err := pdf0.NewPDFADocumentWith(pdfa.SkeletonOptions{
		Level: level, Title: "RenderInto", Created: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC),
		FileID: []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// bytesOf is doc written, the whole of what a caller could observe of it.
func bytesOf(t *testing.T, doc *pdf0.Document) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := doc.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// restricted is Noto Sans with its OS/2 fsType set to Restricted License
// embedding (0x0002), a face whose licence forbids embedding it.
func restricted(t *testing.T) *shape.Face {
	t.Helper()
	data := append([]byte(nil), notoSansBytes(t)...)
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == "OS/2" {
			binary.BigEndian.PutUint16(data[binary.BigEndian.Uint32(rec[8:])+8:], 0x0002)
		}
	}
	f, err := shape.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestRenderIntoAddsAPageAfterTheOnesThere: a page per call, after the pages
// the document had, each carrying its own link, and the document itself as the
// result.
func TestRenderIntoAddsAPageAfterTheOnesThere(t *testing.T) {
	doc := pdf0.NewDocument()
	var b content.Builder
	b.Rect(10, 10, 20, 20).Fill()
	if _, err := doc.AddPage(pdf0.Page{Width: 100, Height: 100, Content: &b}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		html := `<p><a href="https://example.com/` + string(rune('a'+i)) + `">out</a></p>`
		out, err := RenderInto(doc, Input{HTML: html}, Options{Page: A5})
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if out.Document != doc {
			t.Fatalf("render %d: Result.Document is not the document rendered into", i)
		}
	}
	pages := doc.PageList()
	if len(pages) != 3 {
		t.Fatalf("the document has %d pages; want the one it had and two more", len(pages))
	}
	if annots := pages[0].Get("Annots"); annots != nil {
		t.Errorf("the page the document had gained annotations: %v", annots)
	}
	for i, page := range pages[1:] {
		annots, _ := doc.Resolve(page.Get("Annots")).(object.Array)
		if len(annots) != 1 {
			t.Fatalf("page %d has %d annotations; want its one link", i+1, len(annots))
		}
		action := doc.ResolveDict(doc.ResolveDict(annots[0]).Get("A"))
		uri, _ := doc.Resolve(action.Get("URI")).(object.String)
		if want := "https://example.com/" + string(rune('a'+i)); string(uri.Value) != want {
			t.Errorf("page %d links to %q; want %q", i+1, uri.Value, want)
		}
	}
}

// TestRenderIntoLeavesTheDocumentAloneWhenItFails: a refused page, a page that
// fails while it is written, and a level this package does not write all leave
// the document's bytes as they were.
func TestRenderIntoLeavesTheDocumentAloneWhenItFails(t *testing.T) {
	cases := []struct {
		name string
		doc  func() *pdf0.Document
		in   Input
		opts Options
		want func(error) bool
	}{
		{
			name: "refused: a family nothing supplies, every font embedded",
			doc:  func() *pdf0.Document { return pdfaDoc(t, pdfa.PDFA2b) },
			in:   Input{HTML: `<p style="font-family: monospace">x</p>`},
			opts: Options{Page: A5},
			want: func(err error) bool { var r *RefusedError; return errors.As(err, &r) },
		},
		{
			name: "failed while written: a face whose licence forbids embedding",
			doc:  func() *pdf0.Document { return pdfaDoc(t, pdfa.PDFA2b) },
			in: Input{HTML: `<p>x</p><img src="a.png">`, Fonts: oneFace{restricted(t)},
				Resources: bytesResolver{"a.png": pngBytes(t)}},
			opts: Options{Page: A5},
			want: func(err error) bool { return errors.Is(err, fonts.ErrRestrictedLicense) },
		},
		{
			name: "a level it does not write",
			doc:  func() *pdf0.Document { return pdfaDoc(t, pdfa.PDFA2a) },
			in:   Input{HTML: `<p>x</p>`, Fonts: notoSansSet(t)},
			opts: Options{Page: A5},
			want: func(err error) bool { return errors.Is(err, ErrUnsupportedLevel) },
		},
		{
			name: "an old version, refused",
			doc: func() *pdf0.Document {
				d := pdf0.NewDocument()
				d.Version = "1.3"
				return d
			},
			in:   Input{HTML: `<p style="opacity: .5">x</p>`},
			opts: Options{Page: A5, EmbedAllFonts: true},
			want: func(err error) bool { var r *RefusedError; return errors.As(err, &r) },
		},
	}
	for _, tc := range cases {
		doc := tc.doc()
		before := bytesOf(t, doc)
		version := doc.Version
		_, err := RenderInto(doc, tc.in, tc.opts)
		if !tc.want(err) {
			t.Errorf("%s: err %v", tc.name, err)
		}
		if doc.Version != version {
			t.Errorf("%s: the version went from %s to %s", tc.name, version, doc.Version)
		}
		if after := bytesOf(t, doc); !bytes.Equal(before, after) {
			t.Errorf("%s: the document changed (%d bytes, then %d)", tc.name, len(before), len(after))
		}
	}
	if _, err := RenderInto(nil, Input{HTML: "x"}, Options{}); err == nil {
		t.Error("a nil document was accepted")
	}
}

// TestRenderIntoKeepsTheDocumentsClaim: at every level it writes, a page laid
// out in the document's own @font-face face is one Save writes, which it does
// only for a document that passes the level it claims — and, when PDF0_VERAPDF
// names veraPDF, one veraPDF passes too. A generic family, which would be a
// standard font, is refused rather than written, whatever EmbedAllFonts says.
func TestRenderIntoKeepsTheDocumentsClaim(t *testing.T) {
	res := bytesResolver{"noto.ttf": notoSansBytes(t), "a.png": pngBytes(t)}
	dir := t.TempDir()
	html := notoFace + `<div style="font-family: Doc"><h1>Title</h1>
<p style="opacity: .6">Translucent <b>text</b> and <a href="https://example.com/">a link</a>.</p>
<div style="width: 120px; height: 30px; border-radius: 8px; background: linear-gradient(90deg, red, rgba(0,0,255,.4))"></div>
<img src="a.png" style="width: 40px"></div>`
	var written []string
	for _, l := range renderIntoLevels {
		doc := pdfaDoc(t, l.level)
		if _, err := RenderInto(doc, Input{HTML: html, Resources: res}, Options{Page: A5}); err != nil {
			t.Errorf("%s: %v", l.level, err)
			continue
		}
		var b bytes.Buffer
		if err := doc.Save(&b); err != nil {
			t.Errorf("%s: Save refused the document: %v", l.level, err)
			continue
		}
		path := filepath.Join(dir, l.flavour+".pdf")
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		written = append(written, path)

		generic := pdfaDoc(t, l.level)
		_, err := RenderInto(generic, Input{HTML: `<p style="font-family: sans-serif">x</p>`}, Options{Page: A5})
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Errorf("%s: a generic family rendered (%v); every font is to be embedded", l.level, err)
		}
	}
	veraPDFPasses(t, written)
}

// veraPDFPasses runs PDF0_VERAPDF over files named for their flavour
// ("2b.pdf") and fails for each veraPDF does not pass. Without it, it says so
// and checks nothing.
func veraPDFPasses(t *testing.T, files []string) {
	t.Helper()
	vera := os.Getenv("PDF0_VERAPDF")
	if vera == "" {
		// Logged, not skipped: what ran before this, Save's own check, is a
		// result, and a skip would report it as none.
		t.Log("PDF0_VERAPDF is not set: no veraPDF to validate with (testdata/verapdf/verapdf-docker.sh)")
		return
	}
	for _, f := range files {
		flavour := strings.TrimSuffix(filepath.Base(f), ".pdf")
		out, err := exec.Command(vera, "--flavour", flavour, "--format", "text", f).CombinedOutput()
		if !strings.Contains(string(out), "PASS ") {
			t.Errorf("veraPDF --flavour %s does not pass %s (%v):\n%s", flavour, f, err, out)
		}
	}
}

// TestRenderIntoRefusesTheLevelsItDoesNotWrite: the a levels, which need a
// tagged structure. (PDF/A-1b is written; see TestAPDFA1bPageIsOpaque.)
func TestRenderIntoRefusesTheLevelsItDoesNotWrite(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1a, pdfa.PDFA2a, pdfa.PDFA3a} {
		_, err := RenderInto(pdfaDoc(t, level), Input{HTML: "<p>x</p>", Fonts: notoSansSet(t)}, Options{})
		if !errors.Is(err, ErrUnsupportedLevel) {
			t.Errorf("%s: err %v; want ErrUnsupportedLevel", level, err)
		}
	}
}

// TestRenderIntoRaisesAVersionBelowPDF14: a page may use transparency, which
// is PDF 1.4, so a document claiming nothing and older than that is raised to
// it, and a newer one is left as it is.
func TestRenderIntoRaisesAVersionBelowPDF14(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"1.2", "1.4"}, {"1.3", "1.4"}, {"1.4", "1.4"}, {"1.5", "1.5"}, {"1.7", "1.7"}, {"2.0", "2.0"},
	} {
		doc := pdf0.NewDocument()
		doc.Version = tc.from
		if _, err := RenderInto(doc, Input{HTML: "<p>x</p>"}, Options{}); err != nil {
			t.Fatal(err)
		}
		if doc.Version != tc.to {
			t.Errorf("a %s document is %s after a page was added; want %s", tc.from, doc.Version, tc.to)
		}
	}
	// A document claiming a level keeps the version it has, which its level
	// fixes: Save, not this, says whether the two agree.
	doc := pdfaDoc(t, pdfa.PDFA2b)
	doc.Version = "1.6"
	if _, err := RenderInto(doc, Input{HTML: "<p>x</p>", Fonts: notoSansSet(t)}, Options{}); err != nil {
		t.Fatal(err)
	}
	if doc.Version != "1.6" {
		t.Errorf("a PDF/A-2b document at 1.6 is %s after a page was added; a claim's version is not raised", doc.Version)
	}
}

// TestRenderIntoCarriesThePageWhole: what AppendPages leaves out of the
// scratch document is its catalog, which describes the scratch document and
// not the page, and nothing else — no link, destination or page key.
func TestRenderIntoCarriesThePageWhole(t *testing.T) {
	expected := map[string]bool{"Metadata": true, "OutputIntents": true, "Names": true}
	res := bytesResolver{"noto.ttf": notoSansBytes(t), "a.png": pngBytes(t)}
	html := notoFace + `<div style="font-family: Doc"><p><a href="https://example.com/">out</a></p>
<div style="background: url(a.png); background-size: 10px 10px; width: 50px; height: 20px"></div>
<p style="transform: rotate(10deg); text-shadow: 1px 1px 0 red">b</p></div>`
	for _, doc := range []*pdf0.Document{pdf0.NewDocument(), pdfaDoc(t, pdfa.PDFA2b), pdfaDoc(t, pdfa.PDFA4F)} {
		out, err := RenderInto(doc, Input{HTML: html, Resources: res}, Options{Page: A5})
		if err != nil {
			t.Fatal(err)
		}
		r := out.imported
		if r.Pages != 1 || r.DestinationsDropped != 0 || len(r.FieldsRenamed) != 0 {
			t.Errorf("import: %d pages, %d destinations dropped, %d fields renamed; want 1, 0, 0",
				r.Pages, r.DestinationsDropped, len(r.FieldsRenamed))
		}
		for _, o := range r.Omitted {
			if !expected[o.Entry] {
				t.Errorf("the import left out %s (%s), which is part of the page", o.Entry, o.Reason)
			}
		}
	}
}

// pngBytes is a small RGBA PNG with transparency, for a page that draws a
// soft mask.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 30, B: 30, A: uint8(60 + 24*x)})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
