package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/forme/fonttest"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Single-defect files, made here: a PDF/A skeleton veraPDF passes at each
// part, given one page and one thing wrong with it — or one thing that only
// looks wrong. Each records the clauses veraPDF fails it on at PDF/A-1b, -2b
// and -4, and pdf0 must report exactly those. The files are built by the
// test, never committed, and when PDF0_VERAPDF names veraPDF it is run over
// them too, so the recorded answers cannot drift from it.
//
// They pin what the corpus does not test: most of them are rules where pdf0
// once said more than veraPDF (a non-standard rendering intent or PostScript
// at PDF/A-4, which its profile has no rule for; a name no resource
// dictionary defines at PDF/A-1 and -2; a non-widget annotation without /AP
// at PDF/A-1) or less (BitsPerComponent, FileAttachment at PDF/A-4).

// defectFixture is one file: what is wrong with it, and veraPDF's clauses at
// 1b, 2b and 4 — none for a file it passes.
type defectFixture struct {
	name  string
	build func(d *Document, page, res *object.Dictionary) string // the page's content
	// patch, when set, changes the written file: for a defect in the bytes
	// rather than the objects.
	patch func([]byte) []byte
	want  [3][]string
}

var defectLevels = [3]struct {
	level   pdfa.Level
	flavour string
}{{pdfa.PDFA1b, "1b"}, {pdfa.PDFA2b, "2b"}, {pdfa.PDFA4, "4"}}

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
// "A" (Macintosh standard name 36); the program's own post table names none.
func fxPostNames() []byte {
	post := make([]byte, 32+2+2*2)
	post[1] = 2   // version 2.0
	post[33] = 2  // numGlyphs
	post[37] = 36 // glyph 1; glyph 0 is name 0, .notdef
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
		program := fxTrueTypeProgramWith([]fonttest.CmapSub{{Plat: 0, Enc: 3, Data: fxFormat0()}}, map[string][]byte{"post": fxPostNames()})
		return fxFont(d, r, program, false, nil)
	}, want: [3][]string{nil, nil, {"6.2.10.6"}}},
	{name: "symbolic-truetype-without-30-or-10-cmap", build: func(d *Document, p, r *object.Dictionary) string {
		return fxFont(d, r, fxTrueTypeProgram([]fonttest.CmapSub{{Plat: 3, Enc: 1, Data: fxFormat0()}}), true, nil)
	}, want: [3][]string{{"6.3.5"}, {"6.2.11.4.1"}, {"6.2.10.4.1", "6.2.10.6"}}},
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
	}, want: [3][]string{{"6.5.2"}, nil, {"6.3.1"}}},
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
	}, want: [3][]string{{"6.1.11"}, {"6.8"}, nil}},
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
	}, want: [3][]string{{"6.1.11"}, {"6.8"}, {"6.9"}}},
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
	{name: "alternate-presentations", build: func(d *Document, p, r *object.Dictionary) string {
		fxCatalog(d).Set("Names", object.NewDictionary(object.Entry{Key: "AlternatePresentations", Value: object.NewDictionary(
			object.Entry{Key: "Names", Value: object.Array{}})}))
		return ""
	}, want: [3][]string{nil, {"6.10"}, {"6.11"}}},
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
	page.Set("Contents", d.Add(object.NewStream(&object.Dictionary{}, []byte(drawn))))
	pages := d.ResolveDict(pagesRef)
	pages.Set("Kids", object.Array{d.Add(page)})
	pages.Set("Count", fxInt(1))
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
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
	for i, lv := range defectLevels {
		dir := filepath.Join(t.TempDir(), lv.flavour)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range defectFixtures {
			data := buildDefect(t, f, lv.level)
			want := slices.Clone(f.want[i])
			slices.Sort(want)
			if got := pdf0Clauses(t, data, lv.level); !slices.Equal(got, want) {
				t.Errorf("%s at %s: pdf0 fails it on %v; veraPDF, as the fixture records, on %v", f.name, lv.flavour, got, want)
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
			want := slices.Clone(f.want[i])
			slices.Sort(want)
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
