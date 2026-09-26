package pdf0

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonts/notosans"
	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/fonts"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// A font's licence, as the font states it, and the size of what is embedded.
//
// The OS/2 table's fsType says whether a font may be embedded and how
// (OpenType, OS/2 "fsType"). Nothing here read it, so a Restricted License
// font was subsetted into documents without a word (audit 2026-09-22 C112).

// withFSType is the bundled Noto Sans program with its fsType set, which is
// how a font with any licence can be made without shipping one.
func withFSType(t *testing.T, fsType uint16) []byte {
	t.Helper()
	data := append([]byte(nil), notosans.Regular()...)
	n := int(binary.BigEndian.Uint16(data[4:]))
	for i := 0; i < n; i++ {
		rec := data[12+16*i:]
		if string(rec[:4]) == "OS/2" {
			off := binary.BigEndian.Uint32(rec[8:])
			binary.BigEndian.PutUint16(data[off+8:], fsType)
			return data
		}
	}
	t.Fatal("the bundled font has no OS/2 table")
	return nil
}

// embedWith draws a word with the face, embeds it on a page of a PDF/A
// document and returns the error, or the written file read back.
func embedWith(t *testing.T, face *fonts.Face, level pdfa.Level) (*Document, []byte, error) {
	t.Helper()
	doc := mustPDFADoc(t, level)
	var b content.Builder
	b.BeginText().SetFont("F1", 12).MoveText(20, 20)
	face.DrawShaped(&b, "Licence", 12)
	b.EndText()
	if _, err := doc.AddPage(Page{Width: 200, Height: 100, Content: &b,
		Faces: map[object.Name]*fonts.Face{"F1": face}}); err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatalf("writing: %v", err)
	}
	back, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	return back, buf.Bytes(), nil
}

func TestARestrictedLicenceFontIsNotEmbedded(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fsType uint16
		want   error
	}{
		{"restricted", 0x0002, fonts.ErrRestrictedLicense},
		{"bitmap-only", 0x0200, fonts.ErrBitmapEmbeddingOnly},
		{"restricted-bitmap", 0x0202, fonts.ErrRestrictedLicense},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for kind, load := range map[string]func([]byte) (*fonts.Face, error){
				"composite": fonts.Load,
				"simple":    fonts.LoadSimple,
				// A face that arrived without its bytes is held to its licence
				// by the subset, which carries the OS/2 table through.
				"adopted": func(data []byte) (*fonts.Face, error) {
					f, err := shape.Load(data)
					if err != nil {
						return nil, err
					}
					return fonts.Adopt(f), nil
				},
			} {
				face, err := load(withFSType(t, tc.fsType))
				if err != nil {
					t.Fatalf("%s: loading: %v", kind, err)
				}
				_, _, err = embedWith(t, face, pdfa.PDFA2b)
				if !errors.Is(err, tc.want) {
					t.Errorf("%s: embedding returned %v, want %v", kind, err, tc.want)
				}
			}
		})
	}
}

// TestPermissiveLicencesEmbed: Preview & Print and Editable permit what a PDF
// does, and where several usage bits are set the least restrictive wins.
func TestPermissiveLicencesEmbed(t *testing.T) {
	for _, fsType := range []uint16{0x0000, 0x0004, 0x0008, 0x0006, 0x000A} {
		face, err := fonts.Load(withFSType(t, fsType))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := embedWith(t, face, pdfa.PDFA2b); err != nil {
			t.Errorf("fsType %#04x: %v", fsType, err)
		}
	}
}

// TestANoSubsettingFontIsEmbeddedWhole: fsType 0x0100 permits embedding only
// the whole font. The program in the file is the one loaded, byte for byte,
// named without a subset tag, and the document still validates.
func TestANoSubsettingFontIsEmbeddedWhole(t *testing.T) {
	for kind, load := range map[string]func([]byte) (*fonts.Face, error){
		"composite": fonts.Load,
		"simple":    fonts.LoadSimple,
	} {
		for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
			original := withFSType(t, 0x0104)
			face, err := load(original)
			if err != nil {
				t.Fatal(err)
			}
			back, _, err := embedWith(t, face, level)
			if err != nil {
				t.Fatalf("%s %s: %v", kind, level, err)
			}
			fd := fontDescriptorOf(t, back)
			key := "FontFile2"
			st, ok := back.Resolve(fd.Get(object.Name(key))).(*object.Stream)
			if !ok {
				t.Fatalf("%s: no /%s", kind, key)
			}
			program, err := back.StreamData(st)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(program, original) {
				t.Errorf("%s %s: the embedded program is %d bytes, not the %d-byte font loaded",
					kind, level, len(program), len(original))
			}
			if name, _ := back.Resolve(fd.Get("FontName")).(object.Name); strings.Contains(string(name), "+") {
				t.Errorf("%s: a whole font was named as a subset: %s", kind, name)
			}
			for _, v := range ValidatePDFA(back, level) {
				t.Errorf("%s %s: %s", kind, level, v.Error())
			}
		}
	}
	// A face from Adopt has no program to embed whole, and says so.
	f, err := shape.Load(withFSType(t, 0x0100))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := embedWith(t, fonts.Adopt(f), pdfa.PDFA2b); err == nil ||
		!strings.Contains(err.Error(), "forbids subsetting") {
		t.Errorf("an adopted no-subsetting face embedded, or failed for another reason: %v", err)
	}
}

// fontDescriptorOf finds the one font descriptor in a document.
func fontDescriptorOf(t *testing.T, doc *Document) *object.Dictionary {
	t.Helper()
	for _, o := range doc.Objects {
		if d, ok := o.Value.(*object.Dictionary); ok {
			if ty, _ := d.Get("Type").(object.Name); ty == "FontDescriptor" {
				return d
			}
		}
	}
	t.Fatal("no font descriptor")
	return nil
}

// TestEmbeddedFontStreamsAreCompressed: the program, /CIDSet and /ToUnicode
// are Flate streams, and each decodes to what a reader needs — /Length1 is the
// program's decoded length (C110).
func TestEmbeddedFontStreamsAreCompressed(t *testing.T) {
	face, err := fonts.NotoSans()
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := embedWith(t, face, pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	fd := fontDescriptorOf(t, back)
	program, _ := back.Resolve(fd.Get("FontFile2")).(*object.Stream)
	cidSet, _ := back.Resolve(fd.Get("CIDSet")).(*object.Stream)
	var toUnicode *object.Stream
	for _, o := range back.Objects {
		if d, ok := o.Value.(*object.Dictionary); ok {
			if st, _ := d.Get("Subtype").(object.Name); st == "Type0" {
				toUnicode, _ = back.Resolve(d.Get("ToUnicode")).(*object.Stream)
			}
		}
	}
	for name, st := range map[string]*object.Stream{"program": program, "CIDSet": cidSet, "ToUnicode": toUnicode} {
		if st == nil {
			t.Fatalf("no %s stream", name)
		}
		if f, _ := st.Dict.Get("Filter").(object.Name); f != "FlateDecode" {
			t.Errorf("the %s stream is not Flate-compressed (/Filter %v)", name, st.Dict.Get("Filter"))
		}
		if _, err := back.StreamData(st); err != nil {
			t.Errorf("the %s stream does not decode: %v", name, err)
		}
	}
	decoded, _ := back.StreamData(program)
	if l1, _ := program.Dict.Get("Length1").(object.Integer); int(l1) != len(decoded) {
		t.Errorf("/Length1 is %d; the program decodes to %d bytes", l1, len(decoded))
	}
}
