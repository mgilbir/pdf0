package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// The requirements a PDF/A-4 file takes on as an E or an F.
//
// Each variant relaxes something the base part forbids and adds something in
// exchange. pdf0 honoured the relaxations and not the exchange, so a file could
// claim the variant, collect the permission, and owe nothing for it. Both are
// gated on the target: these checks are called at the variant levels.

// efDoc builds a catalog carrying the given pdfaid:conformance, plus whatever
// extra objects a case needs.
func efDoc(conformance string, catalogExtra func(*object.Dictionary), objs map[int]*object.IndirectObject) core.View {
	if objs == nil {
		objs = map[int]*object.IndirectObject{}
	}
	xmp := `<?xpacket begin=""?><x:xmpmeta xmlns:x="adobe:ns:meta/">
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
<pdfaid:part>4</pdfaid:part>`
	if conformance != "" {
		xmp += `<pdfaid:conformance>` + conformance + `</pdfaid:conformance>`
	}
	xmp += `</rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="r"?>`

	meta := &object.Stream{Dict: object.Dictionary{}, Data: []byte(xmp)}
	meta.Dict.Set("Type", object.Name("Metadata"))
	objs[50] = &object.IndirectObject{Number: 50, Value: meta}

	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Metadata", object.IndirectRef{Number: 50})
	if catalogExtra != nil {
		catalogExtra(cat)
	}
	objs[1] = &object.IndirectObject{Number: 1, Value: cat}
	tr := object.Dictionary{}
	tr.Set("Root", object.IndirectRef{Number: 1})
	return mkView(objs, &tr)
}

// TestAnFMustCarryTheFilesItsNameClaims (ISO 19005-4 6.9).
func TestAnFMustCarryTheFilesItsNameClaims(t *testing.T) {
	// No name dictionary at all, and a name dictionary without the key: two
	// shapes of the same violation, reported distinctly so the message tells a
	// reader which one they have.
	if v := checkA4FEmbeddedFilesPresent(efDoc("F", nil, nil), PDFA4F); !hasMessage(v, "no name dictionary") {
		t.Errorf("a PDF/A-4f file with no name dictionary was not reported: %v", v)
	}
	withNames := func(cat *object.Dictionary) {
		cat.Set("Names", &object.Dictionary{})
	}
	if v := checkA4FEmbeddedFilesPresent(efDoc("F", withNames, nil), PDFA4F); !hasMessage(v, "/EmbeddedFiles") {
		t.Errorf("a PDF/A-4f file whose name dictionary has no /EmbeddedFiles was not reported: %v", v)
	}

	// With the key, nothing to say.
	withEF := func(cat *object.Dictionary) {
		n := &object.Dictionary{}
		n.Set("EmbeddedFiles", &object.Dictionary{})
		cat.Set("Names", n)
	}
	if v := checkA4FEmbeddedFilesPresent(efDoc("F", withEF, nil), PDFA4F); len(v) != 0 {
		t.Errorf("a conforming PDF/A-4f file was reported: %v", v)
	}

	// And the rule belongs to F alone. A plain PDF/A-4 file, or an E, is not
	// required to attach anything — reporting them would refuse a conforming
	// document for a variant it was not validated as.
	for _, lvl := range []Level{PDFA4, PDFA4E} {
		if v := checkA4FEmbeddedFilesPresent(efDoc("F", nil, nil), lvl); len(v) != 0 {
			t.Errorf("%s was held to the PDF/A-4f attachment rule: %v", lvl, v)
		}
	}
	// Nor does it apply at the earlier parts, which have no such variant.
	for _, lvl := range []Level{PDFA1b, PDFA2b, PDFA3b} {
		if v := checkA4FEmbeddedFilesPresent(efDoc("F", nil, nil), lvl); len(v) != 0 {
			t.Errorf("%s was held to a PDF/A-4 variant rule: %v", lvl, v)
		}
	}
}

// TestAnEsArtworkMustBeInAFormatTheStandardNames (ISO 19005-4 6.1.6.1).
func TestAnEsArtworkMustBeInAFormatTheStandardNames(t *testing.T) {
	stream := func(subtype object.Object) map[int]*object.IndirectObject {
		s := &object.Stream{Dict: object.Dictionary{}}
		s.Dict.Set("Type", object.Name("3D"))
		if subtype != nil {
			s.Dict.Set("Subtype", subtype)
		}
		return map[int]*object.IndirectObject{15: {Number: 15, Value: s}}
	}

	for _, ok := range []string{"U3D", "PRC"} {
		if v := checkA4E3DStreamSubtype(referenced(efDoc("E", nil, stream(object.Name(ok))), 15), PDFA4E); len(v) != 0 {
			t.Errorf("/%s is a permitted 3D format and was reported: %v", ok, v)
		}
	}
	// The corpus file's own case: the right letters in the wrong case. A name
	// is case-sensitive, so /u3d is not /U3D.
	if v := checkA4E3DStreamSubtype(referenced(efDoc("E", nil, stream(object.Name("u3d"))), 15), PDFA4E); !hasMessage(v, "/u3d") {
		t.Errorf("a lowercase /u3d was accepted as /U3D: %v", v)
	}
	if v := checkA4E3DStreamSubtype(referenced(efDoc("E", nil, stream(object.Name("STL"))), 15), PDFA4E); !hasMessage(v, "/STL") {
		t.Errorf("an unlisted 3D format was not reported: %v", v)
	}
	if v := checkA4E3DStreamSubtype(referenced(efDoc("E", nil, stream(nil)), 15), PDFA4E); !hasMessage(v, "no /Subtype") {
		t.Errorf("a 3D stream with no /Subtype was not reported: %v", v)
	}

	// Only at E. Anywhere else a 3D annotation is itself forbidden, and the
	// annotation rule says so; adding a complaint about the artwork format
	// would be answering a question nobody reached.
	for _, lvl := range []Level{PDFA4, PDFA4F} {
		if v := checkA4E3DStreamSubtype(referenced(efDoc("E", nil, stream(object.Name("STL"))), 15), lvl); len(v) != 0 {
			t.Errorf("%s was held to the PDF/A-4e artwork rule: %v", lvl, v)
		}
	}
}

// TestAThreeDAnnotationsColourIsStillColour.
//
// A 3D annotation's artwork is not in its appearance stream: the 3D stream
// carries its own /ColorSpace. The device-colour scan walked /AP and stopped,
// so a document could paint in DeviceRGB with no output intent and be told it
// was fine — PDF/A-4e permits the annotation, not the unmanaged colour.
func TestAThreeDAnnotationsColourIsStillColour(t *testing.T) {
	build := func(cs object.Object) *object.Dictionary {
		s := &object.Stream{Dict: object.Dictionary{}}
		s.Dict.Set("Type", object.Name("3D"))
		s.Dict.Set("Subtype", object.Name("U3D"))
		if cs != nil {
			s.Dict.Set("ColorSpace", cs)
		}
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("3D"))
		annot.Set("3DD", s)
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Annots", object.Array{annot})
		return page
	}

	doc := mkView(map[int]*object.IndirectObject{}, nil)
	rgb, cmyk, gray := core.PageDeviceColourUse(doc, build(object.Name("DeviceRGB")))
	if !rgb {
		t.Error("DeviceRGB in a 3D annotation's artwork was not seen as device colour")
	}
	if cmyk || gray {
		t.Errorf("DeviceRGB was reported as cmyk=%v gray=%v", cmyk, gray)
	}

	_, cmyk, _ = core.PageDeviceColourUse(doc, build(object.Name("DeviceCMYK")))
	if !cmyk {
		t.Error("DeviceCMYK in a 3D annotation's artwork was not seen")
	}

	// A managed colour space is not device colour, and neither is none at all.
	rgb, cmyk, gray = core.PageDeviceColourUse(doc, build(nil))
	if rgb || cmyk || gray {
		t.Errorf("a 3D stream with no /ColorSpace reported device colour: %v %v %v", rgb, cmyk, gray)
	}
}
