package fonts

import (
	"os"
	"slices"
	"sort"
	"strconv"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
)

// versionFaces are a face of every kind this package writes, each with text
// it draws, loaded by the subtest that uses it (a face from a dataset skips
// only that subtest when the dataset is absent).
var versionFaces = []struct {
	name string
	load func(t *testing.T) *Face
	text string
}{
	{"truetype", func(t *testing.T) *Face { return mustFace(t)(NotoSans()) }, "Hello"},
	{"simple", func(t *testing.T) *Face { return mustFace(t)(NotoSansSimple()) }, "Hello"},
	{"standard", func(t *testing.T) *Face { return mustFace(t)(Standard("Helvetica")) }, "Hello"},
	{"namecff", func(t *testing.T) *Face {
		return mustFace(t)(Load(formeTestFile(t, "testdata/harfbuzz/fonts/CFFInk.otf")))
	}, "Hello"},
	{"cidcff", cjkFace, "日本語"},
	{"strikes", strikesFace, "AB"},
	{"emoji", func(t *testing.T) *Face {
		data, err := os.ReadFile(testfiles.NotoEmoji.File(t, "NotoColorEmoji.ttf"))
		if err != nil {
			t.Fatal(err)
		}
		return mustFace(t)(Load(data))
	}, "😀🇫🇷"},
	{"colr", func(t *testing.T) *Face {
		data, err := os.ReadFile(testfiles.Committed(t, "testdata/colrv1/test_glyphs-glyf_colr_1.ttf"))
		if err != nil {
			t.Fatal(err)
		}
		return mustFace(t)(Load(data))
	}, ""}, // every character the font maps: see drawAll
}

func mustFace(t *testing.T) func(*Face, error) *Face {
	return func(f *Face, err error) *Face {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
}

// texts are the strings a face is drawn with, one embedding each: text, or,
// for empty text, each character the face maps on its own, so that every way
// the face paints a glyph is written even where one glyph cannot be (the COLR
// conformance font has glyphs in blend modes PDF has no equivalent for).
func texts(face *Face, text string) []string {
	if text != "" {
		return []string{text}
	}
	var rs []rune
	for r := range face.Cmap() {
		rs = append(rs, r)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] })
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

// embedEach embeds face drawn with each of texts(face, text) in a fresh copy
// loaded by load, and returns what each embedding that succeeded wrote and
// its Version. A face drawn from text it names is required to embed; one
// drawn glyph by glyph only needs some glyph to.
func embedEach(t *testing.T, load func(*testing.T) *Face, text string, forms Forms) (objs [][]object.Object, versions []string) {
	t.Helper()
	for _, s := range texts(load(t), text) {
		face := load(t)
		drawAll(face, s)
		c := &collector{}
		e, err := face.EmbedForms(c, forms)
		if err != nil {
			if text != "" {
				t.Fatalf("%+v: %v", forms, err)
			}
			continue
		}
		objs = append(objs, c.objs)
		versions = append(versions, e.Version)
	}
	if len(objs) == 0 {
		t.Fatalf("%+v: nothing embedded", forms)
	}
	return objs, versions
}

// drawAll draws text in face.
func drawAll(face *Face, text string) {
	var b content.Builder
	// A DeviceRGB text colour: a COLR glyph whose gradient stops in the text
	// colour needs one.
	b.SetRGB(0.2, 0.3, 0.4)
	b.BeginText().SetFont("F1", 12)
	face.DrawShaped(&b, text, 12)
	b.EndText()
}

// collector is an Allocator that keeps every object added.
type collector struct{ objs []object.Object }

func (c *collector) Add(o object.Object) object.IndirectRef {
	c.objs = append(c.objs, o)
	return object.IndirectRef{Number: len(c.objs)}
}

// versionFormsOf are the Forms a face can be written with that change what is
// written: the OpenType wrapper or the bare CFF, transparency or none.
var versionFormsOf = []Forms{
	{Horizontal: true},
	{Horizontal: true, BareCFF: true},
	{Horizontal: true, Opaque: true},
	{Horizontal: true, PDFA1: true, Opaque: true},
}

// baselineKeys are the dictionary keys this package writes that PDF 1.0 or
// 1.1 already has, or that occur only in a dictionary another of whose keys
// carries the version: a function's /Domain beside its /FunctionType, a
// shading's /Coords beside its /ShadingType, a group's /S beside /Group.
// keyVersions lists the rest. A key in neither fails
// TestEveryFeatureTheWriterUsesHasAVersion until its version is decided.
var baselineKeys = map[object.Name]bool{
	// Font dictionaries and descriptors (9.6, 9.8): PDF 1.0/1.1.
	"Type": true, "Subtype": true, "BaseFont": true, "FirstChar": true, "LastChar": true,
	"Widths": true, "FontDescriptor": true, "Encoding": true, "Differences": true,
	"FontName": true, "Flags": true, "FontBBox": true, "ItalicAngle": true, "Ascent": true,
	"Descent": true, "CapHeight": true, "StemV": true, "FontFile2": true,
	"FontMatrix": true, "CharProcs": true,
	// Streams and images (7.3.8, 8.9.5): PDF 1.0.
	"Length": true, "Length1": true, "Filter": true, "Width": true, "Height": true,
	"ColorSpace": true, "BitsPerComponent": true, "ImageMask": true, "Decode": true,
	// Form XObjects (8.10): PDF 1.0 (/Resources is in keyVersions).
	"BBox": true, "Matrix": true, "XObject": true,
	// A function's type is classified by its value (functionVersions).
	"FunctionType": true,
	// Soft-mask dictionary entries, inside an /SMask.
	"G": true, "TR": true,
	// Function entries, beside /FunctionType.
	"Domain": true, "Range": true, "C0": true, "C1": true, "N": true, "Bounds": true,
	"Encode": true, "Functions": true, "Function": true,
	// Shading entries, beside /ShadingType.
	"Coords": true, "Extend": true,
	// Transparency group entries, inside a /Group.
	"S": true, "CS": true, "I": true, "K": true,
}

// baselineValues are the /Subtype and /Filter values this package writes that
// PDF 1.0 or 1.1 already has.
var baselineValues = map[object.Name]bool{
	"TrueType": true, "Type1": true, "Type3": true, "Form": true, "Image": true,
}

// TestEveryFeatureTheWriterUsesHasAVersion writes every kind of face with
// every option that changes what is written, and requires every key, every
// /Subtype and /Filter value and every function type in what it wrote to be
// classified: in keyVersions, subtypeVersions, filterVersions or
// functionVersions with the version ISO 32000-2 gives it, or in the PDF 1.1
// baseline above. A feature the writer comes to use that neither lists fails
// here, so Embedded.Version cannot silently understate what a file needs.
func TestEveryFeatureTheWriterUsesHasAVersion(t *testing.T) {
	for _, k := range versionFaces {
		t.Run(k.name, func(t *testing.T) {
			for _, forms := range versionFormsOf {
				written, _ := embedEach(t, k.load, k.text, forms)
				for _, objs := range written {
					for _, o := range objs {
						unclassified(t, o, "")
					}
				}
			}
		})
	}
}

// userNamed are the keys whose dictionaries are maps named by the writer —
// a Type 3 font's glyph procedures, a resource dictionary's entries — whose
// own keys are names, not features.
var userNamed = map[string]bool{"CharProcs": true, "XObject": true, "ExtGState": true, "Shading": true, "Font": true}

func unclassified(t *testing.T, o object.Object, parent string) {
	t.Helper()
	switch x := o.(type) {
	case *object.Stream:
		unclassified(t, &x.Dict, parent)
	case *object.Dictionary:
		if userNamed[parent] {
			for v := range x.Values() {
				unclassified(t, v, "")
			}
			return
		}
		for key, val := range x.All() {
			if _, ok := keyVersions[key]; !ok && !baselineKeys[key] {
				t.Errorf("the writer uses the key %s, whose PDF version is not decided (keyVersions or baselineKeys)", key)
			}
			switch key {
			case "Subtype", "Filter":
				names := []object.Object{val}
				if a, ok := val.(object.Array); ok {
					names = a
				}
				for _, n := range names {
					n, ok := n.(object.Name)
					if !ok {
						continue
					}
					_, s := subtypeVersions[n]
					_, f := filterVersions[n]
					if !s && !f && !baselineValues[n] {
						t.Errorf("the writer uses %s %s, whose PDF version is not decided", key, n)
					}
				}
			case "FunctionType":
				if n, ok := val.(object.Integer); ok {
					if _, ok := functionVersions[int64(n)]; !ok {
						t.Errorf("the writer uses function type %s, whose PDF version is not decided", strconv.FormatInt(int64(n), 10))
					}
				}
			}
			unclassified(t, val, string(key))
		}
	case object.Array:
		for _, e := range x {
			unclassified(t, e, parent)
		}
	}
}

// TestEmbeddedVersionIsWhatTheFontNeeds holds Embedded.Version, per kind of
// face and option, to the feature that decides it.
func TestEmbeddedVersionIsWhatTheFontNeeds(t *testing.T) {
	want := map[string]map[string]string{
		// Flate and ToUnicode (1.2); a CIDFontType2's FontFile2 (1.3).
		"truetype": {"": "1.3", "bare": "1.3", "opaque": "1.3"},
		// A TrueType font with Flate streams and a ToUnicode CMap.
		"simple": {"": "1.2", "bare": "1.2", "opaque": "1.2"},
		// A name and an encoding: nothing later than 1.1.
		"standard": {"": "1.1", "bare": "1.1", "opaque": "1.1"},
		// The OpenType wrapper is 1.6; the bare CFF 1.3.
		"namecff": {"": "1.6", "bare": "1.3", "opaque": "1.6"},
		"cidcff":  {"": "1.6", "bare": "1.3", "opaque": "1.6"},
		// Greyscale strikes are painted through a soft mask (1.4); opaque,
		// they are stencils in a Type 3 font with /Resources (1.2).
		"strikes": {"": "1.4", "bare": "1.4", "opaque": "1.2"},
		// Colour bitmaps with alpha: a soft mask (1.4); opaque, an explicit
		// /Mask (1.3).
		"emoji": {"": "1.4", "bare": "1.4", "opaque": "1.3"},
		// COLR: groups and soft masks (1.4); opaque, the outlines in the text
		// colour.
		"colr": {"": "1.4", "bare": "1.4", "opaque": "1.2"},
	}
	opts := map[string]Forms{
		"":       {Horizontal: true},
		"bare":   {Horizontal: true, BareCFF: true},
		"opaque": {Horizontal: true, Opaque: true},
	}
	for _, k := range versionFaces {
		t.Run(k.name, func(t *testing.T) {
			for opt, forms := range opts {
				// Drawn glyph by glyph, the latest any glyph needs.
				_, versions := embedEach(t, k.load, k.text, forms)
				got := slices.Max(versions)
				if w := want[k.name][opt]; got != w {
					t.Errorf("%q: Version %s; want %s", opt, got, w)
				}
			}
		})
	}
}

// TestBareCFFIsTheCFFAlone writes a CFF face with BareCFF and no PDF/A-1:
// the program is the bare CFF, and a CFF that is not CID-keyed carries no
// /CIDSet, which only PDF/A-1 asks of it.
func TestBareCFFIsTheCFFAlone(t *testing.T) {
	face := mustFace(t)(Load(formeTestFile(t, "testdata/harfbuzz/fonts/CFFInk.otf")))
	drawAll(face, "Hello")
	c := &collector{}
	if _, err := face.EmbedForms(c, Forms{Horizontal: true, BareCFF: true}); err != nil {
		t.Fatal(err)
	}
	var file *object.Stream
	for _, o := range c.objs {
		d, ok := o.(*object.Dictionary)
		if !ok || d.Get("FontFile3") == nil {
			continue
		}
		if d.Get("CIDSet") != nil {
			t.Error("a bare CFF that is not CID-keyed carries a /CIDSet outside PDF/A-1")
		}
		file = c.objs[d.Get("FontFile3").(object.IndirectRef).Number-1].(*object.Stream)
	}
	if file == nil {
		t.Fatal("no FontFile3 written")
	}
	if got := file.Dict.Get("Subtype"); got != object.Name("CIDFontType0C") {
		t.Errorf("the font file's /Subtype is %v; want /CIDFontType0C", got)
	}
	data := inflate(t, file)
	if len(data) < 4 || data[0] != 1 {
		t.Errorf("the font file is not a bare CFF (it begins % X)", data[:min(4, len(data))])
	}
}
