package pdf0

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Every way of putting text on a page, in every kind of face, judged by what
// comes back out of the file.
//
// The fonts package has five public ways to turn a string into content-stream
// bytes — Encode, Shape, ShapeWith, Draw and DrawShaped — and htmlpdf is a
// sixth (it has its own test, over the same faces, in htmlpdf). They grew
// apart: two of them wrote the glyph index where a CID-keyed CFF font is
// addressed by CID, one wrote two-byte codes into a one-byte font, and none of
// them told the ToUnicode CMap which text a shaped glyph was drawn for. Each
// fault was invisible to a test that exercised one path, because the path that
// was tested was the one that was right.
//
// So this is a matrix, and every cell asks the two questions a reader asks of
// a page: does the text come back out, and does the font dictionary agree with
// the codes the page shows? The second is this module's own PDF/A validator,
// which checks every shown code against /CIDSet, the program's glyphs and /W.

// drawPath is one public way of drawing a string with a face.
type drawPath struct {
	name string
	draw func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64)
}

var drawPaths = []drawPath{
	{"Encode", func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64) {
		codes, _ := face.Encode(s)
		b.ShowText(codes)
	}},
	{"Shape", func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64) {
		spans, _ := face.Shape(s)
		if len(spans) > 0 {
			b.ShowTextAdjusted(spans...)
		}
	}},
	{"ShapeWith", func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64) {
		// Small capitals: a substitution whose glyphs no cmap entry reaches,
		// so the only way their text survives is through what was drawn.
		spans, _ := face.ShapeWith(s, "smcp")
		if len(spans) > 0 {
			b.ShowTextAdjusted(spans...)
		}
	}},
	{"Draw", func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64) {
		glyphs, _ := face.ShapeGlyphs(s)
		face.Draw(b, s, glyphs, size)
	}},
	{"DrawShaped", func(t *testing.T, face *fonts.Face, b *content.Builder, s string, size float64) {
		face.DrawShaped(b, s, size)
	}},
}

// faceCase is one kind of face, with the text that exercises it.
type faceCase struct {
	name  string
	load  func(t *testing.T) *fonts.Face
	texts []string
	// embedded is whether the face carries a program, so that the document
	// can be a PDF/A one. A standard face cannot: PDF/A requires every font
	// embedded.
	embedded bool
}

func drawPathFaces() []faceCase {
	return []faceCase{
		{
			// A CID-keyed CFF, where the code is the CID and not the glyph
			// index. ｱ is the character whose CID (59158) and glyph index
			// (15435) differ, so a path writing the wrong one is caught.
			name: "cid-keyed-cff",
			load: func(t *testing.T) *fonts.Face {
				f, err := fonts.Load(cidKeyedFace(t))
				if err != nil {
					t.Fatalf("loading: %v", err)
				}
				return f
			},
			texts:    []string{"ｱ日本", "日本語のテキスト"},
			embedded: true,
		},
		{
			// TrueType outlines, composite: ligatures, conjuncts, reordering.
			name: "truetype",
			load: func(t *testing.T) *fonts.Face {
				f, err := fonts.NotoSans()
				if err != nil {
					t.Fatalf("loading: %v", err)
				}
				return f
			},
			texts:    []string{"office", "affluent", "क्षत्रिय", "नमस्ते"},
			embedded: true,
		},
		{
			// Arabic: contextual forms, lam-alef, marks, right to left.
			name:     "arabic",
			load:     func(t *testing.T) *fonts.Face { return arabicFace(t) },
			texts:    []string{"سلام", "السلام عليكم", "بِسْمِ"},
			embedded: true,
		},
		{
			// A simple face: one byte per character, WinAnsi.
			name: "simple",
			load: func(t *testing.T) *fonts.Face {
				f, err := fonts.NotoSansSimple()
				if err != nil {
					t.Fatalf("loading: %v", err)
				}
				return f
			},
			texts:    []string{"Hello office", "Café"},
			embedded: true,
		},
		{
			// A standard face: nothing embedded, one byte per character.
			name: "standard",
			load: func(t *testing.T) *fonts.Face {
				f, err := fonts.Standard("Helvetica")
				if err != nil {
					t.Fatalf("loading: %v", err)
				}
				return f
			},
			texts: []string{"Hello office", "Café"},
		},
	}
}

// formeDir is the directory of the forme module this build uses, which is
// where its test fonts are. It is asked of the go command, which is running
// this test and so is always there.
var formeDir = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/mgilbir/forme").Output()
	return strings.TrimSpace(string(out)), err
})

// arabicFace is Noto Sans Arabic from forme's shaping test data: the bundled
// face has no Arabic in it, and joining is the case that has to be tested.
func arabicFace(t *testing.T) *fonts.Face {
	t.Helper()
	dir, err := formeDir()
	if err != nil || dir == "" {
		t.Fatalf("locating the forme module: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "testdata", "harfbuzz", "fonts", "NotoSansArabic.ttf"))
	if err != nil {
		t.Fatalf("reading forme's Arabic test face: %v", err)
	}
	f, err := fonts.Load(data)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	return f
}

// drawnDocument draws one string through one path and returns the written
// file, read back.
func drawnDocument(t *testing.T, face *fonts.Face, path drawPath, text string, level pdfa.Level, embedded bool) (*Document, []byte) {
	t.Helper()
	var doc *Document
	if embedded {
		doc = mustPDFADoc(t, level)
	} else {
		doc = NewDocument()
	}
	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(40, 100)
	path.draw(t, face, &b, text, 12)
	b.EndText()
	if _, err := doc.AddPage(Page{
		Width: 400, Height: 200, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face},
	}); err != nil {
		t.Fatalf("adding the page: %v", err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("the document pdf0 wrote, pdf0 cannot read: %v", err)
	}
	return back, buf.Bytes()
}

// TestEveryDrawingPathRoundTripsInEveryFaceKind is the matrix.
func TestEveryDrawingPathRoundTripsInEveryFaceKind(t *testing.T) {
	for _, fc := range drawPathFaces() {
		t.Run(fc.name, func(t *testing.T) {
			base := fc.load(t)
			for _, path := range drawPaths {
				t.Run(path.name, func(t *testing.T) {
					for _, text := range fc.texts {
						// A clone per document: a face records what it was
						// asked to draw, and that decides the subset and the
						// ToUnicode CMap. The clone shares the parse, which for
						// the CJK face is most of the cost.
						back, _ := drawnDocument(t, base.Clone(), path, text, pdfa.PDFA4, fc.embedded)
						if got := strings.TrimSpace(mustExtractText(t, back)); got != text {
							t.Errorf("%q extracted as %q", text, got)
						}
					}
				})
			}
		})
	}
}

// TestEveryDrawingPathValidatesAtEveryLevel asks this module's PDF/A validator
// about every cell: a code the program does not define, a /CIDSet that misses
// a CID the page shows, or a /W that disagrees with the program is a finding.
func TestEveryDrawingPathValidatesAtEveryLevel(t *testing.T) {
	levels := []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4}
	for _, fc := range drawPathFaces() {
		if !fc.embedded {
			continue
		}
		t.Run(fc.name, func(t *testing.T) {
			base := fc.load(t)
			for _, path := range drawPaths {
				t.Run(path.name, func(t *testing.T) {
					for _, level := range levels {
						text := strings.Join(fc.texts, " ")
						back, _ := drawnDocument(t, base.Clone(), path, text, level, true)
						for _, v := range ValidatePDFA(back, level) {
							t.Errorf("%s, %q: %s", level, text, v.Error())
						}
					}
				})
			}
		})
	}
}
