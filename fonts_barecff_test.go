package pdf0

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// TestADocumentBeforePDF16CarriesTheCFFBare adds a page in a CFF face to a
// document declaring each version, and holds the font file to it: a version
// before 1.6 has no OpenType font file, so the program is the bare
// /CIDFontType0C; 1.6 and later keep the wrapper. A document before 1.4 has
// no transparency, so a greyscale glyph in a colour is not a soft mask.
func TestADocumentBeforePDF16CarriesTheCFFBare(t *testing.T) {
	for version, want := range map[string]object.Name{
		"1.3": "CIDFontType0C", "1.5": "CIDFontType0C", "1.6": "OpenType", "1.7": "OpenType", "2.0": "OpenType",
	} {
		t.Run(version, func(t *testing.T) {
			face := textFontKinds[2].face(t)
			doc := NewDocument()
			doc.Version = version
			var b content.Builder
			b.BeginText().SetFont("F1", 12)
			face.DrawShaped(&b, "Hello", 12)
			b.EndText()
			if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
				t.Fatal(err)
			}
			back, _ := writeAndRead(t, doc)
			fd := cidFontDescriptor(t, back)
			file := back.Resolve(fd.Get("FontFile3")).(*object.Stream)
			if got := file.Dict.Get("Subtype"); got != want {
				t.Errorf("PDF %s: the font file's /Subtype is %v; want %v", version, got, want)
			}
			if fd.Get("CIDSet") != nil {
				t.Errorf("PDF %s: a name-keyed CFF outside PDF/A-1 carries a /CIDSet", version)
			}
		})
	}
	for version, soft := range map[string]bool{"1.3": false, "1.4": true} {
		t.Run("strikes-"+version, func(t *testing.T) {
			face := bitmapFace(t, "Strikes.ttf")
			doc := NewDocument()
			doc.Version = version
			var b content.Builder
			b.SetRGB(0.2, 0.3, 0.4)
			b.BeginText().SetFont("F1", 12)
			face.DrawShaped(&b, "AB", 12)
			b.EndText()
			if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b,
				Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
				t.Fatal(err)
			}
			got := false
			for _, o := range doc.Objects {
				if s, ok := o.Value.(*object.Stream); ok && s.Dict.Get("SMask") != nil {
					got = true
				}
			}
			if got != soft {
				t.Errorf("PDF %s: a soft mask written: %v; want %v", version, got, soft)
			}
		})
	}
}

// TestABareCFFIsPDFA2 writes each kind of CFF face bare (fonts.Forms.BareCFF)
// into a PDF/A-2b document, which would otherwise carry it in the OpenType
// wrapper, and holds it to pdf0's validator and, with PDF0_VERAPDF set,
// veraPDF: the bare program, without PDF/A-1's /CIDSet for a name-keyed CFF,
// is a conforming font at PDF/A-2.
func TestABareCFFIsPDFA2(t *testing.T) {
	out := t.TempDir()
	var written []string
	for _, k := range []int{1, 2} { // cidcff, namecff
		kind := textFontKinds[k]
		t.Run(kind.name, func(t *testing.T) {
			face := kind.face(t)
			doc := mustPDFADoc(t, pdfa.PDFA2b)
			var b content.Builder
			b.SetRGB(0, 0, 0)
			b.BeginText().SetFont("F1", 20).SetTextMatrix(1, 0, 0, 1, 10, 40)
			face.DrawShaped(&b, kind.text(face), 20)
			b.EndText()
			e, err := face.EmbedForms(doc, fonts.Forms{Horizontal: true, BareCFF: true})
			if err != nil {
				t.Fatal(err)
			}
			if e.Version != "1.3" {
				t.Errorf("Version %s; a bare CFF is PDF 1.3", e.Version)
			}
			if _, err := doc.AddPage(Page{Width: 400, Height: 100, Content: &b,
				Fonts: map[object.Name]object.Object{"F1": e.Horizontal}}); err != nil {
				t.Fatal(err)
			}
			back, data := writeAndRead(t, doc)
			fd := cidFontDescriptor(t, back)
			if st := back.Resolve(fd.Get("FontFile3")).(*object.Stream).Dict.Get("Subtype"); st != object.Name("CIDFontType0C") {
				t.Fatalf("the font file's /Subtype is %v; want /CIDFontType0C", st)
			}
			for _, v := range ValidatePDFA(back, pdfa.PDFA2b) {
				t.Errorf("pdf0: %s", v.Error())
			}
			path := filepath.Join(out, kind.name+".pdf")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			written = append(written, path)
		})
	}
	if vera := os.Getenv("PDF0_VERAPDF"); vera != "" && len(written) > 0 {
		veraPDFPasses(t, vera, written, "--flavour", "2b")
	}
}
