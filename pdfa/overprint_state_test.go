package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// overprintDoc is a PDF/A-2b page with content, whose resources name:
//
//   - /CS0, an ICCBased CMYK colour space (a four-component profile stream);
//   - /OPM1: overprint mode 1, overprinting off;
//   - /OPon: OP and op on, overprint mode 0;
//   - /All: OP and op on and overprint mode 1;
//   - /Stroke: OP on, op off, overprint mode 1;
//   - /Fm0, a form XObject whose content is form (with /CS0 in its own
//     resources), when form is not empty.
//
// extra entries are added to the page's /ColorSpace dictionary.
func overprintDoc(t *testing.T, content, form string, extra ...object.Entry) core.View {
	t.Helper()
	doc := mkPDFAViewT(t, PDFA2b)
	page := addTestPage(doc)
	icc := object.NewStream(dictWith("N", object.Integer(4)), nil)
	doc.Objects[50] = &object.IndirectObject{Number: 50, Value: icc}
	cs := func() *object.Dictionary {
		d := dictWith("CS0", object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 50}})
		for _, e := range extra {
			d.Set(e.Key, e.Value)
		}
		return d
	}
	gs := func(entries ...object.Entry) *object.Dictionary { return object.NewDictionary(entries...) }
	b := func(v bool) object.Boolean { return object.Boolean(v) }
	res := object.NewDictionary(
		object.Entry{Key: "ColorSpace", Value: cs()},
		object.Entry{Key: "ExtGState", Value: object.NewDictionary(
			object.Entry{Key: "OPM1", Value: gs(object.Entry{Key: "OPM", Value: object.Integer(1)})},
			object.Entry{Key: "OPon", Value: gs(object.Entry{Key: "OP", Value: b(true)}, object.Entry{Key: "op", Value: b(true)}, object.Entry{Key: "OPM", Value: object.Integer(0)})},
			object.Entry{Key: "All", Value: gs(object.Entry{Key: "OP", Value: b(true)}, object.Entry{Key: "op", Value: b(true)}, object.Entry{Key: "OPM", Value: object.Integer(1)})},
			object.Entry{Key: "Stroke", Value: gs(object.Entry{Key: "OP", Value: b(true)}, object.Entry{Key: "op", Value: b(false)}, object.Entry{Key: "OPM", Value: object.Integer(1)})},
		)},
	)
	if form != "" {
		fm := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
			object.Entry{Key: "Resources", Value: dictWith("ColorSpace", cs())},
		), []byte(form))
		doc.Objects[51] = &object.IndirectObject{Number: 51, Value: fm}
		res.Set("XObject", dictWith("Fm0", object.IndirectRef{Number: 51}))
	}
	doc.Objects[52] = &object.IndirectObject{Number: 52, Value: object.NewStream(&object.Dictionary{}, []byte(content))}
	page.Set("Resources", res)
	page.Set("Contents", object.IndirectRef{Number: 52})
	return doc
}

// TestOverprintModeIsJudgedAtThePaintingOperator: the ICCBased CMYK overprint
// rule (ISO 19005-2 6.2.4.2) is about the graphics state in effect when
// something is painted — overprint mode 1 and overprinting on for that
// operation, set by gs, restored by Q, and inherited by the forms the content
// draws. The rule used to OR every gs a page named, in any order and inside or
// outside q/Q, and never looked inside a form (audit 2026-09-22 C65).
func TestOverprintModeIsJudgedAtThePaintingOperator(t *testing.T) {
	const fill = "/CS0 cs 0 0 0 1 sc 0 0 10 10 re f"
	const stroke = "/CS0 CS 0 0 0 1 SC 0 0 10 10 re S"
	cases := []struct {
		name, content, form string
		extra               []object.Entry
		want                bool
	}{
		// The audit's case: OPM 1 lives only inside q/Q, and the fill that
		// overprints happens after Q, at OPM 0.
		{name: "OPM 1 restored by Q", content: "q /OPM1 gs Q /OPon gs " + fill},
		{name: "overprint state set after the fill", content: fill + " /All gs"},
		{name: "overprint state in effect at the fill", content: "/All gs " + fill, want: true},
		{name: "OPM 1 and overprint set by separate gs", content: "/OPon gs /OPM1 gs " + fill, want: true},
		{name: "fill with only stroking overprint", content: "/Stroke gs " + fill},
		{name: "stroke with stroking overprint", content: "/Stroke gs " + stroke, want: true},
		{name: "text shown in a fill mode", content: "/All gs /CS0 cs 0 0 0 1 sc BT (A) Tj ET", want: true},
		{name: "invisible text paints nothing", content: "/All gs /CS0 cs 0 0 0 1 sc BT 3 Tr (A) Tj ET"},
		// The form paints in its own ICCBased CMYK space, in the state it
		// inherits at Do.
		{name: "a form inherits the state at Do", content: "/All gs /Fm0 Do", form: fill, want: true},
		{name: "a form drawn after Q", content: "q /All gs Q /Fm0 Do", form: fill},
		{name: "a form never drawn", content: "/All gs", form: fill},
		// DeviceCMYK where DefaultCMYK is an ICCBased CMYK space paints in
		// that space (ISO 32000-2 8.6.5.6).
		{
			name: "DeviceCMYK standing for an ICCBased CMYK DefaultCMYK", content: "/All gs 0 0 0 1 k 0 0 10 10 re f",
			extra: []object.Entry{{Key: "DefaultCMYK", Value: object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 50}}}},
			want:  true,
		},
		{name: "DeviceCMYK with no DefaultCMYK", content: "/All gs 0 0 0 1 k 0 0 10 10 re f"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := overprintDoc(t, c.content, c.form, c.extra...)
			got := checkICCBasedUsageRules(doc, PDFA2b)
			if c.want && len(got) != 1 {
				t.Errorf("want one overprint finding, got %v", got)
			}
			if !c.want && len(got) != 0 {
				t.Errorf("want no overprint finding, got %v", got)
			}
		})
	}
}
