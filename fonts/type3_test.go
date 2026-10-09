package fonts

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/forme/shape"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
)

// A face whose glyphs are only bitmaps is drawn and embedded as Type 3 fonts:
// one per strike and, for a greyscale strike, per text colour, split into
// planes of 256 codes (type3.go, docs/proposals/bitmap-fonts-type3.md).
//
// The fixture is forme's Strikes.ttf, built for its strike reader: EBDT with
// no outlines, 26 glyphs for A to Y, a 1-bit strike at 12 ppem, a 2-bit one at
// 16, and strikes at 20, 24 and 32, the largest, which glyph 5 (E) is missing
// from.

var formeModuleDir = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/mgilbir/forme").Output()
	return strings.TrimSpace(string(out)), err
})

func formeTestFile(t *testing.T, rel string) []byte {
	t.Helper()
	dir, err := formeModuleDir()
	if err != nil || dir == "" {
		t.Fatalf("locating the forme module: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading forme's %s: %v", rel, err)
	}
	return data
}

func strikesFace(t *testing.T) *Face {
	t.Helper()
	f, err := Load(formeTestFile(t, "testdata/freetype/fonts/Strikes.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.BitmapOnly() || !f.isType3() {
		t.Fatal("the fixture is not a bitmap-only face")
	}
	return f
}

func inflate(t *testing.T, s *object.Stream) []byte {
	t.Helper()
	r, err := zlib.NewReader(bytes.NewReader(s.Data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// drawIn draws s in face at size into a fresh stream that selected it as F1,
// with whatever the setup did to the stream first.
func drawIn(t *testing.T, face *Face, s string, size float64, setup func(*content.Builder)) string {
	t.Helper()
	var b content.Builder
	if setup != nil {
		setup(&b)
	}
	b.BeginText().SetFont("F1", size)
	if missing := face.DrawShaped(&b, s, size); missing != 0 {
		t.Fatalf("%d of %q missing", missing, s)
	}
	b.EndText()
	out, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// type3Font is one embedded sub-font, read back.
type type3Font struct {
	dict   *object.Dictionary
	procs  map[object.Name]string // glyph name -> procedure
	names  []object.Name          // code -> glyph name
	widths []float64
	cmap   string
}

func readType3(t *testing.T, a *allocator, ref object.IndirectRef) type3Font {
	t.Helper()
	d := a.dict(t, ref)
	if d.Get("Subtype") != object.Name("Type3") {
		t.Fatalf("font %v is %v, not Type3", ref, d.Get("Subtype"))
	}
	f := type3Font{dict: d, procs: map[object.Name]string{}}
	procs := d.Get("CharProcs").(*object.Dictionary)
	for name, r := range procs.All() {
		f.procs[name] = string(inflate(t, a.at(r).(*object.Stream)))
	}
	diff := d.Get("Encoding").(*object.Dictionary).Get("Differences").(object.Array)
	if diff[0] != object.Integer(0) {
		t.Fatalf("Differences start at %v", diff[0])
	}
	for _, n := range diff[1:] {
		f.names = append(f.names, n.(object.Name))
	}
	for _, w := range d.Get("Widths").(object.Array) {
		switch v := w.(type) {
		case object.Integer:
			f.widths = append(f.widths, float64(v))
		case object.Real:
			f.widths = append(f.widths, float64(v))
		}
	}
	if last := d.Get("LastChar"); last != object.Integer(len(f.names)-1) || len(f.widths) != len(f.names) {
		t.Fatalf("LastChar %v, %d widths, for %d codes", last, len(f.widths), len(f.names))
	}
	f.cmap = string(inflate(t, a.at(d.Get("ToUnicode")).(*object.Stream)))
	return f
}

// wx is the width a procedure states, its first operand.
func wx(proc string) string { return strings.Fields(proc)[0] }

func TestABitmapFaceIsDrawnInTheFontOfItsStrike(t *testing.T) {
	face := strikesFace(t)
	// 9pt is 12 CSS pixels: the 12 ppem strike, which is not the home
	// font's (the largest, 32), so the run switches to a sub-font and back.
	stream := drawIn(t, face, "ABC", 9, func(b *content.Builder) { b.SetRGB(1, 0, 0) })
	if !strings.Contains(stream, "/F1.1 9 Tf\n(\x00\x01\x02) Tj\n/F1 9 Tf\n") {
		t.Errorf("the run was not drawn in F1.1 and F1 selected after it:\n%q", stream)
	}

	a := &allocator{}
	e, err := face.Embed(a)
	if err != nil {
		t.Fatal(err)
	}
	home := readType3(t, a, e)
	if len(home.names) != 1 {
		t.Errorf("the home font has %d glyphs, want .notdef alone", len(home.names))
	}
	all, err := face.EmbedForms(&allocator{}, Forms{Horizontal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Subfonts) != 1 {
		t.Fatalf("%d sub-fonts beside the home font, want 1", len(all.Subfonts))
	}
	a = &allocator{}
	all, _ = face.EmbedForms(a, Forms{Horizontal: true})
	sub := readType3(t, a, all.Subfonts[0])
	if len(sub.names) != 3 {
		t.Fatalf("the sub-font has %d glyphs, want 3", len(sub.names))
	}
	for code, name := range sub.names {
		proc := sub.procs[name]
		// A 1-bit strike is a stencil: d1, which takes the text's colour.
		if !strings.Contains(proc, " d1\n") || !strings.Contains(proc, " Do\n") {
			t.Errorf("code %d's procedure is not a stencil:\n%s", code, proc)
		}
		if wx(proc) != "600" || sub.widths[code] != 600 {
			t.Errorf("code %d: wx %s, /Widths %v, want the face's advance, 600", code, wx(proc), sub.widths[code])
		}
	}
	for _, want := range []string{"<00> <0041>", "<01> <0042>", "<02> <0043>"} {
		if !strings.Contains(sub.cmap, want) {
			t.Errorf("the ToUnicode CMap has no %s:\n%s", want, sub.cmap)
		}
	}
}

func TestAGreyscaleStrikeIsPaintedInTheTextColour(t *testing.T) {
	face := strikesFace(t)
	// 12pt is 16 CSS pixels: the 2-bit strike.
	red := drawIn(t, face, "AB", 12, func(b *content.Builder) { b.SetRGB(1, 0, 0) })
	green := drawIn(t, face, "AB", 12, func(b *content.Builder) { b.SetRGB(0, 1, 0) })
	unknown := drawIn(t, face, "AB", 12, nil)
	for i, s := range []string{red, green, unknown} {
		want := "/F1." + string(rune('1'+i)) + " 12 Tf"
		if !strings.Contains(s, want) {
			t.Errorf("draw %d is not in its own sub-font %s:\n%q", i, want, s)
		}
	}

	a := &allocator{}
	e, err := face.EmbedForms(a, Forms{Horizontal: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Subfonts) != 3 {
		t.Fatalf("%d sub-fonts, want red, green and the stencils", len(e.Subfonts))
	}
	for i, wantRGB := range [][]byte{{255, 0, 0}, {0, 255, 0}} {
		f := readType3(t, a, e.Subfonts[i])
		for _, name := range f.names {
			proc := f.procs[name]
			if !strings.Contains(proc, " d0\n") {
				t.Errorf("sub-font %d's %s is not a coloured glyph:\n%s", i+1, name, proc)
			}
		}
		xobjects := f.dict.Get("Resources").(*object.Dictionary).Get("XObject").(*object.Dictionary)
		for imName, ref := range xobjects.All() {
			im := a.at(ref).(*object.Stream)
			if im.Dict.Get("ColorSpace") != object.Name("DeviceRGB") || im.Dict.Get("Width") != object.Integer(1) {
				t.Errorf("%s is not one DeviceRGB pixel: %v", imName, im.Dict.Get("ColorSpace"))
			}
			if got := inflate(t, im); !bytes.Equal(got, wantRGB) {
				t.Errorf("%s's colour is %v, want %v", imName, got, wantRGB)
			}
			mask := a.at(im.Dict.Get("SMask")).(*object.Stream)
			cov := inflate(t, mask)
			partial := false
			for _, v := range cov {
				partial = partial || (v != 0 && v != 255)
			}
			if !partial {
				t.Errorf("%s's soft mask has no partial coverage: the anti-aliasing is gone", imName)
			}
		}
	}
	// In a colour the stream has not set, the glyph is a stencil.
	f := readType3(t, a, e.Subfonts[2])
	for _, name := range f.names {
		if !strings.Contains(f.procs[name], " d1\n") {
			t.Errorf("a greyscale glyph in an unknown colour is not a stencil:\n%s", f.procs[name])
		}
	}
}

func TestAGlyphMissingFromItsStrikeIsDrawnFromTheNearest(t *testing.T) {
	face := strikesFace(t)
	gid, ok := face.GlyphID('E')
	if !ok {
		t.Fatal("no E")
	}
	if _, has := strikeImage(t, face, gid, 32); has {
		t.Fatal("the fixture's E is in the 32 ppem strike; this test needs it missing")
	}
	// 24pt is 32 CSS pixels.
	drawIn(t, face, "E", 24, nil)
	a := &allocator{}
	e, err := face.EmbedForms(a, Forms{Horizontal: true})
	if err != nil {
		t.Fatal(err)
	}
	f := readType3(t, a, e.Horizontal) // the 32 ppem strike is the home font's
	name := object.Name("g" + itoa(gid))
	proc, ok := f.procs[name]
	if !ok {
		t.Fatalf("E is not in the home font: %v", f.names)
	}
	if !strings.Contains(proc, " Do\n") {
		t.Fatalf("E is drawn blank rather than from another strike:\n%s", proc)
	}
	// The nearest strike to 32 that has E is 24: its image there, which is
	// not the 20 ppem strike's either.
	want, _ := strikeImage(t, face, gid, 24)
	if other, _ := strikeImage(t, face, gid, 20); other.Width == want.Width && other.Height == want.Height {
		t.Fatal("E is the same size at 20 and 24 ppem; this test cannot tell the strikes apart")
	}
	im := a.at(f.dict.Get("Resources").(*object.Dictionary).Get("XObject").(*object.Dictionary).Get("I1")).(*object.Stream)
	if im.Dict.Get("Width") != object.Integer(want.Width) || im.Dict.Get("Height") != object.Integer(want.Height) {
		t.Errorf("E's image is %v×%v, want the 24 ppem strike's %d×%d",
			im.Dict.Get("Width"), im.Dict.Get("Height"), want.Width, want.Height)
	}
}

// strikeImage is a glyph's image in the face's strike of a size, as forme
// reads it from that strike alone.
func strikeImage(t *testing.T, face *Face, gid, ppem int) (shape.Image, bool) {
	t.Helper()
	for _, s := range face.Strikes() {
		if s.PPEM() == ppem {
			return face.StrikeImage(gid, s)
		}
	}
	t.Fatalf("the face has no %d ppem strike", ppem)
	return shape.Image{}, false
}

func itoa(v int) string { return strconv.Itoa(v) }

func TestASubfontSplitsIntoPlanesWhenItsCodesRunOut(t *testing.T) {
	defer func(n int) { maxCodes = n }(maxCodes)
	maxCodes = 4
	face := strikesFace(t)
	const text = "ABCDEFGHIJ"
	stream := drawIn(t, face, text, 9, nil)
	a := &allocator{}
	e, err := face.EmbedForms(a, Forms{Horizontal: true})
	if err != nil {
		t.Fatal(err)
	}
	// Ten glyphs in planes of four: three planes, F1.1 to F1.3.
	if len(e.Subfonts) != 3 {
		t.Fatalf("%d sub-fonts, want 3 planes", len(e.Subfonts))
	}
	for k := 1; k <= 3; k++ {
		if !strings.Contains(stream, "/F1."+itoa(k)+" 9 Tf") {
			t.Errorf("the stream never selects plane %d:\n%q", k, stream)
		}
	}
	// What the planes' ToUnicode CMaps say, in the order the stream shows
	// codes, is the text.
	cmaps := map[string]map[byte]string{}
	for k, ref := range e.Subfonts {
		f := readType3(t, a, ref)
		if len(f.names) > 4 {
			t.Errorf("plane %d has %d codes", k+1, len(f.names))
		}
		m := map[byte]string{}
		for _, line := range strings.Split(f.cmap, "\n") {
			var code, r int
			if n, _ := sscanHex(line, &code, &r); n == 2 {
				m[byte(code)] = string(rune(r))
			}
		}
		cmaps["F1."+itoa(k+1)] = m
	}
	var got strings.Builder
	var cur string
	for _, line := range strings.Split(stream, "\n") {
		switch {
		case strings.HasSuffix(line, " Tf"):
			cur = strings.TrimPrefix(strings.Fields(line)[0], "/")
		case strings.HasSuffix(line, " Tj"):
			codes := strings.TrimSuffix(strings.TrimPrefix(line, "("), ") Tj")
			for i := 0; i < len(codes); i++ {
				got.WriteString(cmaps[cur][codes[i]])
			}
		}
	}
	if got.String() != text {
		t.Errorf("the planes spell %q, want %q", got.String(), text)
	}
}

// sscanHex reads a bfchar line "<cc> <uuuu>".
func sscanHex(line string, code, r *int) (int, error) {
	if len(line) != 11 || line[0] != '<' || line[3] != '>' || line[5] != '<' || line[10] != '>' {
		return 0, nil
	}
	parse := func(s string) int {
		v := 0
		for _, c := range s {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= int(c - '0')
			case c >= 'A' && c <= 'F':
				v |= int(c-'A') + 10
			}
		}
		return v
	}
	*code, *r = parse(line[1:3]), parse(line[6:10])
	return 2, nil
}

func TestABitmapFaceNeedsItsFontSelected(t *testing.T) {
	face := strikesFace(t)
	var b content.Builder
	b.BeginText()
	face.DrawShaped(&b, "A", 9)
	b.EndText()
	if _, err := b.Bytes(); !errors.Is(err, errType3NoFont) {
		t.Errorf("drawing with no font selected: %v", err)
	}
}

func TestEncodeWritesABitmapFacesHomeCodes(t *testing.T) {
	defer func(n int) { maxCodes = n }(maxCodes)
	maxCodes = 3
	face := strikesFace(t)
	// The home font's first code is .notdef; A and B take the other two,
	// and C has no code left.
	codes, missing := face.Encode("ABC")
	if !bytes.Equal(codes, []byte{1, 2}) || missing != 1 {
		t.Errorf("Encode = %v, %d missing; want [1 2], 1", codes, missing)
	}
}

func TestABitmapFaceHasNoVerticalForm(t *testing.T) {
	if _, err := strikesFace(t).Vertical(); !errors.Is(err, errNoType3Vertical) {
		t.Errorf("Vertical: %v", err)
	}
}

// withFSType is a font with its OS/2 fsType set to v.
func withFSType(t *testing.T, data []byte, v uint16) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	n := int(out[4])<<8 | int(out[5])
	for i := range n {
		rec := out[12+16*i:]
		if string(rec[:4]) == "OS/2" {
			off := int(rec[8])<<24 | int(rec[9])<<16 | int(rec[10])<<8 | int(rec[11])
			out[off+8], out[off+9] = byte(v>>8), byte(v)
			return out
		}
	}
	t.Fatal("the font has no OS/2 table")
	return nil
}

// TestABitmapFacesLicenceIsHonoured: a bitmap face is embedded as images of the
// glyphs drawn, which the bitmap-only bit permits and the restricted and
// no-subsetting bits forbid.
func TestABitmapFacesLicenceIsHonoured(t *testing.T) {
	data := formeTestFile(t, "testdata/freetype/fonts/Strikes.ttf")
	for _, tc := range []struct {
		name   string
		fsType uint16
		want   error
	}{
		{"installable", 0x0000, nil},
		{"bitmap embedding only", 0x0200, nil},
		{"preview and print", 0x0004, nil},
		{"restricted", 0x0002, ErrRestrictedLicense},
		{"no subsetting", 0x0100, ErrBitmapNoSubsetting},
	} {
		face, err := Load(withFSType(t, data, tc.fsType))
		if err != nil {
			t.Fatal(err)
		}
		if got, stated := face.EmbeddingPermissions(); !stated || uint16(got) != tc.fsType {
			t.Fatalf("%s: the patched font states fsType %#x (%v)", tc.name, got, stated)
		}
		drawIn(t, face, "AB", 9, nil)
		if _, err := face.Embed(&allocator{}); !errors.Is(err, tc.want) {
			t.Errorf("%s: Embed: %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestAGlyphPNGPastTheLimitIsRefusedUndecoded: a colour glyph's PNG header
// says how large an image decoding it allocates, and a font is untrusted. One
// past maxGlyphPixels is refused from its header, before any decoding.
func TestAGlyphPNGPastTheLimitIsRefusedUndecoded(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// The IHDR chunk follows the 8-byte signature: length, type, then width
	// and height, then its CRC over type and data.
	const ihdr = 8
	binary.BigEndian.PutUint32(data[ihdr+8:], 4096)
	binary.BigEndian.PutUint32(data[ihdr+12:], 4096)
	binary.BigEndian.PutUint32(data[ihdr+8+13:], crc32.ChecksumIEEE(data[ihdr+4:ihdr+8+13]))
	if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != 4096 {
		t.Fatalf("the crafted header does not read as 4096 wide: %v %v", cfg, err)
	}
	err := embedPNG(&capture{}, shape.Image{Format: shape.ImagePNG, Data: data}, false)
	if err == nil || !strings.Contains(err.Error(), "pixels a glyph may have") {
		t.Errorf("a 4096×4096 glyph PNG: %v", err)
	}
}
