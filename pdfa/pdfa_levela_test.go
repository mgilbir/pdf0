package pdfa

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

// levelADoc builds a minimal document with the catalog entries the Level A
// checks read: optional MarkInfo/Marked, StructTreeRoot, Lang, and an XMP
// metadata stream declaring the given pdfaid:conformance.
func levelADoc(marked, structTree bool, lang, conformance string) core.View {
	d := mkV(core.View{Objects: map[int]*object.IndirectObject{}, Version: "1.4"})
	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	if marked {
		mi := &object.Dictionary{}
		mi.Set("Marked", object.Boolean(true))
		cat.Set("MarkInfo", mi)
	}
	if structTree {
		cat.Set("StructTreeRoot", object.IndirectRef{Number: 9})
	}
	if lang != "" {
		cat.Set("Lang", object.String{Value: []byte(lang)})
	}
	if conformance != "" {
		xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
			`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">` +
			`<pdfaid:part>1</pdfaid:part><pdfaid:conformance>` + conformance + `</pdfaid:conformance>` +
			`</rdf:Description></rdf:RDF></x:xmpmeta>`
		ms := &object.Stream{Dict: object.Dictionary{}, Data: []byte(xmp)}
		ms.Dict.Set("Type", object.Name("Metadata"))
		ms.Dict.Set("Subtype", object.Name("XML"))
		d.Objects[2] = &object.IndirectObject{Number: 2, Value: ms}
		cat.Set("Metadata", object.IndirectRef{Number: 2})
	}
	d.Objects[1] = &object.IndirectObject{Number: 1, Value: cat}
	*d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

func hasMsg(errs []Violation, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func TestLevelAStructureCheck(t *testing.T) {
	// Tagged with a structure tree: no structure finding.
	if v := checkLevelAStructure(levelADoc(true, true, "en", "A"), PDFA1a); len(v) != 0 {
		t.Errorf("tagged document flagged: %v", v)
	}
	// Not marked as tagged.
	if v := checkLevelAStructure(levelADoc(false, true, "en", "A"), PDFA1a); !hasMsg(v, "Tagged PDF") {
		t.Errorf("expected a Tagged-PDF finding; got %v", v)
	}
	// No structure tree.
	if v := checkLevelAStructure(levelADoc(true, false, "en", "A"), PDFA1a); !hasMsg(v, "logical structure tree") {
		t.Errorf("expected a structure-tree finding; got %v", v)
	}
}

func TestLevelAConformanceCheck(t *testing.T) {
	if v := checkLevelAConformance(levelADoc(true, true, "en", "A"), PDFA1a); len(v) != 0 {
		t.Errorf("conformance A flagged: %v", v)
	}
	if v := checkLevelAConformance(levelADoc(true, true, "en", "B"), PDFA1a); !hasMsg(v, "must be A") {
		t.Errorf("expected a conformance finding for B at Level A; got %v", v)
	}
}

func TestLevelALanguageCheck(t *testing.T) {
	// A valid tag (including a UTF-16BE-encoded one) is accepted.
	if v := checkLevelALanguage(levelADoc(true, true, "en-GB", ""), PDFA1a); len(v) != 0 {
		t.Errorf("valid /Lang flagged: %v", v)
	}
	utf16 := append([]byte{0xFE, 0xFF}, utf16be("en-GB")[2:]...)
	if v := checkLevelALanguage(levelADoc(true, true, string(utf16), ""), PDFA1a); len(v) != 0 {
		t.Errorf("valid UTF-16 /Lang flagged: %v", v)
	}
	// A syntactically invalid tag is flagged.
	if v := checkLevelALanguage(levelADoc(true, true, "not a tag!", ""), PDFA1a); !hasMsg(v, "not a valid language") {
		t.Errorf("expected an invalid-/Lang finding; got %v", v)
	}
}
