package pdf0

import (
	"bytes"
	"compress/zlib"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// namedKinds are composite faces of each program kind with text in them.
var namedKinds = []int{0, 1, 2} // truetype, cidcff, namecff in textFontKinds

// namedDocument is a PDF/A-2b document with one line of text in face, its
// font written as forms says through EmbedForms.
func namedDocument(t *testing.T, face *fonts.Face, text string, forms fonts.Forms) (*Document, fonts.Embedded) {
	t.Helper()
	doc := mustPDFADoc(t, pdfa.PDFA2b)
	var b content.Builder
	b.SetRGB(0, 0, 0)
	b.BeginText().SetFont("F1", 20).SetTextMatrix(1, 0, 0, 1, 10, 40)
	face.DrawShaped(&b, text, 20)
	b.EndText()
	e, err := face.EmbedForms(doc, forms)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.AddPage(Page{Width: 400, Height: 100, Content: &b,
		Fonts: map[object.Name]object.Object{"F1": e.Horizontal}}); err != nil {
		t.Fatal(err)
	}
	return doc, e
}

// descriptorOf is the FontDescriptor of a document's one CIDFont.
func descriptorOf(t *testing.T, doc *Document) (cidFont, fd *object.Dictionary) {
	t.Helper()
	for _, o := range doc.Objects {
		if d, ok := o.Value.(*object.Dictionary); ok {
			if st, _ := doc.Resolve(d.Get("Subtype")).(object.Name); st == "CIDFontType2" || st == "CIDFontType0" {
				if cidFont != nil {
					t.Fatal("more than one CIDFont")
				}
				cidFont = d
			}
		}
	}
	if cidFont == nil {
		t.Fatal("no CIDFont")
	}
	return cidFont, doc.ResolveDict(cidFont.Get("FontDescriptor"))
}

// TestANamedFontCarriesNoProgram writes each composite kind by name and holds
// the font to what Forms.Named says: the face's PostScript name with no tag,
// a descriptor without a font file or /CIDSet, and the /W, ToUnicode and
// CIDToGIDMap a reader needs once the program is there.
func TestANamedFontCarriesNoProgram(t *testing.T) {
	for _, k := range namedKinds {
		kind := textFontKinds[k]
		t.Run(kind.name, func(t *testing.T) {
			face := kind.face(t)
			doc, _ := namedDocument(t, face, kind.text(face), fonts.Forms{Horizontal: true, Named: true})
			back, _ := writeAndRead(t, doc)
			cidFont, fd := descriptorOf(t, back)
			for _, key := range []object.Name{"FontFile", "FontFile2", "FontFile3", "CIDSet"} {
				if fd.Get(key) != nil {
					t.Errorf("the descriptor has %s", key)
				}
			}
			want := object.Name(face.Name())
			for what, d := range map[string]*object.Dictionary{"CIDFont": cidFont, "descriptor": fd} {
				key := object.Name("BaseFont")
				if what == "descriptor" {
					key = "FontName"
				}
				if got, _ := back.Resolve(d.Get(key)).(object.Name); got != want {
					t.Errorf("the %s's %s is %v; want %v, untagged", what, key, got, want)
				}
			}
			// /W and /DW are the ones the embedded font has: the named font
			// describes the glyphs drawn as the subset does.
			embedded, _ := namedDocument(t, kind.face(t), kind.text(face), fonts.Forms{Horizontal: true})
			eback, _ := writeAndRead(t, embedded)
			ecid, _ := descriptorOf(t, eback)
			for _, key := range []object.Name{"W", "DW"} {
				if !object.Equal(back.Resolve(cidFont.Get(key)), eback.Resolve(ecid.Get(key))) {
					t.Errorf("%s is %v; the embedded font's is %v", key, cidFont.Get(key), ecid.Get(key))
				}
			}
			if st, _ := back.Resolve(cidFont.Get("Subtype")).(object.Name); st == "CIDFontType2" {
				if m, _ := back.Resolve(cidFont.Get("CIDToGIDMap")).(object.Name); m != "Identity" {
					t.Errorf("/CIDToGIDMap is %v; want /Identity", m)
				}
			}
			// pdf0's validator reports the font, and only as not embedded.
			for _, v := range ValidatePDFA(back, pdfa.PDFA2b) {
				if v.Rule != "6.2.11.4.1" {
					t.Errorf("a named font is reported other than as not embedded: %s", v.Error())
				}
			}
		})
	}
}

// TestANamedFontIsTheFontOnceTheProgramIsAdded is the assembler's half: a
// named font, once the face's program is added to its descriptor under the
// same name, is a complete font. It passes PDF/A-2b in pdf0's validator and,
// with PDF0_VERAPDF set, in veraPDF; it extracts as the text; and it renders
// pixel for pixel as the same text with the font embedded the usual way,
// which is what shows the codes bind to the program's own numbering.
func TestANamedFontIsTheFontOnceTheProgramIsAdded(t *testing.T) {
	out := t.TempDir()
	var written []string
	for _, k := range namedKinds {
		kind := textFontKinds[k]
		t.Run(kind.name, func(t *testing.T) {
			face := kind.face(t)
			text := kind.text(face)
			doc, _ := namedDocument(t, face, text, fonts.Forms{Horizontal: true, Named: true})
			_, fd := descriptorOf(t, doc)
			// The assembler: the whole program the face draws. A CFF program
			// is added bare, its CFF table as /CIDFontType0C, and the font it
			// is compared with is embedded bare too: Poppler, the renderer
			// here, mis-draws an OpenType-wrapped CID-keyed CFF whose CIDs
			// are not its glyph indices (docs/font-embedding.md), which would
			// make the comparison about Poppler.
			program := face.Program()
			if face.IsCFF() {
				program = font.SFNTTables(program)["CFF "]
			}
			file := object.NewStream(nil, deflate(t, program))
			file.Dict.Set("Filter", object.Name("FlateDecode"))
			if face.IsCFF() {
				file.Dict.Set("Subtype", object.Name("CIDFontType0C"))
				fd.Set("FontFile3", doc.Add(file))
			} else {
				file.Dict.Set("Length1", object.Integer(len(program)))
				fd.Set("FontFile2", doc.Add(file))
			}
			back, assembled := writeAndRead(t, doc)
			for _, v := range ValidatePDFA(back, pdfa.PDFA2b) {
				t.Errorf("pdf0, assembled: %s", v.Error())
			}
			path := filepath.Join(out, kind.name+"-assembled.pdf")
			if err := os.WriteFile(path, assembled, 0o644); err != nil {
				t.Fatal(err)
			}
			written = append(written, path)

			embeddedDoc, _ := namedDocument(t, kind.face(t), text, fonts.Forms{Horizontal: true, BareCFF: true})
			_, embedded := writeAndRead(t, embeddedDoc)

			if got := extractText(t, assembled); got != extractText(t, embedded) {
				t.Errorf("assembled, the page extracts as %q; embedded, as %q", got, extractText(t, embedded))
			}
			a, e := renderPNG(t, assembled), renderPNG(t, embedded)
			if a != nil && !bytes.Equal(a, e) {
				t.Error("assembled and embedded, the page renders differently")
			}
		})
	}
	if vera := os.Getenv("PDF0_VERAPDF"); vera != "" && len(written) > 0 {
		veraPDFPasses(t, vera, written, "--flavour", "2b")
	}
}

// TestANamedFontExtractsWithoutItsProgram: the ToUnicode CMap is in the named
// font, so a reader extracts the text before any program is added.
func TestANamedFontExtractsWithoutItsProgram(t *testing.T) {
	kind := textFontKinds[0]
	face := kind.face(t)
	doc, _ := namedDocument(t, face, "plain text", fonts.Forms{Horizontal: true, Named: true})
	_, data := writeAndRead(t, doc)
	if got := extractText(t, data); got != "plain text" {
		t.Errorf("the named font's page extracts as %q", got)
	}
}

// TestNamedIsRefusedWhereItCannotBe: a simple, standard or Type 3 face, a
// PDF/A-1 font, and a face with no PostScript name.
func TestNamedIsRefusedWhereItCannotBe(t *testing.T) {
	std, err := fonts.Standard("Helvetica")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		face  *fonts.Face
		forms fonts.Forms
	}{
		"simple":   {simpleFace(t), fonts.Forms{Horizontal: true, Named: true}},
		"standard": {std, fonts.Forms{Horizontal: true, Named: true}},
		"type3":    {bitmapFace(t, "Strikes.ttf"), fonts.Forms{Horizontal: true, Named: true}},
		"pdfa1":    {textFontKinds[0].face(t), fonts.Forms{Horizontal: true, Named: true, PDFA1: true}},
	} {
		t.Run(name, func(t *testing.T) {
			var b content.Builder
			b.BeginText().SetFont("F1", 12)
			c.face.DrawShaped(&b, "AB", 12)
			b.EndText()
			before := NewDocument()
			n := len(before.Objects)
			if _, err := c.face.EmbedForms(before, c.forms); err == nil {
				t.Error("written by name")
			}
			if len(before.Objects) != n {
				t.Errorf("a refused embedding wrote %d objects", len(before.Objects)-n)
			}
		})
	}
}

func deflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// extractText is poppler's text of a one-page PDF, trimmed.
func extractText(t *testing.T, pdf []byte) string {
	t.Helper()
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		t.Skip("pdftotext is not installed")
	}
	path := filepath.Join(t.TempDir(), "in.pdf")
	if err := os.WriteFile(path, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-q", path, "-").Output()
	if err != nil {
		t.Fatalf("pdftotext: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// renderPNG is poppler's rendering of a one-page PDF at 72 dpi, or nil when
// pdftoppm is not installed.
func renderPNG(t *testing.T, pdf []byte) []byte {
	t.Helper()
	bin, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(in, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "-png", "-r", "72", "-singlefile", in, filepath.Join(dir, "out")).CombinedOutput(); err != nil {
		t.Fatalf("pdftoppm: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.png"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
