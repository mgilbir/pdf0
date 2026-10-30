package pdf0

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/gopenjpeg"
	"github.com/mgilbir/pdf0/internal/signtest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Single-defect files, made here: a PDF/A skeleton veraPDF passes at each
// part, given one page and one thing wrong with it — or one thing that only
// looks wrong. Each records the clauses veraPDF fails it on at every level
// (defectLevels), and pdf0 must report exactly those. The page's content is
// marked as an artifact, so that at Level A it is tagged as it must be. The
// files are built by the
// test, never committed, and when PDF0_VERAPDF names veraPDF it is run over
// them too, so the recorded answers cannot drift from it.
//
// They pin what the corpus does not test: most of them are rules where pdf0
// once said more than veraPDF (a non-standard rendering intent or PostScript
// at PDF/A-4, which its profile has no rule for; a name no resource
// dictionary defines at PDF/A-1 and -2; a non-widget annotation without /AP
// at PDF/A-1) or less (BitsPerComponent, FileAttachment at PDF/A-4).

// defectFixture is one file: what is wrong with it, and veraPDF's clauses at
// 1b, 2b and 4 — none for a file it passes. Every other level is held to its
// part's column (1a to 1b's; 2u, 2a and part 3 to 2b's; 4e and 4f to 4's)
// unless except records what veraPDF says there instead.
type defectFixture struct {
	name  string
	build func(d *Document, page, res *object.Dictionary) string // the page's content
	// patch, when set, changes the written file: for a defect in the bytes
	// rather than the objects.
	patch  func([]byte) []byte
	signed bool // written with WriteSigned, before patch
	want   [3][]string
	except map[string][]string // by veraPDF flavour
	// beyond are clauses pdf0 reports, by flavour, that veraPDF's profile
	// has no rule for: requirements of the ISO 19005 part itself, each
	// said where it is recorded. veraPDF is held to want, pdf0 to want and
	// beyond together.
	beyond map[string][]string
}

// defectLevels are the levels the fixtures are built and judged at, each with
// the want column it is held to by default.
var defectLevels = []struct {
	level   pdfa.Level
	flavour string
	column  int
}{
	{pdfa.PDFA1b, "1b", 0}, {pdfa.PDFA1a, "1a", 0},
	{pdfa.PDFA2b, "2b", 1}, {pdfa.PDFA2u, "2u", 1}, {pdfa.PDFA2a, "2a", 1},
	{pdfa.PDFA3b, "3b", 1}, {pdfa.PDFA3u, "3u", 1}, {pdfa.PDFA3a, "3a", 1},
	{pdfa.PDFA4, "4", 2}, {pdfa.PDFA4E, "4e", 2}, {pdfa.PDFA4F, "4f", 2},
}

// pdf0WantAt is the clauses pdf0 must report for a level: veraPDF's and
// f's beyond.
func (f defectFixture) pdf0WantAt(flavour string, column int) []string {
	want := append(f.wantAt(flavour, column), f.beyond[flavour]...)
	slices.Sort(want)
	return slices.Compact(want)
}

// wantAt is the clauses f records for a level.
func (f defectFixture) wantAt(flavour string, column int) []string {
	want, ok := f.except[flavour]
	if !ok {
		want = f.want[column]
	}
	want = slices.Clone(want)
	slices.Sort(want)
	return want
}

func fxInt(v int) object.Integer { return object.Integer(v) }

// fxAnnot puts a on the page as its one annotation, printable, 100pt square.
func fxAnnot(d *Document, page, a *object.Dictionary) {
	a.Set("Type", object.Name("Annot"))
	a.Set("Rect", object.Array{fxInt(10), fxInt(10), fxInt(110), fxInt(110)})
	a.Set("F", fxInt(4))
	page.Set("Annots", object.Array{d.Add(a)})
}

// fxAppearance is an empty form XObject, an appearance stream.
func fxAppearance(d *Document) object.IndirectRef {
	return d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Form")},
		object.Entry{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(100), fxInt(100)}},
	), nil))
}

// fxImage draws a 1×1 DeviceGray image of the given BitsPerComponent; tweak
// changes its dictionary first.
func fxImage(d *Document, res *object.Dictionary, bpc int, tweak func(*object.Dictionary)) string {
	dict := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Image")},
		object.Entry{Key: "Width", Value: fxInt(1)},
		object.Entry{Key: "Height", Value: fxInt(1)},
		object.Entry{Key: "ColorSpace", Value: object.Name("DeviceGray")},
		object.Entry{Key: "BitsPerComponent", Value: fxInt(bpc)},
	)
	if tweak != nil {
		tweak(dict)
	}
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "Im0", Value: d.Add(object.NewStream(dict, make([]byte, (bpc+7)/8)))}))
	return "q 10 0 0 10 0 0 cm /Im0 Do Q"
}

// fxMask is a 1x1 stencil mask image of bpc bits, for an image's /Mask.
func fxMask(d *Document, bpc int) object.IndirectRef {
	return d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Image")},
		object.Entry{Key: "Width", Value: fxInt(1)},
		object.Entry{Key: "Height", Value: fxInt(1)},
		object.Entry{Key: "ImageMask", Value: object.Boolean(true)},
		object.Entry{Key: "BitsPerComponent", Value: fxInt(bpc)},
	), []byte{0}))
}

// fxCatalog is the document's catalog.
func fxCatalog(d *Document) *object.Dictionary { return d.ResolveDict(d.Trailer.Get("Root")) }

// fxXMP replaces the catalog's metadata with edit's change to it.
func fxXMP(d *Document, edit func(xmp []byte) []byte) {
	cat := fxCatalog(d)
	data, err := d.StreamData(d.Resolve(cat.Get("Metadata")).(*object.Stream))
	if err != nil {
		panic(err)
	}
	cat.Set("Metadata", d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Metadata")},
		object.Entry{Key: "Subtype", Value: object.Name("XML")},
	), edit(data))))
}

// fxStreamForm draws an empty form XObject whose stream has the given
// filter entries and encoded data.
func fxStreamForm(d *Document, res *object.Dictionary, data []byte, filter ...object.Entry) string {
	dict := object.NewDictionary(append([]object.Entry{
		{Key: "Type", Value: object.Name("XObject")},
		{Key: "Subtype", Value: object.Name("Form")},
		{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(10), fxInt(10)}},
	}, filter...)...)
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "X0", Value: d.Add(object.NewStream(dict, data))}))
	return "/X0 Do"
}

// fxEmbed makes the document carry one embedded file, data of the given
// MIME type, under a file specification that tweak changes first.
func fxEmbed(d *Document, data []byte, mime string, tweak func(fs *object.Dictionary)) {
	ef := d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("EmbeddedFile")},
		object.Entry{Key: "Subtype", Value: object.Name(mime)},
	), data))
	fs := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Filespec")},
		object.Entry{Key: "F", Value: object.String{Value: []byte("a.pdf")}},
		object.Entry{Key: "UF", Value: object.String{Value: []byte("a.pdf")}},
		object.Entry{Key: "Desc", Value: object.String{Value: []byte("an attachment")}},
		object.Entry{Key: "AFRelationship", Value: object.Name("Unspecified")},
		object.Entry{Key: "EF", Value: object.NewDictionary(object.Entry{Key: "F", Value: ef}, object.Entry{Key: "UF", Value: ef})},
	)
	if tweak != nil {
		tweak(fs)
	}
	fsRef := d.Add(fs)
	cat := fxCatalog(d)
	cat.Set("Names", object.NewDictionary(object.Entry{Key: "EmbeddedFiles", Value: object.NewDictionary(
		object.Entry{Key: "Names", Value: object.Array{object.String{Value: []byte("a.pdf")}, fsRef}})}))
	cat.Set("AF", object.Array{fsRef})
}

// fxPDFA is a PDF/A file of the level with one empty page, to embed.
func fxPDFA(level pdfa.Level) []byte {
	d, err := NewPDFADocument(level)
	if err != nil {
		panic(err)
	}
	pagesRef := fxCatalog(d).Get("Pages")
	page := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Page")},
		object.Entry{Key: "Parent", Value: pagesRef},
		object.Entry{Key: "MediaBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(200), fxInt(200)}},
		object.Entry{Key: "Resources", Value: &object.Dictionary{}},
	)
	pages := d.ResolveDict(pagesRef)
	pages.Set("Kids", object.Array{d.Add(page)})
	pages.Set("Count", fxInt(1))
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// fxOC gives the document optional content: two groups, both in the default
// configuration's /Order, which tweak changes first.
func fxOC(d *Document, tweak func(ocp, config *object.Dictionary, groups object.Array)) {
	var groups object.Array
	for _, n := range []string{"one", "two"} {
		groups = append(groups, d.Add(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("OCG")},
			object.Entry{Key: "Name", Value: object.String{Value: []byte(n)}})))
	}
	config := object.NewDictionary(
		object.Entry{Key: "Name", Value: object.String{Value: []byte("default")}},
		object.Entry{Key: "Order", Value: groups},
	)
	ocp := object.NewDictionary(object.Entry{Key: "OCGs", Value: groups}, object.Entry{Key: "D", Value: config})
	if tweak != nil {
		tweak(ocp, config, groups)
	}
	fxCatalog(d).Set("OCProperties", ocp)
}

// fxForm draws a form XObject whose dictionary has extra entries.
func fxForm(d *Document, res *object.Dictionary, body string, extra ...object.Entry) string {
	dict := object.NewDictionary(append([]object.Entry{
		{Key: "Type", Value: object.Name("XObject")},
		{Key: "Subtype", Value: object.Name("Form")},
		{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(10), fxInt(10)}},
	}, extra...)...)
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "X0", Value: d.Add(object.NewStream(dict, []byte(body)))}))
	return "/X0 Do"
}

// fxTrueTypeProgram is a TrueType program with glyph 1 for 'A', 500 units
// wide; cmap, when not nil, replaces its character map's subtables.
func fxTrueTypeProgram(cmap []fonttest.CmapSub) []byte {
	return fxTrueTypeProgramWith(cmap, nil)
}

// fxTrueTypeProgramWith is fxTrueTypeProgram with extra tables besides.
func fxTrueTypeProgramWith(cmap []fonttest.CmapSub, extra map[string][]byte) []byte {
	opts := fonttest.SFNTOptions{Name: "Test", Glyphs: []fonttest.Glyph{{Rune: 'A', Advance: 500, HasShape: true}}, Extra: extra}
	if cmap != nil {
		if opts.Extra == nil {
			opts.Extra = map[string][]byte{}
		}
		// The one-table font SFNTWithCmapSubtables makes holds the cmap
		// table at offset 28.
		opts.Extra["cmap"] = fonttest.SFNTWithCmapSubtables(cmap)[28:]
	}
	return fonttest.SFNT(opts)
}

// fxPostNames is a version 2.0 post table naming fxTrueTypeProgram's glyph 1
// by the given Macintosh standard name index (36 is "A", 37 "B"); the
// program's own post table names none.
func fxPostNames(glyph1 byte) []byte {
	post := make([]byte, 32+2+2*2)
	post[1] = 2       // version 2.0
	post[33] = 2      // numGlyphs
	post[37] = glyph1 // glyph 1; glyph 0 is name 0, .notdef
	return post
}

// fxFormat0 is a format 0 cmap subtable mapping code 'A' to glyph 1.
func fxFormat0() []byte {
	b := make([]byte, 262)
	b[3] = 6 // length 262, high byte 1
	b[2] = 1
	b[6+'A'] = 1
	return b
}

// fxFont shows "A" in a TrueType font over program, non-symbolic under
// WinAnsiEncoding unless symbolic; tweak changes the font dictionary first.
func fxFont(d *Document, res *object.Dictionary, program []byte, symbolic bool, tweak func(font *object.Dictionary)) string {
	flags := 32
	if symbolic {
		flags = 4
	}
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("Test")},
		object.Entry{Key: "Flags", Value: fxInt(flags)},
		object.Entry{Key: "FontBBox", Value: object.Array{fxInt(0), fxInt(-200), fxInt(1000), fxInt(800)}},
		object.Entry{Key: "ItalicAngle", Value: fxInt(0)},
		object.Entry{Key: "Ascent", Value: fxInt(800)},
		object.Entry{Key: "Descent", Value: fxInt(-200)},
		object.Entry{Key: "CapHeight", Value: fxInt(700)},
		object.Entry{Key: "StemV", Value: fxInt(80)},
		object.Entry{Key: "FontFile2", Value: d.Add(object.NewStream(object.NewDictionary(
			object.Entry{Key: "Length1", Value: fxInt(len(program))}), program))},
	)
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("TrueType")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "FirstChar", Value: fxInt('A')},
		object.Entry{Key: "LastChar", Value: fxInt('A')},
		object.Entry{Key: "Widths", Value: object.Array{fxInt(500)}},
		object.Entry{Key: "FontDescriptor", Value: d.Add(fd)},
	)
	if !symbolic {
		font.Set("Encoding", object.Name("WinAnsiEncoding"))
	}
	if tweak != nil {
		tweak(font)
	}
	res.Set("Font", object.NewDictionary(object.Entry{Key: "F1", Value: d.Add(font)}))
	return "BT /F1 12 Tf 10 10 Td (A) Tj ET"
}

// fxICCFill fills with an ICCBased colour space over the skeleton's own
// output profile's bytes, patched by patch.
func fxICCFill(d *Document, res *object.Dictionary, patch func([]byte)) string {
	cat := d.ResolveDict(d.Trailer.Get("Root"))
	oi := d.ResolveDict(d.Resolve(cat.Get("OutputIntents")).(object.Array)[0])
	src, err := d.StreamData(d.Resolve(oi.Get("DestOutputProfile")).(*object.Stream))
	if err != nil {
		panic(err)
	}
	prof := append([]byte(nil), src...)
	if patch != nil {
		patch(prof)
	}
	ref := d.Add(object.NewStream(object.NewDictionary(object.Entry{Key: "N", Value: fxInt(3)}), prof))
	res.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Array{object.Name("ICCBased"), ref}}))
	return "/CS0 cs 1 0 0 sc 0 0 10 10 re f"
}

var defectFixtures = []defectFixture{
	{name: "base", build: func(d *Document, p, r *object.Dictionary) string { return "" }},
	{name: "image-bpc8", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 8, nil) }},
	{name: "image-bpc16", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 16, nil) },
		want: [3][]string{{"6.2.4"}, nil, nil}},
	{name: "image-bpc12", build: func(d *Document, p, r *object.Dictionary) string { return fxImage(d, r, 12, nil) },
		want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "inline-image-bpc12", build: func(d *Document, p, r *object.Dictionary) string {
		return "q 10 0 0 10 0 0 cm BI /W 1 /H 1 /BPC 12 /CS /G ID \x00\x00 EI Q"
	}, want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "image-intent-custom", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) { i.Set("Intent", object.Name("Custom")) })
	}, want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "ri-custom", build: func(d *Document, p, r *object.Dictionary) string { return "/Custom ri" },
		want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "extgstate-ri-custom", build: func(d *Document, p, r *object.Dictionary) string {
		r.Set("ExtGState", object.NewDictionary(object.Entry{Key: "G0", Value: object.NewDictionary(object.Entry{Key: "RI", Value: object.Name("Custom")})}))
		return "/G0 gs"
	}, want: [3][]string{{"6.2.9"}, {"6.2.6"}, nil}},
	{name: "undefined-operator", build: func(d *Document, p, r *object.Dictionary) string { return "q BogusOp Q" },
		want: [3][]string{{"6.2.10"}, {"6.2.2"}, {"6.2.2"}}},
	{name: "xobject-defined-nowhere", build: func(d *Document, p, r *object.Dictionary) string { return "q /X0 Do Q" },
		want: [3][]string{nil, nil, {"6.2.2"}}},
	{name: "form-inherits-colour-space", build: func(d *Document, p, r *object.Dictionary) string {
		r.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Array{object.Name("CalGray"),
			object.NewDictionary(object.Entry{Key: "WhitePoint", Value: object.Array{object.Real(0.9505), fxInt(1), object.Real(1.089)}})}}))
		return fxForm(d, r, "/CS0 cs 0.5 sc 0 0 5 5 re f")
	}, want: [3][]string{nil, {"6.2.2"}, {"6.2.2"}}},
	{name: "postscript-xobject", build: func(d *Document, p, r *object.Dictionary) string {
		ps := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("PS")},
		), []byte("%!PS"))
		r.Set("XObject", object.NewDictionary(object.Entry{Key: "P0", Value: d.Add(ps)}))
		return "/P0 Do"
	}, want: [3][]string{{"6.2.7"}, {"6.2.9"}, nil}},
	{name: "form-ps-entry", build: func(d *Document, p, r *object.Dictionary) string {
		return fxForm(d, r, "0 0 5 5 re f",
			object.Entry{Key: "PS", Value: d.Add(object.NewStream(&object.Dictionary{}, []byte("%!")))},
			object.Entry{Key: "Resources", Value: &object.Dictionary{}})
	}, want: [3][]string{{"6.2.5"}, {"6.2.9"}, nil}},
	{name: "output-profile-n-mismatch", build: func(d *Document, p, r *object.Dictionary) string {
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		oi := d.ResolveDict(d.Resolve(cat.Get("OutputIntents")).(object.Array)[0])
		d.Resolve(oi.Get("DestOutputProfile")).(*object.Stream).Dict.Set("N", fxInt(4))
		return ""
	}, want: [3][]string{{"6.2.3.2"}, nil, nil}},
	{name: "output-profile-without-n", build: func(d *Document, p, r *object.Dictionary) string {
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		oi := d.ResolveDict(d.Resolve(cat.Get("OutputIntents")).(object.Array)[0])
		d.Resolve(oi.Get("DestOutputProfile")).(*object.Stream).Dict.Delete("N")
		return ""
	}, want: [3][]string{{"6.2.3.2"}, nil, nil}},
	{name: "xmp-not-well-formed", build: func(d *Document, p, r *object.Dictionary) string {
		cat := d.ResolveDict(d.Trailer.Get("Root"))
		md := d.Resolve(cat.Get("Metadata")).(*object.Stream)
		data, err := d.StreamData(md)
		if err != nil {
			panic(err)
		}
		bad := bytes.Replace(data, []byte("</rdf:RDF>"), []byte("</rdf:Rdf>"), 1)
		cat.Set("Metadata", d.Add(object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Metadata")},
			object.Entry{Key: "Subtype", Value: object.Name("XML")},
		), bad)))
		return ""
	}, want: [3][]string{{"6.7.11", "6.7.9"}, {"6.6.2.1", "6.6.4"}, {"6.7.2.1", "6.7.3"}}},
	{name: "truetype-font", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, nil)
	}, want: [3][]string{nil, nil, nil}},
	{name: "font-without-type", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Delete("Type") })
	}, want: [3][]string{{"6.3.2"}, {"6.2.11.2"}, {"6.2.10.2"}}},
	{name: "font-unknown-subtype", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Set("Subtype", object.Name("TrueType2")) })
	}, want: [3][]string{nil, nil, {"6.2.2"}}},
	{name: "font-without-basefont", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Delete("BaseFont") })
	}, want: [3][]string{{"6.3.2"}, {"6.2.11.2"}, {"6.2.10.2"}}},
	{name: "font-without-firstchar", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Delete("FirstChar") })
	}, want: [3][]string{{"6.3.2", "6.3.6"}, {"6.2.11.2", "6.2.11.5"}, {"6.2.10.2", "6.2.10.5"}}},
	{name: "font-without-lastchar", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Delete("LastChar") })
	}, want: [3][]string{{"6.3.2", "6.3.6"}, {"6.2.11.2", "6.2.11.5"}, {"6.2.10.2", "6.2.10.5"}}},
	{name: "font-widths-wrong-size", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Set("Widths", object.Array{fxInt(500), fxInt(500)}) })
	}, want: [3][]string{{"6.3.2"}, {"6.2.11.2"}, {"6.2.10.2"}}},
	{name: "nonsymbolic-truetype-without-31-or-10-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 0, Enc: 3, Data: fxFormat0()}}), false, nil)
	}, want: [3][]string{{"6.3.5", "6.3.6"}, {"6.2.11.4.1", "6.2.11.5"}, {"6.2.10.4.1", "6.2.10.5", "6.2.10.6"}}},
	{name: "nonsymbolic-truetype-with-only-30-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 3, Enc: 0, Data: fxFormat0()}}), false, nil)
	}, want: [3][]string{{"6.3.5", "6.3.6"}, {"6.2.11.4.1", "6.2.11.5", "6.2.11.6"}, {"6.2.10.4.1", "6.2.10.5", "6.2.10.6"}}},
	{name: "nonsymbolic-truetype-without-31-or-10-cmap-with-post-names", build: func(d *Document, p, r *object.Dictionary) string {
		program := fxTrueTypeProgramWith([]fonttest.CmapSub{{Plat: 0, Enc: 3, Data: fxFormat0()}}, map[string][]byte{"post": fxPostNames(36)})
		return fxFont(d, r, program, false, nil)
	}, want: [3][]string{nil, nil, {"6.2.10.6"}}},
	{name: "nonsymbolic-truetype-without-31-or-10-cmap-post-names-another-glyph", build: func(d *Document, p, r *object.Dictionary) string {
		// The (0,3) cmap maps "A" to glyph 1, and the post table names glyph
		// 1 "B": looked up by its name, as a reader must here, "A" has no
		// glyph.
		program := fxTrueTypeProgramWith([]fonttest.CmapSub{{Plat: 0, Enc: 3, Data: fxFormat0()}}, map[string][]byte{"post": fxPostNames(37)})
		return fxFont(d, r, program, false, nil)
	}, want: [3][]string{{"6.3.5", "6.3.6"}, {"6.2.11.4.1", "6.2.11.5"}, {"6.2.10.4.1", "6.2.10.5", "6.2.10.6"}}},
	{name: "symbolic-truetype-without-30-or-10-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 3, Enc: 1, Data: fxFormat0()}}), true, nil)
	}, want: [3][]string{{"6.3.5"}, {"6.2.11.4.1"}, {"6.2.10.4.1", "6.2.10.6"}},
		except: map[string][]string{"1a": {"6.3.5", "6.3.8"}, "2u": {"6.2.11.4.1", "6.2.11.7.2"}, "2a": {"6.2.11.4.1", "6.2.11.7.2"}, "3u": {"6.2.11.4.1", "6.2.11.7.2"}, "3a": {"6.2.11.4.1", "6.2.11.7.2"}}},
	{name: "image-mask-bpc2", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 2, func(i *object.Dictionary) { i.Delete("ColorSpace"); i.Set("ImageMask", object.Boolean(true)) })
	}, want: [3][]string{nil, nil, nil}},
	{name: "explicit-mask-bpc2", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) { i.Set("Mask", fxMask(d, 2)) })
	}, want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "explicit-mask-bpc1", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) { i.Set("Mask", fxMask(d, 1)) })
	}, want: [3][]string{nil, nil, nil}},
	{name: "iccbased-fill", build: func(d *Document, p, r *object.Dictionary) string { return fxICCFill(d, r, nil) }, want: [3][]string{nil, nil, nil}},
	{name: "iccbased-device-link-profile", build: func(d *Document, p, r *object.Dictionary) string {
		return fxICCFill(d, r, func(b []byte) { copy(b[12:16], "link") })
	}, want: [3][]string{{"6.2.3.2"}, {"6.2.4.2"}, {"6.2.4.2"}}},
	{name: "iccbased-xyz-profile", build: func(d *Document, p, r *object.Dictionary) string {
		return fxICCFill(d, r, func(b []byte) { copy(b[16:20], "XYZ ") })
	}, want: [3][]string{{"6.2.3.2"}, {"6.2.4.2"}, {"6.2.4.2"}}},
	{name: "text-annotation-appearance-states", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Text")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: object.NewDictionary(
				object.Entry{Key: "On", Value: fxAppearance(d)})})}))
		return ""
	}, want: [3][]string{{"6.5.3"}, {"6.3.3"}, {"6.3.3"}}},
	{name: "text-annotation-without-ap", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Text")}))
		return ""
	}, want: [3][]string{nil, {"6.3.3"}, {"6.3.3"}}},
	{name: "widget-without-ap", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Widget")},
			object.Entry{Key: "FT", Value: object.Name("Tx")}, object.Entry{Key: "T", Value: object.String{Value: []byte("f")}}))
		return ""
	}, want: [3][]string{{"6.9"}, {"6.3.3"}, {"6.3.3"}}},
	{name: "popup-ap-with-down", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Popup")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)}, object.Entry{Key: "D", Value: fxAppearance(d)})}))
		return ""
	}, want: [3][]string{{"6.5.3"}, {"6.3.3"}, {"6.3.3"}}},
	{name: "file-attachment-annotation", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("FileAttachment")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})}))
		return ""
	}, want: [3][]string{{"6.5.2"}, nil, {"6.3.1"}},
		except: map[string][]string{"4f": nil}},
	{name: "stream-length-wrong", build: func(d *Document, p, r *object.Dictionary) string {
		return "0 0 m 1 1 l S"
	},
		patch: fxSameLength("<< /Length 31 >>\nstream\r\n/Artifact", "<< /Length 30 >>\nstream\r\n/Artifact"), want: [3][]string{{"6.1.7"}, {"6.1.7.1"}, {"6.1.6.1"}}},
	{name: "stream-keyword-cr-only", build: func(d *Document, p, r *object.Dictionary) string {
		return "0 0 m 1 1 l S"
	},
		patch: fxSameLength("<< /Length 31 >>\nstream\r\n/Artifact", "<< /Length 31  >>\nstream\r/Artifact"), want: [3][]string{{"6.1.7"}, {"6.1.7.1"}, nil}},
	{name: "endstream-without-eol", build: func(d *Document, p, r *object.Dictionary) string {
		return "0 0 m 1 1 l S"
	},
		patch: fxSameLength("EMC\nendstream", "EMC endstream"), want: [3][]string{{"6.1.7"}, {"6.1.7.1"}, {"6.1.6.1"}}},
	{name: "stream-ffilter", build: func(d *Document, p, r *object.Dictionary) string {
		return fxStreamForm(d, r, nil, object.Entry{Key: "FFilter", Value: object.Name("FlateDecode")})
	}, want: [3][]string{{"6.1.7"}, {"6.1.7.1"}, {"6.1.6.1"}}},
	{name: "array-8192-elements", build: func(d *Document, p, r *object.Dictionary) string {
		arr := make(object.Array, 8192)
		for i := range arr {
			arr[i] = fxInt(0)
		}
		p.Set("PieceInfo", object.NewDictionary(object.Entry{Key: "X", Value: object.NewDictionary(object.Entry{Key: "Private", Value: arr})}))
		return ""
	}, want: [3][]string{{"6.1.12"}, nil, nil}},
	{name: "dictionary-4096-entries", build: func(d *Document, p, r *object.Dictionary) string {
		dict := &object.Dictionary{}
		for i := range 4096 {
			dict.Set(object.Name(fmt.Sprintf("K%d", i)), fxInt(0))
		}
		p.Set("PieceInfo", object.NewDictionary(object.Entry{Key: "X", Value: object.NewDictionary(object.Entry{Key: "Private", Value: dict})}))
		return ""
	}, want: [3][]string{{"6.1.12"}, nil, nil}},
	{name: "output-intents-different-profile-objects", build: func(d *Document, p, r *object.Dictionary) string {
		cat := fxCatalog(d)
		ois := d.Resolve(cat.Get("OutputIntents")).(object.Array)
		x := d.ResolveDict(ois[0]).Clone()
		x.Set("S", object.Name("GTS_PDFX"))
		prof := d.Resolve(x.Get("DestOutputProfile")).(*object.Stream)
		x.Set("DestOutputProfile", d.Add(object.NewStream(prof.Dict.Clone(), prof.Data)))
		cat.Set("OutputIntents", append(slices.Clone(ois), d.Add(x)))
		return ""
	}, want: [3][]string{{"6.2.2"}, {"6.2.3"}, {"6.2.3"}}},
	{name: "output-intents-same-profile-object", build: func(d *Document, p, r *object.Dictionary) string {
		cat := fxCatalog(d)
		ois := d.Resolve(cat.Get("OutputIntents")).(object.Array)
		x := d.ResolveDict(ois[0]).Clone()
		x.Set("S", object.Name("GTS_PDFX"))
		cat.Set("OutputIntents", append(slices.Clone(ois), d.Add(x)))
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "iccbased-profile-not-a-stream", build: func(d *Document, p, r *object.Dictionary) string {
		r.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Array{object.Name("ICCBased"),
			object.NewDictionary(object.Entry{Key: "N", Value: fxInt(3)})}}))
		return "/CS0 cs 1 0 0 sc 0 0 10 10 re f"
	}, want: [3][]string{nil, nil, nil}},
	{name: "image-alternates", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) { i.Set("Alternates", object.Array{}) })
	}, want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "image-opi", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) {
			i.Set("OPI", object.NewDictionary(object.Entry{Key: "2.0", Value: object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("OPI")})}))
		})
	}, want: [3][]string{{"6.2.4"}, {"6.2.8"}, {"6.2.7.1"}}},
	{name: "form-opi", build: func(d *Document, p, r *object.Dictionary) string {
		return fxForm(d, r, "", object.Entry{Key: "OPI", Value: object.NewDictionary(object.Entry{Key: "2.0", Value: object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("OPI")})})})
	}, want: [3][]string{{"6.2.4"}, {"6.2.9"}, {"6.2.8.1"}}},
	{name: "reference-xobject", build: func(d *Document, p, r *object.Dictionary) string {
		return fxForm(d, r, "", object.Entry{Key: "Ref", Value: object.NewDictionary(
			object.Entry{Key: "F", Value: object.String{Value: []byte("other.pdf")}}, object.Entry{Key: "Page", Value: fxInt(0)})})
	}, want: [3][]string{{"6.2.6"}, {"6.2.9"}, {"6.2.8.2"}}},
	{name: "extgstate-tr", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "TR", Value: object.Name("Identity")})
	}, want: [3][]string{{"6.2.8"}, {"6.2.5"}, {"6.2.5"}}},
	{name: "extgstate-tr2-default", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "TR2", Value: object.Name("Default")})
	}, want: [3][]string{nil, nil, nil}},
	{name: "extgstate-tr2-identity", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "TR2", Value: object.Name("Identity")})
	}, want: [3][]string{{"6.2.8"}, {"6.2.5"}, {"6.2.5"}}},
	{name: "extgstate-smask", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "SMask", Value: object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("Mask")},
			object.Entry{Key: "S", Value: object.Name("Luminosity")}, object.Entry{Key: "G", Value: d.Add(object.NewStream(object.NewDictionary(
				object.Entry{Key: "Type", Value: object.Name("XObject")}, object.Entry{Key: "Subtype", Value: object.Name("Form")},
				object.Entry{Key: "BBox", Value: object.Array{fxInt(0), fxInt(0), fxInt(10), fxInt(10)}},
				object.Entry{Key: "Group", Value: object.NewDictionary(object.Entry{Key: "S", Value: object.Name("Transparency")}, object.Entry{Key: "CS", Value: object.Name("DeviceRGB")})}), nil))})})
	}, want: [3][]string{{"6.4"}, nil, nil}},
	{name: "extgstate-smask-none", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "SMask", Value: object.Name("None")})
	}, want: [3][]string{nil, nil, nil}},
	{name: "extgstate-ca-half", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "CA", Value: object.Real(0.5)})
	}, want: [3][]string{{"6.4"}, nil, nil}},
	{name: "extgstate-fill-ca-half", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "ca", Value: object.Real(0.5)})
	}, want: [3][]string{{"6.4"}, nil, nil}},
	{name: "extgstate-ca-one", build: func(d *Document, p, r *object.Dictionary) string {
		return fxGS(r, object.Entry{Key: "CA", Value: object.Real(1)}, object.Entry{Key: "ca", Value: fxInt(1)})
	}, want: [3][]string{nil, nil, nil}},
	{name: "image-smask", build: func(d *Document, p, r *object.Dictionary) string {
		return fxImage(d, r, 8, func(i *object.Dictionary) {
			i.Set("SMask", d.Add(object.NewStream(object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("XObject")},
				object.Entry{Key: "Subtype", Value: object.Name("Image")}, object.Entry{Key: "Width", Value: fxInt(1)}, object.Entry{Key: "Height", Value: fxInt(1)},
				object.Entry{Key: "ColorSpace", Value: object.Name("DeviceGray")}, object.Entry{Key: "BitsPerComponent", Value: fxInt(8)}), []byte{0})))
		})
	}, want: [3][]string{{"6.4"}, nil, nil}},
	{name: "cid-font", build: func(d *Document, p, r *object.Dictionary) string {
		return fxCIDFont(d, r, nil)
	}, want: [3][]string{nil, nil, nil},
		except: map[string][]string{"1a": {"6.3.8"}, "2u": {"6.2.11.7.2"}, "2a": {"6.2.11.7.2"}, "3u": {"6.2.11.7.2"}, "3a": {"6.2.11.7.2"}}},
	{name: "cid-font-without-cidtogidmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxCIDFont(d, r, func(t0, cid *object.Dictionary) { cid.Delete("CIDToGIDMap") })
	}, want: [3][]string{{"6.3.3.2"}, {"6.2.11.3.2"}, {"6.2.10.3.2"}},
		except: map[string][]string{"1a": {"6.3.3.2", "6.3.8"}, "2u": {"6.2.11.3.2", "6.2.11.7.2"}, "2a": {"6.2.11.3.2", "6.2.11.7.2"}, "3u": {"6.2.11.3.2", "6.2.11.7.2"}, "3a": {"6.2.11.3.2", "6.2.11.7.2"}}},
	{name: "cid-font-cmap-not-embedded", build: func(d *Document, p, r *object.Dictionary) string {
		return fxCIDFont(d, r, func(t0, cid *object.Dictionary) { t0.Set("Encoding", object.Name("Test-H")) })
	}, want: [3][]string{{"6.3.3.1", "6.3.3.3"}, {"6.2.11.3.1", "6.2.11.3.3"}, {"6.2.10.3.1", "6.2.10.3.3"}}},
	{name: "cid-font-embedded-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxCIDFont(d, r, func(t0, cid *object.Dictionary) { t0.Set("Encoding", fxEmbeddedCMap(d, 0)) })
	}, want: [3][]string{nil, nil, nil},
		except: map[string][]string{"1a": {"6.3.8"}, "2u": {"6.2.11.7.2"}, "2a": {"6.2.11.7.2"}, "3u": {"6.2.11.7.2"}, "3a": {"6.2.11.7.2"}}},
	{name: "cid-font-embedded-cmap-wmode-differs", build: func(d *Document, p, r *object.Dictionary) string {
		return fxCIDFont(d, r, func(t0, cid *object.Dictionary) { t0.Set("Encoding", fxEmbeddedCMap(d, 1)) })
	}, want: [3][]string{{"6.3.3.3"}, {"6.2.11.3.3"}, {"6.2.10.3.3"}},
		except: map[string][]string{"1a": {"6.3.3.3", "6.3.8"}, "2u": {"6.2.11.3.3", "6.2.11.7.2"}, "2a": {"6.2.11.3.3", "6.2.11.7.2"}, "3u": {"6.2.11.3.3", "6.2.11.7.2"}, "3a": {"6.2.11.3.3", "6.2.11.7.2"}}},
	{name: "truetype-not-embedded", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) {
			d.ResolveDict(f.Get("FontDescriptor")).Delete("FontFile2")
		})
	}, want: [3][]string{{"6.3.4"}, {"6.2.11.4.1"}, {"6.2.10.4.1"}}},
	{name: "nonsymbolic-truetype-without-encoding", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram(nil), false, func(f *object.Dictionary) { f.Delete("Encoding") })
	}, want: [3][]string{{"6.3.5", "6.3.7"}, {"6.2.11.4.1", "6.2.11.6"}, {"6.2.10.4.1", "6.2.10.6"}},
		except: map[string][]string{"1a": {"6.3.5", "6.3.7", "6.3.8"}, "2u": {"6.2.11.4.1", "6.2.11.6", "6.2.11.7.2"}, "2a": {"6.2.11.4.1", "6.2.11.6", "6.2.11.7.2"}, "3u": {"6.2.11.4.1", "6.2.11.6", "6.2.11.7.2"}, "3a": {"6.2.11.4.1", "6.2.11.6", "6.2.11.7.2"}}},
	{name: "nonsymbolic-truetype-without-encoding-with-10-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 1, Enc: 0, Data: fxFormat0()}}), false, func(f *object.Dictionary) { f.Delete("Encoding") })
	}, want: [3][]string{{"6.3.7"}, {"6.2.11.6"}, {"6.2.10.6"}},
		except: map[string][]string{"1a": {"6.3.7", "6.3.8"}, "2u": {"6.2.11.6", "6.2.11.7.2"}, "2a": {"6.2.11.6", "6.2.11.7.2"}, "3u": {"6.2.11.6", "6.2.11.7.2"}, "3a": {"6.2.11.6", "6.2.11.7.2"}}},
	{name: "nonsymbolic-truetype-without-encoding-with-30-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 3, Enc: 0, Data: fxFormat0()}}), false, func(f *object.Dictionary) { f.Delete("Encoding") })
	}, want: [3][]string{{"6.3.7"}, {"6.2.11.6"}, {"6.2.10.6"}},
		except: map[string][]string{"1a": {"6.3.7", "6.3.8"}, "2u": {"6.2.11.6", "6.2.11.7.2"}, "2a": {"6.2.11.6", "6.2.11.7.2"}, "3u": {"6.2.11.6", "6.2.11.7.2"}, "3a": {"6.2.11.6", "6.2.11.7.2"}}},
	{name: "symbolic-truetype-with-encoding", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 3, Enc: 0, Data: fxFormat0()}}), true, func(f *object.Dictionary) {
			f.Set("Encoding", object.Name("WinAnsiEncoding"))
		})
	}, want: [3][]string{{"6.3.7"}, {"6.2.11.6"}, {"6.2.10.6"}}},
	{name: "annotation-ca-half", build: func(d *Document, p, r *object.Dictionary) string {
		a := object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Square")}, object.Entry{Key: "CA", Value: object.Real(0.5)},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})})
		fxAnnot(d, p, a)
		return ""
	}, want: [3][]string{{"6.5.3"}, nil, nil}},
	{name: "annotation-without-f", build: func(d *Document, p, r *object.Dictionary) string {
		a := object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Square")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})})
		fxAnnot(d, p, a)
		a.Delete("F")
		return ""
	}, want: [3][]string{{"6.5.3"}, {"6.3.2"}, {"6.3.2"}}},
	{name: "annotation-colour-under-rgb-intent", build: func(d *Document, p, r *object.Dictionary) string {
		a := object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Square")}, object.Entry{Key: "C", Value: object.Array{fxInt(1), fxInt(0), fxInt(0)}},
			object.Entry{Key: "IC", Value: object.Array{fxInt(0), fxInt(0), fxInt(1)}},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})})
		fxAnnot(d, p, a)
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "link-named-action-print", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Link")},
			object.Entry{Key: "A", Value: object.NewDictionary(object.Entry{Key: "S", Value: object.Name("Named")}, object.Entry{Key: "N", Value: object.Name("Print")})}))
		return ""
	}, want: [3][]string{{"6.6.1"}, {"6.5.1"}, {"6.6.1"}}},
	{name: "link-named-action-next-page", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Link")},
			object.Entry{Key: "A", Value: object.NewDictionary(object.Entry{Key: "S", Value: object.Name("Named")}, object.Entry{Key: "N", Value: object.Name("NextPage")})}))
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "widget-with-action", build: func(d *Document, p, r *object.Dictionary) string {
		w := object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Widget")},
			object.Entry{Key: "FT", Value: object.Name("Btn")}, object.Entry{Key: "T", Value: object.String{Value: []byte("b")}}, object.Entry{Key: "Ff", Value: fxInt(65536)},
			object.Entry{Key: "A", Value: object.NewDictionary(object.Entry{Key: "S", Value: object.Name("Named")}, object.Entry{Key: "N", Value: object.Name("NextPage")})},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})})
		fxAnnot(d, p, w)
		fxCatalog(d).Set("AcroForm", object.NewDictionary(object.Entry{Key: "Fields", Value: p.Get("Annots")}))
		return ""
	}, want: [3][]string{{"6.5.3", "6.6.1"}, {"6.3.3", "6.4.1"}, {"6.3.3", "6.4.1"}}},
	{name: "catalog-aa", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("AA", object.NewDictionary(object.Entry{Key: "WC", Value: object.NewDictionary(
			object.Entry{Key: "S", Value: object.Name("Named")}, object.Entry{Key: "N", Value: object.Name("FirstPage")})}))
		return ""
	}, want: [3][]string{{"6.6.2"}, {"6.5.2"}, {"6.6.3"}}},
	{name: "metadata-without-subtype", build: func(d *Document, p, r *object.Dictionary) string {
		d.Resolve(fxCatalog(d).Get("Metadata")).(*object.Stream).Dict.Delete("Subtype")
		return ""
	}, want: [3][]string{{"6.7.2"}, {"6.6.2.1"}, {"6.7.2.1"}}},
	{name: "metadata-flate", build: func(d *Document, p, r *object.Dictionary) string {
		md := d.Resolve(fxCatalog(d).Get("Metadata")).(*object.Stream)
		data, err := d.StreamData(md)
		if err != nil {
			panic(err)
		}
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(data)
		w.Close()
		dict := md.Dict.Clone()
		dict.Set("Filter", object.Name("FlateDecode"))
		fxCatalog(d).Set("Metadata", d.Add(object.NewStream(dict, z.Bytes())))
		return ""
	}, want: [3][]string{{"6.7.2"}, nil, nil}},
	{name: "xpacket-bytes-attribute", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte(`id="W5M0MpCehiHzreSzNTczkc9d"`), []byte(`id="W5M0MpCehiHzreSzNTczkc9d" bytes="1000"`), 1)
		})
		return ""
	}, want: [3][]string{{"6.7.5"}, {"6.6.2.1"}, {"6.7.2.1"}}},
	{name: "xpacket-encoding-attribute", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte(`id="W5M0MpCehiHzreSzNTczkc9d"`), []byte(`id="W5M0MpCehiHzreSzNTczkc9d" encoding="UTF-8"`), 1)
		})
		return ""
	}, want: [3][]string{{"6.7.5"}, {"6.6.2.1"}, {"6.7.2.1"}}},
	{name: "extension-schema", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, nil)
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "extension-property-without-schema", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte("</rdf:RDF>"), []byte(`<rdf:Description rdf:about="" xmlns:ex="http://example.com/ns/"><ex:foo>bar</ex:foo></rdf:Description></rdf:RDF>`), 1)
		})
		return ""
	}, want: [3][]string{{"6.7.9"}, {"6.6.2.3.1"}, nil}},
	{name: "extension-schema-without-prefix", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string {
			return strings.Replace(s, "<pdfaSchema:prefix>ex</pdfaSchema:prefix>", "", 1)
		})
		return ""
	}, want: [3][]string{{"6.7.8"}, {"6.6.2.3.3"}, nil}},
	{name: "extension-schema-property-without-category", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string {
			return strings.Replace(s, "<pdfaProperty:category>external</pdfaProperty:category>", "", 1)
		})
		return ""
	}, want: [3][]string{{"6.7.8"}, {"6.6.2.3.3"}, nil}},
	{name: "extension-schema-property-without-description", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string {
			return strings.Replace(s, "<pdfaProperty:description>a property</pdfaProperty:description>", "", 1)
		})
		return ""
	}, want: [3][]string{{"6.7.8"}, {"6.6.2.3.3"}, nil}},
	{name: "extension-schema-property-without-value-type", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string {
			return strings.Replace(s, "<pdfaProperty:valueType>Text</pdfaProperty:valueType>", "", 1)
		})
		return ""
	}, want: [3][]string{{"6.7.8", "6.7.9"}, {"6.6.2.3.1", "6.6.2.3.3"}, nil}},
	{name: "extension-schema-container-other-prefix", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string { return strings.ReplaceAll(s, "pdfaExtension", "ext") })
		return ""
	}, want: [3][]string{{"6.7.8"}, {"6.6.2.3.3"}, nil}},
	{name: "extension-schema-unknown-value-type", build: func(d *Document, p, r *object.Dictionary) string {
		fxExtensionSchema(d, func(s string) string {
			return strings.Replace(s, "<pdfaProperty:valueType>Text<", "<pdfaProperty:valueType>Widget<", 1)
		})
		return ""
	}, want: [3][]string{{"6.7.8", "6.7.9"}, {"6.6.2.3.1", "6.6.2.3.3"}, nil}},
	{name: "acroform-need-appearances", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("AcroForm", object.NewDictionary(object.Entry{Key: "Fields", Value: object.Array{}}, object.Entry{Key: "NeedAppearances", Value: object.Boolean(true)}))
		return ""
	}, want: [3][]string{{"6.9"}, {"6.4.1"}, {"6.4.1"}}},
	{name: "acroform-need-appearances-false", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("AcroForm", object.NewDictionary(object.Entry{Key: "Fields", Value: object.Array{}}, object.Entry{Key: "NeedAppearances", Value: object.Boolean(false)}))
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "data-after-eof", build: func(d *Document, p, r *object.Dictionary) string { return "" },
		patch: func(b []byte) []byte { return append(b, "trailing junk\n"...) }, want: [3][]string{{"6.1.3"}, {"6.1.3"}, {"6.1.3"}}},
	{name: "lzw-filter", build: func(d *Document, p, r *object.Dictionary) string {
		// Clear code then end of data, 9 bits each: an empty stream.
		return fxStreamForm(d, r, []byte{0x80, 0x40, 0x40}, object.Entry{Key: "Filter", Value: object.Name("LZWDecode")})
	}, want: [3][]string{{"6.1.10"}, {"6.1.7.2"}, {"6.1.6.2"}}},
	{name: "crypt-filter-identity", build: func(d *Document, p, r *object.Dictionary) string {
		return fxStreamForm(d, r, nil, object.Entry{Key: "Filter", Value: object.Array{object.Name("Crypt")}},
			object.Entry{Key: "DecodeParms", Value: object.Array{object.NewDictionary(object.Entry{Key: "Name", Value: object.Name("Identity")})}})
	}, want: [3][]string{nil, nil, nil}},
	{name: "crypt-filter-without-decodeparms", build: func(d *Document, p, r *object.Dictionary) string {
		return fxStreamForm(d, r, nil, object.Entry{Key: "Filter", Value: object.Name("Crypt")})
	}, want: [3][]string{nil, nil, nil}},
	{name: "crypt-filter-named", build: func(d *Document, p, r *object.Dictionary) string {
		return fxStreamForm(d, r, nil, object.Entry{Key: "Filter", Value: object.Array{object.Name("Crypt")}},
			object.Entry{Key: "DecodeParms", Value: object.Array{object.NewDictionary(object.Entry{Key: "Name", Value: object.Name("StdCF")})}})
	}, want: [3][]string{nil, {"6.1.7.2"}, {"6.1.6.2"}}},
	{name: "real-too-large-in-content", build: func(d *Document, p, r *object.Dictionary) string {
		return "340400000000000000000000000000000000000.0 0 m 0 0 l S"
	}, want: [3][]string{{"6.1.12"}, {"6.1.13"}, nil}},
	{name: "real-too-small-in-content", build: func(d *Document, p, r *object.Dictionary) string {
		return "0.0000000000000000000000000000000000000001 0 m 0 0 l S"
	}, want: [3][]string{nil, {"6.1.13"}, nil}},
	{name: "real-too-large-in-object", build: func(d *Document, p, r *object.Dictionary) string {
		p.Set("UserUnit", object.Real(3.404e38))
		return ""
	}, want: [3][]string{{"6.1.12"}, {"6.1.13"}, nil}},
	{name: "q-nesting-29", build: func(d *Document, p, r *object.Dictionary) string {
		return strings.Repeat("q ", 29) + strings.Repeat("Q ", 29)
	}, want: [3][]string{{"6.1.12"}, {"6.1.13"}, nil}},
	{name: "q-nesting-28", build: func(d *Document, p, r *object.Dictionary) string {
		return strings.Repeat("q ", 28) + strings.Repeat("Q ", 28)
	}, want: [3][]string{nil, nil, nil}},
	{name: "devicen-33-colourants", build: func(d *Document, p, r *object.Dictionary) string { return fxDeviceN(d, r, 33) },
		want: [3][]string{{"6.1.12"}, {"6.1.13", "6.2.4.4"}, {"6.2.4.4"}}},
	{name: "devicen-32-colourants", build: func(d *Document, p, r *object.Dictionary) string { return fxDeviceN(d, r, 32) },
		want: [3][]string{{"6.1.12"}, {"6.2.4.4"}, {"6.2.4.4"}}},
	{name: "pdfx-output-intent-with-dest-output-profile-ref", build: func(d *Document, p, r *object.Dictionary) string {
		cat := fxCatalog(d)
		ois := d.Resolve(cat.Get("OutputIntents")).(object.Array)
		x := d.ResolveDict(ois[0]).Clone()
		x.Set("S", object.Name("GTS_PDFX"))
		x.Set("DestOutputProfileRef", object.NewDictionary(object.Entry{Key: "URLs", Value: object.Array{}}))
		cat.Set("OutputIntents", append(slices.Clone(ois), d.Add(x)))
		return ""
	}, want: [3][]string{nil, {"6.2.3"}, {"6.2.3"}}},
	{name: "annotation-hidden", build: func(d *Document, p, r *object.Dictionary) string {
		a := object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Square")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: fxAppearance(d)})})
		fxAnnot(d, p, a)
		a.Set("F", fxInt(4|2))
		return ""
	}, want: [3][]string{{"6.5.3"}, {"6.3.2"}, {"6.3.2"}}},
	{name: "button-widget-appearance-states", build: func(d *Document, p, r *object.Dictionary) string {
		fxAnnot(d, p, object.NewDictionary(object.Entry{Key: "Subtype", Value: object.Name("Widget")},
			object.Entry{Key: "FT", Value: object.Name("Btn")}, object.Entry{Key: "T", Value: object.String{Value: []byte("b")}},
			object.Entry{Key: "AS", Value: object.Name("Off")},
			object.Entry{Key: "AP", Value: object.NewDictionary(object.Entry{Key: "N", Value: object.NewDictionary(
				object.Entry{Key: "On", Value: fxAppearance(d)}, object.Entry{Key: "Off", Value: fxAppearance(d)})})}))
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "xmp-utf16", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			// UTF-16BE, the packet's begin attribute its byte order mark.
			var out []byte
			for _, c := range string(x) {
				out = append(out, byte(c>>8), byte(c))
			}
			return out
		})
		return ""
	}, want: [3][]string{nil, {"6.6.2.1"}, {"6.7.2.1"}}},
	{name: "xmp-pdfaid-amd-other-prefix", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte("<pdfaid:conformance>"),
				[]byte("<id:amd xmlns:id=\"http://www.aiim.org/pdfa/ns/id/\">2005</id:amd>\n      <pdfaid:conformance>"), 1)
		})
		return ""
	}, want: [3][]string{{"6.7.11"}, {"6.6.4"}, nil}},
	{name: "xmp-pdfaid-corr-other-prefix", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte("<pdfaid:conformance>"),
				[]byte("<id:corr xmlns:id=\"http://www.aiim.org/pdfa/ns/id/\">2011</id:corr>\n      <pdfaid:conformance>"), 1)
		})
		return ""
	}, want: [3][]string{{"6.7.9"}, {"6.6.4"}, nil}},
	{name: "xmp-pdfaid-amd", build: func(d *Document, p, r *object.Dictionary) string {
		fxXMP(d, func(x []byte) []byte {
			return bytes.Replace(x, []byte("<pdfaid:conformance>"), []byte("<pdfaid:amd>2005</pdfaid:amd>\n      <pdfaid:conformance>"), 1)
		})
		return ""
	}, want: [3][]string{nil, nil, nil}},
	{name: "embedded-pdfa", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, fxPDFA(pdfa.PDFA2b), "application/pdf", nil)
		return ""
	}, want: [3][]string{{"6.1.11"}, nil, nil}},
	{name: "embedded-pdfa-without-uf", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, fxPDFA(pdfa.PDFA2b), "application/pdf", func(fs *object.Dictionary) { fs.Delete("UF") })
		return ""
	}, want: [3][]string{{"6.1.11"}, {"6.8"}, {"6.9"}}},
	{name: "embedded-pdfa4", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, fxPDFA(pdfa.PDFA4), "application/pdf", nil)
		return ""
	}, want: [3][]string{{"6.1.11"}, {"6.8"}, nil},
		except: map[string][]string{"3b": nil, "3u": nil, "3a": nil}},
	{name: "embedded-pdfa1b", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, fxPDFA(pdfa.PDFA1b), "application/pdf", nil)
		return ""
	}, want: [3][]string{{"6.1.11"}, nil, nil}},
	{name: "embedded-pdfa-labelled-text", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, fxPDFA(pdfa.PDFA2b), "text/plain", nil)
		return ""
	}, want: [3][]string{{"6.1.11"}, nil, nil}},
	{name: "embedded-text-file", build: func(d *Document, p, r *object.Dictionary) string {
		fxEmbed(d, []byte("hello\n"), "text/plain", nil)
		return ""
	}, want: [3][]string{{"6.1.11"}, {"6.8"}, {"6.9"}},
		except: map[string][]string{"3b": nil, "3u": nil, "3a": nil, "4e": nil, "4f": nil}},
	{name: "optional-content", build: func(d *Document, p, r *object.Dictionary) string { fxOC(d, nil); return "" },
		want: [3][]string{{"6.1.13"}, nil, nil}},
	{name: "optional-content-config-without-name", build: func(d *Document, p, r *object.Dictionary) string {
		fxOC(d, func(ocp, c *object.Dictionary, g object.Array) { c.Delete("Name") })
		return ""
	}, want: [3][]string{{"6.1.13"}, {"6.9"}, {"6.10"}}},
	{name: "optional-content-config-names-repeat", build: func(d *Document, p, r *object.Dictionary) string {
		fxOC(d, func(ocp, c *object.Dictionary, g object.Array) { ocp.Set("Configs", object.Array{c.Clone()}) })
		return ""
	}, want: [3][]string{{"6.1.13"}, {"6.9"}, {"6.10"}}},
	{name: "optional-content-order-misses-a-group", build: func(d *Document, p, r *object.Dictionary) string {
		fxOC(d, func(ocp, c *object.Dictionary, g object.Array) { c.Set("Order", object.Array{g[0]}) })
		return ""
	}, want: [3][]string{{"6.1.13"}, {"6.9"}, {"6.10"}}},
	{name: "optional-content-config-as", build: func(d *Document, p, r *object.Dictionary) string {
		fxOC(d, func(ocp, c *object.Dictionary, g object.Array) {
			c.Set("AS", object.Array{object.NewDictionary(object.Entry{Key: "Event", Value: object.Name("View")},
				object.Entry{Key: "OCGs", Value: g}, object.Entry{Key: "Category", Value: object.Array{object.Name("View")}})})
		})
		return ""
	}, want: [3][]string{{"6.1.13"}, {"6.9"}, nil}},
	{name: "jpx-gray", build: func(d *Document, p, r *object.Dictionary) string { return fxJPX(d, r, []uint32{8}, nil, nil) },
		want: [3][]string{nil, nil, nil}, beyond: jpxAtPDFA1},
	{name: "jpx-rgb", build: func(d *Document, p, r *object.Dictionary) string { return fxJPX(d, r, []uint32{8, 8, 8}, nil, nil) },
		want: [3][]string{nil, nil, nil}, beyond: jpxAtPDFA1},
	{name: "jpx-two-channels", build: func(d *Document, p, r *object.Dictionary) string { return fxJPX(d, r, []uint32{8, 8}, nil, nil) },
		want: [3][]string{nil, {"6.2.8.3"}, {"6.2.7.3"}}, beyond: jpxAtPDFA1},
	{name: "jpx-channels-of-different-depths", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8, 8, 4}, nil, nil)
	}, want: [3][]string{nil, {"6.2.8.3"}, {"6.2.7.3"}}, beyond: jpxAtPDFA1},
	{name: "jpx-colr-meth-4", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, func(b []byte) []byte {
			colr, _ := fxColr(b)
			b = slices.Clone(b)
			b[colr] = 4
			return b
		}, nil)
	}, want: [3][]string{nil, {"6.2.8.3"}, {"6.2.7.3"}}, beyond: jpxAtPDFA1},
	{name: "jpx-colr-meth-4-with-colorspace", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, func(b []byte) []byte {
			colr, _ := fxColr(b)
			b = slices.Clone(b)
			b[colr] = 4
			return b
		}, func(i *object.Dictionary) { i.Set("ColorSpace", object.Name("DeviceGray")) })
	}, want: [3][]string{nil, nil, nil}, beyond: jpxAtPDFA1},
	{name: "jpx-enumcs-19", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, func(b []byte) []byte {
			colr, _ := fxColr(b)
			b = slices.Clone(b)
			binary.BigEndian.PutUint32(b[colr+3:], 19)
			return b
		}, nil)
	}, want: [3][]string{nil, {"6.2.8.3"}, {"6.2.7.3"}}, beyond: jpxAtPDFA1},
	{name: "jpx-enumcs-20", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, func(b []byte) []byte {
			colr, _ := fxColr(b)
			b = slices.Clone(b)
			binary.BigEndian.PutUint32(b[colr+3:], 20)
			return b
		}, nil)
	}, want: [3][]string{nil, nil, nil}, beyond: jpxAtPDFA1},
	{name: "jpx-two-colr-none-approx", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, fxSecondColr(0, 0), nil)
	}, want: [3][]string{nil, {"6.2.8.3"}, {"6.2.7.3"}}, beyond: jpxAtPDFA1},
	{name: "jpx-two-colr-one-approx", build: func(d *Document, p, r *object.Dictionary) string {
		return fxJPX(d, r, []uint32{8}, fxSecondColr(1, 0), nil)
	}, want: [3][]string{nil, nil, nil}, beyond: jpxAtPDFA1},
	{name: "signed", build: func(d *Document, p, r *object.Dictionary) string { return "" }, signed: true,
		want: [3][]string{{"6.9"}, nil, nil}},
	{name: "signed-byterange-short", build: func(d *Document, p, r *object.Dictionary) string { return "" }, signed: true,
		patch: fxShortByteRange, want: [3][]string{{"6.9"}, {"6.4.3"}, nil}},
	{name: "signed-cms-without-certificates", build: func(d *Document, p, r *object.Dictionary) string { return "" }, signed: true,
		patch: fxSigContents(func(e [][]byte) [][]byte {
			return slices.DeleteFunc(e, func(x []byte) bool { return x[0] == 0xA0 })
		}), want: [3][]string{{"6.9"}, {"6.4.3"}, nil}},
	{name: "signed-cms-two-signer-infos", build: func(d *Document, p, r *object.Dictionary) string { return "" }, signed: true,
		patch: fxSigContents(func(e [][]byte) [][]byte {
			last := e[len(e)-1]
			infos := derElems(derBody(last))
			e[len(e)-1] = derTLV(0x31, slices.Concat(infos[0], infos[0]))
			return e
		}), want: [3][]string{{"6.9"}, {"6.4.3"}, nil}},
	{name: "alternate-presentations", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("Names", object.NewDictionary(object.Entry{Key: "AlternatePresentations", Value: object.NewDictionary(
			object.Entry{Key: "Names", Value: object.Array{}})}))
		return ""
	}, want: [3][]string{nil, {"6.10"}, {"6.11"}},
		except: map[string][]string{"4f": {"6.11", "6.9"}}},
	{name: "page-pressteps", build: func(d *Document, p, r *object.Dictionary) string {
		p.Set("PresSteps", d.Add(object.NewDictionary(object.Entry{Key: "Type", Value: object.Name("NavNode")})))
		return ""
	}, want: [3][]string{nil, {"6.10"}, {"6.11"}}},
	{name: "catalog-requirements", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("Requirements", object.Array{object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Requirement")}, object.Entry{Key: "S", Value: object.Name("EnableJavaScripts")})})
		return ""
	}, want: [3][]string{nil, {"6.11"}, {"6.12"}}},
}

// fxGS applies an ExtGState with the given entries.
func fxGS(res *object.Dictionary, entries ...object.Entry) string {
	res.Set("ExtGState", object.NewDictionary(object.Entry{Key: "GS0", Value: object.NewDictionary(
		append([]object.Entry{{Key: "Type", Value: object.Name("ExtGState")}}, entries...)...)}))
	return "/GS0 gs 0 0 m 1 1 l S"
}

// fxCIDFont shows glyph 1 through an Identity-H Type 0 font over a
// CIDFontType2 on fxTrueTypeProgram; tweak changes the two dictionaries first.
func fxCIDFont(d *Document, res *object.Dictionary, tweak func(type0, cid *object.Dictionary)) string {
	program := fxTrueTypeProgram(nil)
	fd := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("FontDescriptor")},
		object.Entry{Key: "FontName", Value: object.Name("Test")},
		object.Entry{Key: "Flags", Value: fxInt(4)},
		object.Entry{Key: "FontBBox", Value: object.Array{fxInt(0), fxInt(-200), fxInt(1000), fxInt(800)}},
		object.Entry{Key: "ItalicAngle", Value: fxInt(0)},
		object.Entry{Key: "Ascent", Value: fxInt(800)},
		object.Entry{Key: "Descent", Value: fxInt(-200)},
		object.Entry{Key: "CapHeight", Value: fxInt(700)},
		object.Entry{Key: "StemV", Value: fxInt(80)},
		object.Entry{Key: "FontFile2", Value: d.Add(object.NewStream(object.NewDictionary(
			object.Entry{Key: "Length1", Value: fxInt(len(program))}), program))},
	)
	cid := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("CIDFontType2")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "CIDSystemInfo", Value: object.NewDictionary(
			object.Entry{Key: "Registry", Value: object.String{Value: []byte("Adobe")}},
			object.Entry{Key: "Ordering", Value: object.String{Value: []byte("Identity")}},
			object.Entry{Key: "Supplement", Value: fxInt(0)})},
		object.Entry{Key: "FontDescriptor", Value: d.Add(fd)},
		object.Entry{Key: "CIDToGIDMap", Value: object.Name("Identity")},
		object.Entry{Key: "DW", Value: fxInt(1000)},
		object.Entry{Key: "W", Value: object.Array{fxInt(1), object.Array{fxInt(500)}}},
	)
	type0 := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type0")},
		object.Entry{Key: "BaseFont", Value: object.Name("Test")},
		object.Entry{Key: "Encoding", Value: object.Name("Identity-H")},
	)
	if tweak != nil {
		tweak(type0, cid)
	}
	type0.Set("DescendantFonts", object.Array{d.Add(cid)})
	res.Set("Font", object.NewDictionary(object.Entry{Key: "F0", Value: d.Add(type0)}))
	return "BT /F0 12 Tf 10 10 Td <0001> Tj ET"
}

// fxEmbeddedCMap is an embedded CMap mapping two-byte codes to the same CIDs,
// its stream dictionary's /WMode wmode and its content's 0.
func fxEmbeddedCMap(d *Document, wmode int) object.IndirectRef {
	body := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> def\n" +
		"/CMapName /Test-H def /CMapType 1 def /WMode 0 def\n" +
		"1 begincodespacerange <0000> <FFFF> endcodespacerange\n" +
		"1 begincidrange <0000> <FFFF> 0 endcidrange\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end\n"
	return d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("CMap")},
		object.Entry{Key: "CMapName", Value: object.Name("Test-H")},
		object.Entry{Key: "CIDSystemInfo", Value: object.NewDictionary(
			object.Entry{Key: "Registry", Value: object.String{Value: []byte("Adobe")}},
			object.Entry{Key: "Ordering", Value: object.String{Value: []byte("Identity")}},
			object.Entry{Key: "Supplement", Value: fxInt(0)})},
		object.Entry{Key: "WMode", Value: fxInt(wmode)},
	), []byte(body)))
}

// fxExtensionSchema adds to the metadata a property ex:foo, in a namespace
// of its own, and the extension schema that describes it; edit changes the
// schema description first.
func fxExtensionSchema(d *Document, edit func(schema string) string) {
	schema := `<rdf:Description rdf:about="" xmlns:pdfaExtension="http://www.aiim.org/pdfa/ns/extension/" xmlns:pdfaSchema="http://www.aiim.org/pdfa/ns/schema#" xmlns:pdfaProperty="http://www.aiim.org/pdfa/ns/property#">
      <pdfaExtension:schemas><rdf:Bag><rdf:li rdf:parseType="Resource">
        <pdfaSchema:schema>Example</pdfaSchema:schema>
        <pdfaSchema:namespaceURI>http://example.com/ns/</pdfaSchema:namespaceURI>
        <pdfaSchema:prefix>ex</pdfaSchema:prefix>
        <pdfaSchema:property><rdf:Seq><rdf:li rdf:parseType="Resource">
          <pdfaProperty:name>foo</pdfaProperty:name>
          <pdfaProperty:valueType>Text</pdfaProperty:valueType>
          <pdfaProperty:category>external</pdfaProperty:category>
          <pdfaProperty:description>a property</pdfaProperty:description>
        </rdf:li></rdf:Seq></pdfaSchema:property>
      </rdf:li></rdf:Bag></pdfaExtension:schemas>
    </rdf:Description>
    <rdf:Description rdf:about="" xmlns:ex="http://example.com/ns/"><ex:foo>bar</ex:foo></rdf:Description>
  </rdf:RDF>`
	if edit != nil {
		schema = edit(schema)
	}
	fxXMP(d, func(x []byte) []byte { return bytes.Replace(x, []byte("</rdf:RDF>"), []byte(schema), 1) })
}

// fxSameLength replaces old with new in a written file, which must be as
// long, so that no offset the cross-reference table records moves.
func fxSameLength(old, new string) func([]byte) []byte {
	if len(old) != len(new) {
		panic("fxSameLength: " + old + " and " + new + " differ in length")
	}
	return func(b []byte) []byte {
		if !bytes.Contains(b, []byte(old)) {
			panic("fxSameLength: no " + old)
		}
		return bytes.Replace(b, []byte(old), []byte(new), 1)
	}
}

// fxJPX draws a 4×4 JPEG 2000 image, JP2-wrapped, of one component per
// precision in prec, its colour space the JP2 data's own (no /ColorSpace);
// edit changes the JP2 bytes first, and tweak the image dictionary.
func fxJPX(d *Document, res *object.Dictionary, prec []uint32, edit func([]byte) []byte, tweak func(*object.Dictionary)) string {
	cs := gopenjpeg.ColorSpaceGray
	if len(prec) == 3 {
		cs = gopenjpeg.ColorSpaceSRGB
	}
	comps := make([]gopenjpeg.Component, len(prec))
	for i, p := range prec {
		comps[i] = gopenjpeg.Component{Dx: 1, Dy: 1, W: 4, H: 4, Prec: p, Data: make([]int32, 16)}
	}
	var jp2 bytes.Buffer
	if err := gopenjpeg.Encode(gopenjpeg.NewImage(cs, 0, 0, 4, 4, comps), &jp2, gopenjpeg.WithEncodeFormat(gopenjpeg.FormatJP2), gopenjpeg.WithResolutions(1)); err != nil {
		panic(err)
	}
	data := jp2.Bytes()
	if edit != nil {
		data = edit(data)
	}
	dict := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("XObject")},
		object.Entry{Key: "Subtype", Value: object.Name("Image")},
		object.Entry{Key: "Width", Value: fxInt(4)},
		object.Entry{Key: "Height", Value: fxInt(4)},
		object.Entry{Key: "Filter", Value: object.Name("JPXDecode")},
	)
	if tweak != nil {
		tweak(dict)
	}
	res.Set("XObject", object.NewDictionary(object.Entry{Key: "Im0", Value: d.Add(object.NewStream(dict, data))}))
	return "q 10 0 0 10 0 0 cm /Im0 Do Q"
}

// jpxAtPDFA1 is what pdf0 reports of any JPEG 2000 image at PDF/A-1 beyond
// veraPDF: JPXDecode is a PDF 1.5 filter, and PDF/A-1, on PDF 1.4, has no
// such filter to use (6.1.10; audit 2026-07-26 C17). veraPDF's PDF/A-1
// profiles forbid LZWDecode alone.
var jpxAtPDFA1 = map[string][]string{"1b": {"6.1.10"}, "1a": {"6.1.10"}}

// fxColr is the offset of the payload of a JP2 file's first colour
// specification box (METH, PREC, APPROX, then EnumCS for METH 1), and of
// the jp2h header box that holds it.
func fxColr(b []byte) (colr, jp2h int) {
	jp2h = bytes.Index(b, []byte("jp2h")) - 4
	colr = bytes.Index(b, []byte("colr")) + 4
	if jp2h < 0 || colr < 4 {
		panic("fxColr: no jp2h or colr box")
	}
	return colr, jp2h
}

// fxSecondColr gives a JP2 file a second colour specification box, a copy
// of its first with APPROX approx, after it, and the first APPROX first.
func fxSecondColr(first, approx byte) func([]byte) []byte {
	return func(b []byte) []byte {
		colr, jp2h := fxColr(b)
		box := slices.Clone(b[colr-8 : colr-8+int(binary.BigEndian.Uint32(b[colr-8:]))])
		b = slices.Clone(b)
		b[colr+2] = first
		box[8+2] = approx
		end := colr - 8 + len(box)
		b = slices.Concat(b[:end], box, b[end:])
		binary.BigEndian.PutUint32(b[jp2h:], binary.BigEndian.Uint32(b[jp2h:])+uint32(len(box)))
		return b
	}
}

// fxSigContents rewrites, in place, the CMS a signed file's /Contents
// holds: edit gets the SignedData's elements (version, digest algorithms,
// encapsulated content, [0] certificates, ..., signer infos) and returns
// the ones to write. The new DER must fit the placeholder, which is padded
// with zeros as written.
func fxSigContents(edit func(elems [][]byte) [][]byte) func([]byte) []byte {
	return func(b []byte) []byte {
		m := regexp.MustCompile(`/Contents\s*<([0-9A-Fa-f]+)>`).FindSubmatchIndex(b)
		if m == nil {
			panic("fxSigContents: no /Contents")
		}
		der, err := hex.DecodeString(string(b[m[2]:m[3]]))
		if err != nil {
			panic(err)
		}
		ci := derElems(derBody(der))            // ContentInfo: OID, [0]
		sd := derElems(derBody(derBody(ci[1]))) // [0] EXPLICIT SignedData
		sdNew := derTLV(0x30, slices.Concat(edit(sd)...))
		ciNew := derTLV(0x30, slices.Concat(ci[0], derTLV(0xA0, sdNew)))
		out := []byte(strings.ToUpper(hex.EncodeToString(ciNew)))
		if len(out) > m[3]-m[2] {
			panic("fxSigContents: the new CMS does not fit")
		}
		b = slices.Clone(b)
		copy(b[m[2]:m[3]], bytes.Repeat([]byte("0"), m[3]-m[2]))
		copy(b[m[2]:], out)
		return b
	}
}

// fxShortByteRange makes a signed file's /ByteRange stop one byte short of
// the end of the file, its length unchanged.
func fxShortByteRange(b []byte) []byte {
	m := regexp.MustCompile(`/ByteRange\s*\[\s*(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*\]`).FindSubmatchIndex(b)
	if m == nil {
		panic("fxShortByteRange: no /ByteRange")
	}
	n, _ := strconv.Atoi(string(b[m[8]:m[9]]))
	short := strconv.Itoa(n - 1)
	short += strings.Repeat(" ", m[9]-m[8]-len(short))
	b = slices.Clone(b)
	copy(b[m[8]:m[9]], short)
	return b
}

// derTLV, derBody and derElems read and write the DER the CMS is made of:
// a tag, a definite length, the contents.
func derTLV(tag byte, body []byte) []byte {
	n := len(body)
	switch {
	case n < 0x80:
		return slices.Concat([]byte{tag, byte(n)}, body)
	case n < 0x100:
		return slices.Concat([]byte{tag, 0x81, byte(n)}, body)
	case n < 0x10000:
		return slices.Concat([]byte{tag, 0x82, byte(n >> 8), byte(n)}, body)
	}
	return slices.Concat([]byte{tag, 0x83, byte(n >> 16), byte(n >> 8), byte(n)}, body)
}

func derHeader(b []byte) (hdr, n int) {
	if b[1] < 0x80 {
		return 2, int(b[1])
	}
	k := int(b[1] & 0x7F)
	for i := 0; i < k; i++ {
		n = n<<8 | int(b[2+i])
	}
	return 2 + k, n
}

func derBody(b []byte) []byte {
	h, n := derHeader(b)
	return b[h : h+n]
}

func derElems(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		h, n := derHeader(b)
		out = append(out, b[:h+n])
		b = b[h+n:]
	}
	return out
}

// fxDeviceN fills with a DeviceN colour space of n colourants over
// DeviceGray, whose tint transform drops all but the first.
func fxDeviceN(d *Document, res *object.Dictionary, n int) string {
	var names, domain object.Array
	for i := range n {
		names = append(names, object.Name(fmt.Sprintf("C%d", i)))
		domain = append(domain, fxInt(0), fxInt(1))
	}
	fn := d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "FunctionType", Value: fxInt(4)},
		object.Entry{Key: "Domain", Value: domain},
		object.Entry{Key: "Range", Value: object.Array{fxInt(0), fxInt(1)}},
	), []byte("{"+strings.Repeat(" pop", n-1)+" }")))
	res.Set("ColorSpace", object.NewDictionary(object.Entry{Key: "CS0", Value: object.Array{
		object.Name("DeviceN"), names, object.Name("DeviceGray"), fn}}))
	return "/CS0 cs" + strings.Repeat(" 0", n) + " scn 0 0 10 10 re f"
}

// buildDefect writes one fixture at one level: the level's skeleton with one
// page, as the fixture draws and dresses it.
func buildDefect(t *testing.T, f defectFixture, level pdfa.Level) []byte {
	t.Helper()
	d, err := NewPDFADocument(level)
	if err != nil {
		t.Fatal(err)
	}
	res, page := &object.Dictionary{}, &object.Dictionary{}
	drawn := f.build(d, page, res)
	pagesRef := d.ResolveDict(d.Trailer.Get("Root")).Get("Pages")
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", pagesRef)
	page.Set("MediaBox", object.Array{fxInt(0), fxInt(0), fxInt(200), fxInt(200)})
	page.Set("Resources", res)
	// The page's content is marked as an artifact, so that at Level A it is
	// not a second defect: content outside every marked-content sequence
	// fails ISO 19005-1 6.8.3.2 and -2/-3 6.7.3.2 (checkLevelAArtifacts).
	if drawn != "" {
		drawn = "/Artifact BMC\n" + drawn + "\nEMC"
	}
	page.Set("Contents", d.Add(object.NewStream(&object.Dictionary{}, []byte(drawn))))
	pages := d.ResolveDict(pagesRef)
	pages.Set("Kids", object.Array{d.Add(page)})
	pages.Set("Count", fxInt(1))
	var buf bytes.Buffer
	write := func() error { return d.Write(&buf) }
	if f.signed {
		cert, key := signtest.CertKey(t)
		write = func() error { return d.WriteSigned(&buf, cert, key) }
	}
	if err := write(); err != nil {
		t.Fatal(err)
	}
	if f.patch != nil {
		return f.patch(buf.Bytes())
	}
	return buf.Bytes()
}

// pdf0Clauses is the distinct clauses pdf0 fails a file on at a level,
// checker findings aside.
func pdf0Clauses(t *testing.T, data []byte, level pdfa.Level) []string {
	t.Helper()
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range ValidatePDFA(doc, level) {
		if !IsCheckerFinding(v) && !slices.Contains(out, v.Rule) {
			out = append(out, v.Rule)
		}
	}
	slices.Sort(out)
	return out
}

// TestSingleDefectFilesAgreeWithVeraPDF: pdf0 fails each fixture on exactly
// the clauses veraPDF does, at each part; and when PDF0_VERAPDF names
// veraPDF, veraPDF still says what the fixtures record.
func TestSingleDefectFilesAgreeWithVeraPDF(t *testing.T) {
	vera := os.Getenv("PDF0_VERAPDF")
	for _, lv := range defectLevels {
		dir := filepath.Join(t.TempDir(), lv.flavour)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range defectFixtures {
			data := buildDefect(t, f, lv.level)
			want := f.pdf0WantAt(lv.flavour, lv.column)
			if got := pdf0Clauses(t, data, lv.level); !slices.Equal(got, want) {
				t.Errorf("%s at %s: pdf0 fails it on %v; the fixture records %v", f.name, lv.flavour, got, want)
			}
			if err := os.WriteFile(filepath.Join(dir, f.name+".pdf"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if vera == "" {
			continue
		}
		verdicts := veraPDFClauses(t, vera, lv.flavour, dir)
		for _, f := range defectFixtures {
			want := f.wantAt(lv.flavour, lv.column)
			got, ok := verdicts[f.name+".pdf"]
			if !ok {
				t.Errorf("%s at %s: veraPDF gave no verdict", f.name, lv.flavour)
				continue
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s at %s: veraPDF fails it on %v, and the fixture records %v", f.name, lv.flavour, got, want)
			}
		}
	}
	if vera == "" {
		t.Log("PDF0_VERAPDF is not set: the fixtures were held to the clauses they record, not to veraPDF run now")
	}
}

// veraPDFClauses runs veraPDF over every PDF in dir at a flavour and returns,
// per file name, the distinct clauses it fails the file on (none for a pass).
func veraPDFClauses(t *testing.T, vera, flavour, dir string) map[string][]string {
	t.Helper()
	out, err := exec.Command(vera, "--flavour", flavour, "--format", "text", "-v", "--recurse", dir).Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("veraPDF: %v", err)
	}
	verdicts := map[string][]string{}
	var current string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "PASS "), strings.HasPrefix(line, "FAIL "):
			rest := line[5:]
			if i := strings.LastIndexByte(rest, ' '); i > 0 {
				rest = rest[:i]
			}
			current = filepath.Base(rest)
			verdicts[current] = nil
		case strings.HasPrefix(line, "  FAIL ") && current != "":
			rule := strings.TrimSpace(line[len("  FAIL "):])
			if i := strings.LastIndexByte(rule, '-'); i > 0 {
				rule = rule[:i]
			}
			if !slices.Contains(verdicts[current], rule) {
				verdicts[current] = append(verdicts[current], rule)
			}
		}
	}
	for k := range verdicts {
		slices.Sort(verdicts[k])
	}
	return verdicts
}
