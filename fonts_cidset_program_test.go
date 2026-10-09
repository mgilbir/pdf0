package pdf0

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/forme/font"
	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// /CIDSet as veraPDF reads a program to have CIDs, for each way a program can
// be keyed, and the PDF/A-1 font file a CFF is written as. See fonts.cidSet
// and pdfa's checkCIDSetMatchesProgram for the reading.

// textFontKinds are a face of each keying: TrueType (Noto Sans), a
// CID-keyed CFF (Noto Sans JP) and a CFF that is not CID-keyed (forme's
// CFFInk), each with text it maps.
var textFontKinds = []struct {
	name string
	face func(t *testing.T) *fonts.Face
	text func(f *fonts.Face) string
}{
	{"truetype", func(t *testing.T) *fonts.Face {
		s, err := notosans.Face()
		if err != nil {
			t.Fatal(err)
		}
		return fonts.Adopt(s)
	}, func(*fonts.Face) string { return "plain text" }},
	{"cidcff", func(t *testing.T) *fonts.Face { return mustLoadFace(t, cidKeyedFace(t)) },
		func(*fonts.Face) string { return "日本語テキスト" }},
	{"namecff", func(t *testing.T) *fonts.Face {
		return mustLoadFace(t, formeFile(t, "testdata/harfbuzz/fonts/CFFInk.otf"))
	}, func(f *fonts.Face) string {
		var b strings.Builder
		for r := rune(' '); r < 0x80; r++ {
			if _, ok := f.GlyphID(r); ok {
				b.WriteRune(r)
			}
		}
		return b.String()
	}},
}

var textFontLevels = map[string]pdfa.Level{"1b": pdfa.PDFA1b, "2b": pdfa.PDFA2b, "3b": pdfa.PDFA3b, "4": pdfa.PDFA4}

func mustLoadFace(t *testing.T, data []byte) *fonts.Face {
	t.Helper()
	f, err := fonts.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// textDocument is a PDF/A document at level with one line of text in face.
func textDocument(t *testing.T, face *fonts.Face, text string, level pdfa.Level) *Document {
	t.Helper()
	doc := mustPDFADoc(t, level)
	var b content.Builder
	b.SetRGB(0, 0, 0)
	b.BeginText().SetFont("F1", 20).SetTextMatrix(1, 0, 0, 1, 10, 40)
	face.DrawShaped(&b, text, 20)
	b.EndText()
	if _, err := doc.AddPage(Page{Width: 400, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		t.Fatal(err)
	}
	return doc
}

// cidFontDescriptor is the descriptor of a document's one CIDFont.
func cidFontDescriptor(t *testing.T, doc *Document) *object.Dictionary {
	t.Helper()
	var fd *object.Dictionary
	for _, o := range doc.Objects {
		if d, ok := o.Value.(*object.Dictionary); ok && (d.Get("FontFile2") != nil || d.Get("FontFile3") != nil) {
			if fd != nil {
				t.Fatal("the document has more than one embedded font")
			}
			fd = d
		}
	}
	if fd == nil {
		t.Fatal("the document has no embedded font")
	}
	return fd
}

// TestTheCIDSetIsWhatTheProgramHas writes text in each kind of face at every
// PDF/A level and holds the font to what the levels ask:
//
//   - a TrueType program's /CIDSet is every glyph slot it declares, the empty
//     ones its subset dropped included;
//   - a CID-keyed CFF carries a /CIDSet (TestTheCIDSetIsTheEmbeddedProgramsCharset
//     holds it to the charset);
//   - a CFF that is not CID-keyed carries none, except under PDF/A-1, where a
//     subset must, and it is every glyph slot;
//   - a CFF is /OpenType, except under PDF/A-1, whose PDF 1.4 has only the
//     bare /CIDFontType0C.
//
// Each document passes pdf0's validator, and veraPDF when PDF0_VERAPDF names
// it. veraPDF is what found the TrueType and CFF sets wrong, and PDF/A-1's
// /OpenType: pdf0's validator, sharing the writer's reading, passed them.
func TestTheCIDSetIsWhatTheProgramHas(t *testing.T) {
	out := t.TempDir()
	var written []string
	for _, kind := range textFontKinds {
		for name, level := range textFontLevels {
			t.Run(kind.name+"-"+name, func(t *testing.T) {
				face := kind.face(t)
				doc := textDocument(t, face, kind.text(face), level)
				back, data := writeAndRead(t, doc)
				for _, v := range ValidatePDFA(back, level) {
					t.Errorf("pdf0: %s", v.Error())
				}
				path := filepath.Join(out, kind.name+"-"+name+".pdf")
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
				written = append(written, path)

				fd := cidFontDescriptor(t, back)
				cidSet, hasSet := back.Resolve(fd.Get("CIDSet")).(*object.Stream)
				pdfa1 := level.Part() == 1
				if file, ok := back.Resolve(fd.Get("FontFile3")).(*object.Stream); ok {
					want := object.Name("OpenType")
					if pdfa1 {
						want = "CIDFontType0C"
					}
					if got := file.Dict.Get("Subtype"); got != want {
						t.Errorf("the CFF font file's /Subtype is %v; want %v", got, want)
					}
				}
				var slots int
				switch kind.name {
				case "truetype":
					file := back.Resolve(fd.Get("FontFile2")).(*object.Stream)
					program, err := back.StreamData(file)
					if err != nil {
						t.Fatal(err)
					}
					slots = int(binary.BigEndian.Uint16(font.SFNTTables(program)["maxp"][4:]))
				case "namecff":
					if hasSet != pdfa1 {
						t.Fatalf("a CFF that is not CID-keyed carries a /CIDSet: %v; want %v", hasSet, pdfa1)
					}
					slots = face.NumGlyphs()
				case "cidcff":
					if !hasSet {
						t.Fatal("a CID-keyed CFF carries no /CIDSet")
					}
				}
				if slots == 0 || !hasSet {
					return
				}
				bits, err := back.StreamData(cidSet)
				if err != nil {
					t.Fatal(err)
				}
				for cid := 0; cid < 8*len(bits); cid++ {
					if got, want := bitSet(bits, cid), cid < slots; got != want {
						t.Fatalf("/CIDSet has CID %d: %v; the program declares %d glyphs", cid, got, slots)
					}
				}
			})
		}
	}
	if vera := os.Getenv("PDF0_VERAPDF"); vera != "" && len(written) > 0 {
		veraPDFPasses(t, vera, written)
	}
}

// TestTheValidatorHoldsTheCIDSetToTheProgram breaks a written font each way
// veraPDF reported pdf0's own fonts broken, and asks pdf0's validator for the
// clause veraPDF gives: at PDF/A-2 and -3 a TrueType /CIDSet missing an empty
// glyph slot, or naming a CID past the program's glyphs, and a CFF that is not
// CID-keyed with any CID in its set (6.2.11.4.2); at PDF/A-1 a CFF font file
// with /Subtype /OpenType (6.3.2). The set missing an empty slot passes at
// PDF/A-1 and -4, as it does in veraPDF: PDF/A-1 holds the set to the CIDs
// shown, and PDF/A-4 does not have the rule.
func TestTheValidatorHoldsTheCIDSetToTheProgram(t *testing.T) {
	setCIDSet := func(t *testing.T, doc *Document, cids ...int) {
		t.Helper()
		fd := cidFontDescriptor(t, doc)
		highest := 0
		for _, c := range cids {
			highest = max(highest, c)
		}
		bits := make([]byte, highest/8+1)
		for _, c := range cids {
			bits[c/8] |= 0x80 >> (c % 8)
		}
		fd.Set("CIDSet", doc.Add(object.NewStream(nil, bits)))
	}
	for _, tc := range []struct {
		name   string
		kind   int // into textFontKinds
		breaks func(t *testing.T, doc *Document)
		rule   map[pdfa.Level]string
		says   string
	}{
		{
			name: "a TrueType set missing an empty slot", kind: 0,
			breaks: func(t *testing.T, doc *Document) {
				// Every slot but 1, which the subset emptied: "plain text"
				// does not draw it.
				fd := cidFontDescriptor(t, doc)
				program, _ := doc.StreamData(doc.Resolve(fd.Get("FontFile2")).(*object.Stream))
				n := int(binary.BigEndian.Uint16(font.SFNTTables(program)["maxp"][4:]))
				var cids []int
				for c := 0; c < n; c++ {
					if c != 1 {
						cids = append(cids, c)
					}
				}
				setCIDSet(t, doc, cids...)
			},
			rule: map[pdfa.Level]string{pdfa.PDFA1b: "", pdfa.PDFA2b: "6.2.11.4.2", pdfa.PDFA3b: "6.2.11.4.2", pdfa.PDFA4: ""},
			says: "does not list CID 1",
		},
		{
			name: "a TrueType set naming a CID past the glyphs", kind: 0,
			breaks: func(t *testing.T, doc *Document) {
				fd := cidFontDescriptor(t, doc)
				program, _ := doc.StreamData(doc.Resolve(fd.Get("FontFile2")).(*object.Stream))
				n := int(binary.BigEndian.Uint16(font.SFNTTables(program)["maxp"][4:]))
				cids := make([]int, n+1)
				for c := range cids {
					cids[c] = c
				}
				setCIDSet(t, doc, cids...)
			},
			rule: map[pdfa.Level]string{pdfa.PDFA2b: "6.2.11.4.2", pdfa.PDFA3b: "6.2.11.4.2", pdfa.PDFA4: ""},
			says: "which the embedded font program does not have",
		},
		{
			name: "a CFF that is not CID-keyed with a CID in its set", kind: 2,
			breaks: func(t *testing.T, doc *Document) {
				setCIDSet(t, doc, 0, 1, 2)
			},
			rule: map[pdfa.Level]string{pdfa.PDFA2b: "6.2.11.4.2", pdfa.PDFA3b: "6.2.11.4.2", pdfa.PDFA4: ""},
			says: "lists CID 1",
		},
		{
			name: "an OpenType font file at PDF/A-1", kind: 2,
			breaks: func(t *testing.T, doc *Document) {
				fd := cidFontDescriptor(t, doc)
				doc.Resolve(fd.Get("FontFile3")).(*object.Stream).Dict.Set("Subtype", object.Name("OpenType"))
			},
			rule: map[pdfa.Level]string{pdfa.PDFA1b: "6.3.2"},
			says: "/Subtype is /OpenType",
		},
	} {
		for level, wantRule := range tc.rule {
			t.Run(tc.name+"@"+level.String(), func(t *testing.T) {
				kind := textFontKinds[tc.kind]
				face := kind.face(t)
				doc := textDocument(t, face, kind.text(face), level)
				// Clean first: what is flagged is the break.
				back, _ := writeAndRead(t, doc)
				for _, v := range ValidatePDFA(back, level) {
					t.Fatalf("before the break: %s", v.Error())
				}
				tc.breaks(t, back)
				back, _ = writeAndRead(t, back)
				var found []string
				for _, v := range ValidatePDFA(back, level) {
					found = append(found, v.Rule+": "+v.Message)
					if wantRule == "" {
						t.Errorf("flagged: %s", v.Error())
					}
				}
				if wantRule == "" {
					return
				}
				ok := false
				for _, f := range found {
					if strings.HasPrefix(f, wantRule+": ") && strings.Contains(f, tc.says) {
						ok = true
					}
				}
				if !ok {
					t.Errorf("flagged %v; want %s saying %q", found, wantRule, tc.says)
				}
			})
		}
	}
}
