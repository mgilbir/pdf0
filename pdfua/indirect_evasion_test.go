package pdfua

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// PDF/UA rules that were escapable by writing one value as an indirect
// reference.
//
// The same defect as in the PDF/A rules and for the same reason: ISO 32000 lets
// almost any value be an indirect reference, so `/Subtype 9 0 R` naming
// `/TrapNet` is a legal TrapNet annotation, and a check that reads the key
// without resolving it concludes the annotation is something else.
//
// The corpus cannot guard this. Running the whole PDF/UA-1 and -2 suites before
// and after the sweep gives the same 337 files flagged and the same 630
// findings — real documents do not write these keys indirectly, which is
// exactly why the hole survived. Every case below was watched to fail against
// the unswept code.

// indirect returns a reference to obj, registering it in objs under num.
func indirect(objs map[int]*object.IndirectObject, num int, obj object.Object) object.IndirectRef {
	objs[num] = &object.IndirectObject{Number: num, Value: obj}
	return object.IndirectRef{Number: num}
}

func has(v []Violation, want string) bool {
	for _, e := range v {
		if strings.Contains(e.Message, want) {
			return true
		}
	}
	return false
}

// TestAnAnnotationSubtypeWrittenIndirectlyIsStillRead, in both directions.
//
// A TrapNet annotation is not permitted at all; a Popup is exempt from the
// alternate-description rule. Reading /Subtype without resolving lost the
// first and broke the second.
func TestAnAnnotationSubtypeWrittenIndirectlyIsStillRead(t *testing.T) {
	build := func(form string, subtype object.Name) []Violation {
		objs := map[int]*object.IndirectObject{}
		a := &object.Dictionary{}
		a.Set("Type", object.Name("Annot"))
		if form == "direct" {
			a.Set("Subtype", subtype)
		} else {
			a.Set("Subtype", indirect(objs, 9, subtype))
		}
		objs[5] = &object.IndirectObject{Number: 5, Value: a}
		return checkUAAnnotations(referenced(mkView(objs, nil), 5))
	}

	for _, form := range []string{"direct", "indirect"} {
		if v := build(form, "TrapNet"); !has(v, "TrapNet annotations are not permitted") {
			t.Errorf("%s /Subtype: a TrapNet annotation was not reported: %v", form, v)
		}
		// And the exemption, which fails the opposite way: a Popup that did
		// not read as one was reported for a description it does not need.
		if v := build(form, "Popup"); len(v) != 0 {
			t.Errorf("%s /Subtype: a Popup annotation, which is exempt, was "+
				"reported: %v", form, v)
		}
	}
}

// TestASubsetTagWrittenIndirectlyIsStillASubsetTag.
//
// isSubsetFont reads /BaseFont and gates the glyph-coverage and /CIDSet rules,
// so an indirect /BaseFont skipped both.
func TestASubsetTagWrittenIndirectlyIsStillASubsetTag(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}
		f := &object.Dictionary{}
		if form == "direct" {
			f.Set("BaseFont", object.Name("ABCDEF+Arial"))
		} else {
			f.Set("BaseFont", indirect(objs, 9, object.Name("ABCDEF+Arial")))
		}
		if !isSubsetFont(mkView(objs, nil), f) {
			t.Errorf("%s /BaseFont: a subset tag was not recognised, so the "+
				"glyph-coverage and /CIDSet rules do not run", form)
		}
	}
}

// TestAStructureElementTypeWrittenIndirectlyIsStillAStructureElement.
//
// /S is what makes a dictionary a structure element; without reading it the
// element and everything under it drop out of the nesting rules.
func TestAStructureElementTypeWrittenIndirectlyIsStillAStructureElement(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}

		// A <TD> directly under <Document>, which 7.2 does not allow.
		cell := &object.Dictionary{}
		cell.Set("Type", object.Name("StructElem"))
		if form == "direct" {
			cell.Set("S", object.Name("TD"))
		} else {
			cell.Set("S", indirect(objs, 9, object.Name("TD")))
		}

		root := &object.Dictionary{}
		root.Set("Type", object.Name("StructElem"))
		root.Set("S", object.Name("Document"))
		root.Set("K", object.Array{indirect(objs, 4, cell)})

		treeRoot := &object.Dictionary{}
		treeRoot.Set("Type", object.Name("StructTreeRoot"))
		treeRoot.Set("K", object.Array{indirect(objs, 3, root)})

		catalog := &object.Dictionary{}
		catalog.Set("Type", object.Name("Catalog"))
		catalog.Set("StructTreeRoot", indirect(objs, 2, treeRoot))
		objs[1] = &object.IndirectObject{Number: 1, Value: catalog}
		trailer := object.Dictionary{}
		trailer.Set("Root", object.IndirectRef{Number: 1})

		v := checkUAStructNesting(mkView(objs, &trailer), catalog)
		if !has(v, "TD") {
			t.Errorf("%s /S: a <TD> outside a table was not reported — the "+
				"element was not seen as a structure element at all: %v", form, v)
		}
	}
}
