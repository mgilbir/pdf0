package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildDPartDoc constructs a document with four pages and a valid two-level
// DPart hierarchy: an internal root node with two leaf parts, each spanning two
// pages, with matching page /DPart back-references and a two-entry NodeNameList.
//
//	catalog(1) → Pages(2) → pages 3,4,5,6
//	DPartRoot(7) → DPartRootNode(8, internal, /DParts [[9 10]])
//	  leaf 9  Start=3 End=4   leaf 10  Start=5 End=6
func buildDPartDoc() *Document {
	d := &Document{Objects: map[int]*object.IndirectObject{}, Version: "2.0"}
	set := func(num int, v object.Object) { d.Objects[num] = &object.IndirectObject{Number: num, Value: v} }

	cat := &object.Dictionary{}
	cat.Set("Type", object.Name("Catalog"))
	cat.Set("Pages", object.IndirectRef{Number: 2})
	cat.Set("DPartRoot", object.IndirectRef{Number: 7})
	set(1, cat)

	pages := &object.Dictionary{}
	pages.Set("Type", object.Name("Pages"))
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 3}, object.IndirectRef{Number: 4}, object.IndirectRef{Number: 5}, object.IndirectRef{Number: 6}})
	pages.Set("Count", object.Integer(4))
	set(2, pages)

	// pages 3,4 belong to leaf 9; pages 5,6 to leaf 10.
	for _, pg := range []struct{ num, leaf int }{{3, 9}, {4, 9}, {5, 10}, {6, 10}} {
		p := &object.Dictionary{}
		p.Set("Type", object.Name("Page"))
		p.Set("Parent", object.IndirectRef{Number: 2})
		p.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		p.Set("DPart", object.IndirectRef{Number: pg.leaf})
		set(pg.num, p)
	}

	root := &object.Dictionary{}
	root.Set("Type", object.Name("DPartRoot"))
	root.Set("DPartRootNode", object.IndirectRef{Number: 8})
	root.Set("NodeNameList", object.Array{object.Name("Document"), object.Name("Section")})
	set(7, root)

	node := &object.Dictionary{}
	node.Set("Type", object.Name("DPart"))
	node.Set("Parent", object.IndirectRef{Number: 7})
	node.Set("DParts", object.Array{object.Array{object.IndirectRef{Number: 9}, object.IndirectRef{Number: 10}}})
	set(8, node)

	leaf1 := &object.Dictionary{}
	leaf1.Set("Type", object.Name("DPart"))
	leaf1.Set("Parent", object.IndirectRef{Number: 8})
	leaf1.Set("Start", object.IndirectRef{Number: 3})
	leaf1.Set("End", object.IndirectRef{Number: 4})
	set(9, leaf1)

	leaf2 := &object.Dictionary{}
	leaf2.Set("Type", object.Name("DPart"))
	leaf2.Set("Parent", object.IndirectRef{Number: 8})
	leaf2.Set("Start", object.IndirectRef{Number: 5})
	leaf2.Set("End", object.IndirectRef{Number: 6})
	set(10, leaf2)

	d.Trailer = object.Dictionary{}
	d.Trailer.Set("Root", object.IndirectRef{Number: 1})
	return d
}

// leafDict returns the leaf DPart dictionary numbered num from a built doc.
func objDict(d *Document, num int) *object.Dictionary {
	return d.Objects[num].Value.(*object.Dictionary)
}

func TestValidateDPartsValid(t *testing.T) {
	d := buildDPartDoc()
	if v := ValidateDParts(d); len(v) != 0 {
		t.Fatalf("valid hierarchy reported %d violation(s): %v", len(v), v)
	}
}

func TestValidateDPartsNoRootIsValid(t *testing.T) {
	d := buildDPartDoc()
	objDict(d, 1).Delete("DPartRoot") // remove the hierarchy entirely
	if v := ValidateDParts(d); v != nil {
		t.Fatalf("a document without /DPartRoot must be valid, got %v", v)
	}
}

func TestValidateDPartsViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(d *Document)
		rule   string
		substr string
	}{
		{"missing DPartRootNode", func(d *Document) { objDict(d, 7).Delete("DPartRootNode") }, "14.12.4.1", "DPartRootNode"},
		{"wrong Parent", func(d *Document) { objDict(d, 9).Set("Parent", object.IndirectRef{Number: 7}) }, "14.12.2", "does not reference its actual parent"},
		{"missing Parent", func(d *Document) { objDict(d, 9).Delete("Parent") }, "14.12.4.1", "missing the required /Parent"},
		{"both DParts and Start", func(d *Document) { objDict(d, 9).Set("DParts", object.Array{object.Array{}}) }, "14.12.4.1", "both /DParts and /Start"},
		{"neither DParts nor Start", func(d *Document) { objDict(d, 9).Delete("Start"); objDict(d, 9).Delete("End") }, "14.12.4.1", "neither /DParts"},
		{"empty DParts", func(d *Document) { objDict(d, 8).Set("DParts", object.Array{}) }, "14.12.4.1", "non-empty array"},
		{"End before Start", func(d *Document) {
			objDict(d, 9).Set("Start", object.IndirectRef{Number: 4})
			objDict(d, 9).Set("End", object.IndirectRef{Number: 3})
		}, "14.12.4.1", "precedes /Start"},
		{"Start not a page", func(d *Document) { objDict(d, 9).Set("Start", object.IndirectRef{Number: 7}) }, "14.12.3", "/Start does not reference a page"},
		{"page uncovered / gap", func(d *Document) { objDict(d, 9).Set("End", object.IndirectRef{Number: 3}) }, "14.12.3", "not contiguous"},
		{"overlapping ranges", func(d *Document) { objDict(d, 10).Set("Start", object.IndirectRef{Number: 4}) }, "14.12.2", "more than one DPart leaf range"},
		{"wrong page back-ref", func(d *Document) { objDict(d, 3).Set("DPart", object.IndirectRef{Number: 10}) }, "14.12.3", "page /DPart does not reference"},
		{"NodeNameList wrong length", func(d *Document) { objDict(d, 7).Set("NodeNameList", object.Array{object.Name("Only")}) }, "14.12.4.1", "levels"},
		{"NodeNameList bad name", func(d *Document) {
			objDict(d, 7).Set("NodeNameList", object.Array{object.Name("1bad"), object.Name("Section")})
		}, "14.12.4.1", "not a valid XML name"},
		{"DPM disallowed value", func(d *Document) {
			dpm := &object.Dictionary{}
			dpm.Set("Kind", object.Name("aName")) // a name value is not permitted
			objDict(d, 9).Set("DPM", dpm)
		}, "14.12.4.2", "not permitted"},
		{"multi-parent / cycle", func(d *Document) {
			// make the internal node reference leaf 9 twice
			objDict(d, 8).Set("DParts", object.Array{object.Array{object.IndirectRef{Number: 9}, object.IndirectRef{Number: 9}, object.IndirectRef{Number: 10}}})
		}, "14.12.2", "more than one parent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := buildDPartDoc()
			tc.mutate(d)
			v := ValidateDParts(d)
			found := false
			for _, e := range v {
				if e.Rule == tc.rule && strings.Contains(e.Message, tc.substr) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected a %s violation containing %q; got %v", tc.rule, tc.substr, v)
			}
		})
	}
}

// TestValidateDPartsValidDPM confirms a well-formed DPM dictionary with nested
// arrays/dictionaries and permitted scalar types passes.
func TestValidateDPartsValidDPM(t *testing.T) {
	d := buildDPartDoc()
	dpm := &object.Dictionary{}
	dpm.Set("RecipientName", object.String{Value: []byte("Jane Doe")})
	dpm.Set("Copies", object.Integer(3))
	dpm.Set("Duplex", object.Boolean(true))
	nested := &object.Dictionary{}
	nested.Set("Zip", object.String{Value: []byte("93407")})
	dpm.Set("Address", nested)
	dpm.Set("Tags", object.Array{object.String{Value: []byte("a")}, object.Integer(1), object.Real(2.5)})
	objDict(d, 9).Set("DPM", dpm)
	if v := ValidateDParts(d); len(v) != 0 {
		t.Fatalf("valid DPM reported %d violation(s): %v", len(v), v)
	}
}

// TestValidateDPartsCalPolySuite is the FP=0 oracle for DPart validation: every
// file in the Cal Poly Graphic Communications PDF/VT-1 Test File Suite is a
// valid PDF/VT-1 document, so ValidateDParts must report no violations and never
// panic on any of them. The suite is not vendored; the test skips when
// testdata/pdfvt is absent (as in CI), mirroring the veraPDF corpus tests. It
// also confirms the parser scales to the largest members (up to ~195k pages).
func TestValidateDPartsCalPolySuite(t *testing.T) {
	files, _ := filepath.Glob("testdata/pdfvt/*.pdf")
	if len(files) == 0 {
		t.Skip("Cal Poly PDF/VT suite not present (testdata/pdfvt)")
	}
	for _, f := range files {
		name := filepath.Base(f)
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: ValidateDParts panicked: %v", name, r)
				}
			}()
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Errorf("%s: parse failed: %v", name, err)
				return
			}
			if v := ValidateDParts(doc); len(v) != 0 {
				t.Errorf("%s: expected 0 DPart violations on a valid PDF/VT-1 file, got %d (first: %s: %s)",
					name, len(v), v[0].Rule, v[0].Message)
			}
		}()
	}
}
