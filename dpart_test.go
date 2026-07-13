package pdf0

import (
	"bytes"
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
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "2.0"}
	set := func(num int, v Object) { d.Objects[num] = &IndirectObject{Number: num, Value: v} }

	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	cat.Set("Pages", IndirectRef{Number: 2})
	cat.Set("DPartRoot", IndirectRef{Number: 7})
	set(1, cat)

	pages := &Dictionary{}
	pages.Set("Type", Name("Pages"))
	pages.Set("Kids", Array{IndirectRef{Number: 3}, IndirectRef{Number: 4}, IndirectRef{Number: 5}, IndirectRef{Number: 6}})
	pages.Set("Count", Integer(4))
	set(2, pages)

	// pages 3,4 belong to leaf 9; pages 5,6 to leaf 10.
	for _, pg := range []struct{ num, leaf int }{{3, 9}, {4, 9}, {5, 10}, {6, 10}} {
		p := &Dictionary{}
		p.Set("Type", Name("Page"))
		p.Set("Parent", IndirectRef{Number: 2})
		p.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		p.Set("DPart", IndirectRef{Number: pg.leaf})
		set(pg.num, p)
	}

	root := &Dictionary{}
	root.Set("Type", Name("DPartRoot"))
	root.Set("DPartRootNode", IndirectRef{Number: 8})
	root.Set("NodeNameList", Array{Name("Document"), Name("Section")})
	set(7, root)

	node := &Dictionary{}
	node.Set("Type", Name("DPart"))
	node.Set("Parent", IndirectRef{Number: 7})
	node.Set("DParts", Array{Array{IndirectRef{Number: 9}, IndirectRef{Number: 10}}})
	set(8, node)

	leaf1 := &Dictionary{}
	leaf1.Set("Type", Name("DPart"))
	leaf1.Set("Parent", IndirectRef{Number: 8})
	leaf1.Set("Start", IndirectRef{Number: 3})
	leaf1.Set("End", IndirectRef{Number: 4})
	set(9, leaf1)

	leaf2 := &Dictionary{}
	leaf2.Set("Type", Name("DPart"))
	leaf2.Set("Parent", IndirectRef{Number: 8})
	leaf2.Set("Start", IndirectRef{Number: 5})
	leaf2.Set("End", IndirectRef{Number: 6})
	set(10, leaf2)

	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

// leafDict returns the leaf DPart dictionary numbered num from a built doc.
func objDict(d *Document, num int) *Dictionary { return d.Objects[num].Value.(*Dictionary) }

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
		{"wrong Parent", func(d *Document) { objDict(d, 9).Set("Parent", IndirectRef{Number: 7}) }, "14.12.2", "does not reference its actual parent"},
		{"missing Parent", func(d *Document) { objDict(d, 9).Delete("Parent") }, "14.12.4.1", "missing the required /Parent"},
		{"both DParts and Start", func(d *Document) { objDict(d, 9).Set("DParts", Array{Array{}}) }, "14.12.4.1", "both /DParts and /Start"},
		{"neither DParts nor Start", func(d *Document) { objDict(d, 9).Delete("Start"); objDict(d, 9).Delete("End") }, "14.12.4.1", "neither /DParts"},
		{"empty DParts", func(d *Document) { objDict(d, 8).Set("DParts", Array{}) }, "14.12.4.1", "non-empty array"},
		{"End before Start", func(d *Document) { objDict(d, 9).Set("Start", IndirectRef{Number: 4}); objDict(d, 9).Set("End", IndirectRef{Number: 3}) }, "14.12.4.1", "precedes /Start"},
		{"Start not a page", func(d *Document) { objDict(d, 9).Set("Start", IndirectRef{Number: 7}) }, "14.12.3", "/Start does not reference a page"},
		{"page uncovered / gap", func(d *Document) { objDict(d, 9).Set("End", IndirectRef{Number: 3}) }, "14.12.3", "not contiguous"},
		{"overlapping ranges", func(d *Document) { objDict(d, 10).Set("Start", IndirectRef{Number: 4}) }, "14.12.2", "more than one DPart leaf range"},
		{"wrong page back-ref", func(d *Document) { objDict(d, 3).Set("DPart", IndirectRef{Number: 10}) }, "14.12.3", "page /DPart does not reference"},
		{"NodeNameList wrong length", func(d *Document) { objDict(d, 7).Set("NodeNameList", Array{Name("Only")}) }, "14.12.4.1", "levels"},
		{"NodeNameList bad name", func(d *Document) { objDict(d, 7).Set("NodeNameList", Array{Name("1bad"), Name("Section")}) }, "14.12.4.1", "not a valid XML name"},
		{"DPM disallowed value", func(d *Document) {
			dpm := &Dictionary{}
			dpm.Set("Kind", Name("aName")) // a name value is not permitted
			objDict(d, 9).Set("DPM", dpm)
		}, "14.12.4.2", "not permitted"},
		{"multi-parent / cycle", func(d *Document) {
			// make the internal node reference leaf 9 twice
			objDict(d, 8).Set("DParts", Array{Array{IndirectRef{Number: 9}, IndirectRef{Number: 9}, IndirectRef{Number: 10}}})
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
	dpm := &Dictionary{}
	dpm.Set("RecipientName", String{Value: []byte("Jane Doe")})
	dpm.Set("Copies", Integer(3))
	dpm.Set("Duplex", Boolean(true))
	nested := &Dictionary{}
	nested.Set("Zip", String{Value: []byte("93407")})
	dpm.Set("Address", nested)
	dpm.Set("Tags", Array{String{Value: []byte("a")}, Integer(1), Real(2.5)})
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
