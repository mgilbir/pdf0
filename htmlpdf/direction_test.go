package htmlpdf

import (
	"bytes"
	"context"
	"testing"

	pdf0 "github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// directionOf is the /ViewerPreferences /Direction of a written document, or
// "" when it has none.
func directionOf(t *testing.T, doc *pdf0.Document) string {
	t.Helper()
	back, _ := writtenBack(t, doc)
	cat := back.ResolveDict(back.Trailer.Get("Root"))
	prefs := back.ResolveDict(cat.Get("ViewerPreferences"))
	if prefs == nil {
		return ""
	}
	d, _ := back.Resolve(prefs.Get("Direction")).(object.Name)
	return string(d)
}

func directionPage(dir string) Input {
	return Input{HTML: `<html` + dir + `><body><p>abc</p><p>x</p></body></html>`}
}

// TestARightToLeftDocumentIsReadRightToLeft: a root element with dir="rtl"
// makes the document Render made, plain or PDF/A, and under a context too,
// say /Direction /R2L. Any other direction writes nothing, as left to right is
// PDF's default.
func TestARightToLeftDocumentIsReadRightToLeft(t *testing.T) {
	renders := map[string]func(Input) (Result, error){
		"Render": func(in Input) (Result, error) { return Render(in, Options{}) },
		"RenderContext": func(in Input) (Result, error) {
			return RenderContext(context.Background(), in, Options{}, RunLimits{})
		},
		"Render with PDFA": func(in Input) (Result, error) {
			return Render(in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}})
		},
		"RenderContext with PDFA": func(in Input) (Result, error) {
			return RenderContext(context.Background(), in, Options{PDFA: pdfa.SkeletonOptions{Level: pdfa.PDFA2b}}, RunLimits{})
		},
	}
	for name, render := range renders {
		for dir, want := range map[string]string{` dir="rtl"`: "R2L", ` dir="ltr"`: "", ` dir="auto"`: "", ``: ""} {
			in := directionPage(dir)
			in.Fonts = notoSansSet(t)
			out, err := render(in)
			if err != nil {
				t.Fatalf("%s, %q: %v", name, dir, err)
			}
			if got := directionOf(t, out.Document); got != want {
				t.Errorf("%s, root%s: /Direction %q, want %q", name, dir, got, want)
			}
		}
	}
}

// TestRenderIntoLeavesTheDirectionAlone: one page's direction is not the
// document's, so a right-to-left page added to a document does not make the
// whole document read right to left.
func TestRenderIntoLeavesTheDirectionAlone(t *testing.T) {
	doc := pdfaDoc(t, pdfa.PDFA2b)
	in := directionPage(` dir="rtl"`)
	in.Fonts = notoSansSet(t)
	if _, err := RenderInto(doc, in, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := directionOf(t, doc); got != "" {
		t.Errorf("RenderInto gave the document /Direction %q", got)
	}
}

// TestARightToLeftPDFADocumentPassesVeraPDF: the preference is one every
// PDF/A level a render writes accepts.
func TestARightToLeftPDFADocumentPassesVeraPDF(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for _, l := range renderIntoLevels {
		in := directionPage(` dir="rtl"`)
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
