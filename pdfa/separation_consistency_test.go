package pdfa

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// spotFn is a tint transform for /Spot: C1 is [c 0 0 0].
func spotFn(c1 object.Object) *object.Dictionary {
	return object.NewDictionary(
		object.Entry{Key: "FunctionType", Value: object.Integer(2)},
		object.Entry{Key: "Domain", Value: object.Array{object.Integer(0), object.Integer(1)}},
		object.Entry{Key: "C0", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)}},
		object.Entry{Key: "C1", Value: c1},
		object.Entry{Key: "N", Value: object.Integer(1)},
	)
}

func spotC1(c float64) object.Array {
	return object.Array{object.Real(c), object.Integer(0), object.Integer(0), object.Integer(0)}
}

func spotSep(tint object.Object) object.Array {
	return object.Array{object.Name("Separation"), object.Name("Spot"), object.Name("DeviceCMYK"), tint}
}

// sepFormsDoc is a PDF/A-2b page whose content is content, with one form
// XObject per colour space in forms (named /Fm0, /Fm1, …), each filling in
// that space from its own resources as /CS0.
func sepFormsDoc(t *testing.T, content string, forms ...object.Object) core.View {
	t.Helper()
	doc := mkPDFAViewT(t, PDFA2b)
	page := addTestPage(doc)
	xobjs := &object.Dictionary{}
	for i, cs := range forms {
		num := 60 + i
		fm := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)}},
			object.Entry{Key: "Resources", Value: dictWith("ColorSpace", dictWith("CS0", cs))},
		), []byte("/CS0 cs 1 sc 0 0 1 1 re f"))
		doc.Objects[num] = &object.IndirectObject{Number: num, Value: fm}
		xobjs.Set(object.Name("Fm"+string(rune('0'+i))), object.IndirectRef{Number: num})
	}
	doc.Objects[59] = &object.IndirectObject{Number: 59, Value: object.NewStream(&object.Dictionary{}, []byte(content))}
	page.Set("Resources", dictWith("XObject", xobjs))
	page.Set("Contents", object.IndirectRef{Number: 59})
	return doc
}

func tintFindings(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		if strings.Contains(v.Message, "inconsistent tint transforms") {
			out = append(out, v.Error())
		}
	}
	return out
}

// TestSeparationConsistencyIsDeterministic is the audit's case: three forms
// define /Spot with three different tint transforms. The "first" definition
// the others were compared against came from ranging over a Go map, so one
// document gave several different reports; it must give one, every time
// (audit 2026-09-22 C66).
func TestSeparationConsistencyIsDeterministic(t *testing.T) {
	build := func() core.View {
		doc := sepFormsDoc(t, "/Fm0 Do /Fm1 Do /Fm2 Do",
			spotSep(object.IndirectRef{Number: 70}), spotSep(object.IndirectRef{Number: 71}), spotSep(object.IndirectRef{Number: 72}))
		for i := 0; i < 3; i++ {
			doc.Objects[70+i] = &object.IndirectObject{Number: 70 + i, Value: spotFn(spotC1(0.1 * float64(i+1)))}
		}
		return doc
	}
	seen := map[string]int{}
	for i := 0; i < 50; i++ {
		got := tintFindings(checkSeparationDeviceN(build(), PDFA2b))
		if len(got) == 0 {
			t.Fatal("three different tint transforms for /Spot were not reported")
		}
		seen[strings.Join(got, "\n")]++
	}
	if len(seen) != 1 {
		t.Errorf("50 validations of one document gave %d different reports: %v", len(seen), seen)
	}
}

// TestSeparationConsistencyReadsExecutedContent: only colour spaces the
// content uses define anything rendered. Forms nobody draws used to count.
func TestSeparationConsistencyReadsExecutedContent(t *testing.T) {
	build := func(content string) core.View {
		doc := sepFormsDoc(t, content, spotSep(object.IndirectRef{Number: 70}), spotSep(object.IndirectRef{Number: 71}))
		doc.Objects[70] = &object.IndirectObject{Number: 70, Value: spotFn(spotC1(0.1))}
		doc.Objects[71] = &object.IndirectObject{Number: 71, Value: spotFn(spotC1(0.9))}
		return doc
	}
	if got := tintFindings(checkSeparationDeviceN(build(""), PDFA2b)); len(got) != 0 {
		t.Errorf("two forms nothing draws were compared: %v", got)
	}
	if got := tintFindings(checkSeparationDeviceN(build("/Fm0 Do"), PDFA2b)); len(got) != 0 {
		t.Errorf("a drawn form was compared with one nothing draws: %v", got)
	}
	if got := tintFindings(checkSeparationDeviceN(build("/Fm0 Do /Fm1 Do"), PDFA2b)); len(got) != 1 {
		t.Errorf("two drawn forms with different tint transforms: got %v, want one finding", got)
	}
}

// TestSeparationTintTransformsCompareByContent: the rule is "the same tint
// transform", which two identical functions are however they are split into
// objects. object.Equal compared a nested reference by its number, and a
// tint transform written inline was not considered at all.
func TestSeparationTintTransformsCompareByContent(t *testing.T) {
	// Two objects, identical but that one writes C1 as an object of its own.
	doc := sepFormsDoc(t, "/Fm0 Do /Fm1 Do", spotSep(object.IndirectRef{Number: 70}), spotSep(object.IndirectRef{Number: 71}))
	doc.Objects[70] = &object.IndirectObject{Number: 70, Value: spotFn(spotC1(0.5))}
	doc.Objects[71] = &object.IndirectObject{Number: 71, Value: spotFn(object.IndirectRef{Number: 73})}
	doc.Objects[73] = &object.IndirectObject{Number: 73, Value: spotC1(0.5)}
	if got := tintFindings(checkSeparationDeviceN(doc, PDFA2b)); len(got) != 0 {
		t.Errorf("identical tint transforms in different objects were reported: %v", got)
	}

	// An inline tint transform that differs from an indirect one.
	doc = sepFormsDoc(t, "/Fm0 Do /Fm1 Do", spotSep(object.IndirectRef{Number: 70}), spotSep(spotFn(spotC1(0.9))))
	doc.Objects[70] = &object.IndirectObject{Number: 70, Value: spotFn(spotC1(0.5))}
	if got := tintFindings(checkSeparationDeviceN(doc, PDFA2b)); len(got) != 1 {
		t.Errorf("an inline tint transform differing from an indirect one: got %v, want one finding", got)
	}

	// A Separation reached as an Indexed space's base is used too.
	indexed := object.Array{object.Name("Indexed"), spotSep(spotFn(spotC1(0.9))), object.Integer(0), object.String{Value: []byte{0}}}
	doc = sepFormsDoc(t, "/Fm0 Do /Fm1 Do", spotSep(spotFn(spotC1(0.5))), indexed)
	if got := tintFindings(checkSeparationDeviceN(doc, PDFA2b)); len(got) != 1 {
		t.Errorf("a Separation under Indexed differing from another: got %v, want one finding", got)
	}
}
