package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// Rules that were escapable by writing one value as an indirect reference.
//
// ISO 32000 lets almost any value in a dictionary be an indirect reference, and
// says so where it does not. `/Subtype 9 0 R` naming `/Widget` is therefore a
// legal way to write a widget annotation. A check that reads it as
// `dict.Get("Subtype").(object.Name)` gets the empty name back, decides this is
// not a widget, and returns without looking — which is a rule that holds only
// for documents that do not try to avoid it.
//
// Every case below is a document that violates a rule and writes exactly one
// value indirectly. All of them passed before the sweep; that is the point of
// the test, and each was checked to fail against the unswept code rather than
// assumed to.

// indirect returns a reference to obj, registering it in objs under num.
func indirect(objs map[int]*object.IndirectObject, num int, obj object.Object) object.IndirectRef {
	objs[num] = &object.IndirectObject{Number: num, Value: obj}
	return object.IndirectRef{Number: num}
}

// TestAWidgetWrittenIndirectlyIsStillAWidget covers the two rules that turn on
// the annotation subtype: a widget shall not carry /A (6.4.1 / 6.6.1) and shall
// not carry /AA.
func TestAWidgetWrittenIndirectlyIsStillAWidget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		check func(doc core.View, level Level) []Violation
		want  string
	}{
		{"/A", "A", checkWidgetNoAction, "must not contain /A"},
		{"/AA", "AA", checkAnnotationAA, "/AA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, form := range []string{"direct", "indirect"} {
				objs := map[int]*object.IndirectObject{}
				// No /Type and no /Rect: IsAnnotation cannot classify this,
				// so the subtype is the only thing that can, which is what
				// makes the /AA case reach isWidgetOrField.
				annot := &object.Dictionary{}
				if form == "direct" {
					annot.Set("Subtype", object.Name("Widget"))
				} else {
					annot.Set("Subtype", indirect(objs, 9, object.Name("Widget")))
				}
				action := &object.Dictionary{}
				action.Set("S", object.Name("JavaScript"))
				annot.Set(object.Name(tc.key), action)
				objs[5] = &object.IndirectObject{Number: 5, Value: annot}

				trailer := object.Dictionary{}
				errs := tc.check(referenced(mkView(objs, &trailer), 5), PDFA2b)
				if !hasMessage(errs, tc.want) {
					t.Errorf("%s /Subtype: the rule did not fire: %v", form, errs)
				}
			}
		})
	}
}

// TestAForbiddenActionWrittenIndirectlyIsStillForbidden.
//
// The action type is /S, and a document that writes it indirectly kept every
// JavaScript, Launch and Movie action it liked.
func TestAForbiddenActionWrittenIndirectlyIsStillForbidden(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}
		action := &object.Dictionary{}
		action.Set("Type", object.Name("Action"))
		if form == "direct" {
			action.Set("S", object.Name("JavaScript"))
		} else {
			action.Set("S", indirect(objs, 9, object.Name("JavaScript")))
		}

		catalog := &object.Dictionary{}
		catalog.Set("Type", object.Name("Catalog"))
		catalog.Set("OpenAction", indirect(objs, 4, action))
		objs[1] = &object.IndirectObject{Number: 1, Value: catalog}
		trailer := object.Dictionary{}
		trailer.Set("Root", object.IndirectRef{Number: 1})

		errs := checkNoForbiddenActions(mkView(objs, &trailer), PDFA2b)
		if !hasMessage(errs, "JavaScript") {
			t.Errorf("%s /S: a forbidden action was not reported: %v", form, errs)
		}
	}
}

// TestAnImageWrittenIndirectlyIsStillAnImage covers /Alternates and
// /Interpolate, and writes /Interpolate itself indirectly in the second case —
// the value, not only the subtype.
func TestAnImageWrittenIndirectlyIsStillAnImage(t *testing.T) {
	build := func(form string) (map[int]*object.IndirectObject, *object.Stream) {
		objs := map[int]*object.IndirectObject{}
		img := &object.Stream{Dict: object.Dictionary{}}
		if form == "direct" {
			img.Dict.Set("Subtype", object.Name("Image"))
		} else {
			img.Dict.Set("Subtype", indirect(objs, 9, object.Name("Image")))
		}
		objs[5] = &object.IndirectObject{Number: 5, Value: img}
		return objs, img
	}

	for _, form := range []string{"direct", "indirect"} {
		objs, img := build(form)
		img.Dict.Set("Alternates", object.Array{})
		if errs := checkNoAlternateImages(referenced(mkView(objs, nil), 5), PDFA2b); !hasMessage(errs, "/Alternates") {
			t.Errorf("%s /Subtype: /Alternates was not reported: %v", form, errs)
		}

		objs, img = build(form)
		if form == "direct" {
			img.Dict.Set("Interpolate", object.Boolean(true))
		} else {
			img.Dict.Set("Interpolate", indirect(objs, 10, object.Boolean(true)))
		}
		if errs := checkInterpolate(referenced(mkView(objs, nil), 5), PDFA2b); !hasMessage(errs, "/Interpolate") {
			t.Errorf("%s: /Interpolate true was not reported: %v", form, errs)
		}
	}
}

// TestATransparencyGroupWrittenIndirectlyIsStillTransparency, which PDF/A-1b
// forbids outright.
func TestATransparencyGroupWrittenIndirectlyIsStillTransparency(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}
		group := &object.Dictionary{}
		if form == "direct" {
			group.Set("S", object.Name("Transparency"))
		} else {
			group.Set("S", indirect(objs, 9, object.Name("Transparency")))
		}

		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Group", indirect(objs, 4, group))
		pages := &object.Dictionary{}
		pages.Set("Type", object.Name("Pages"))
		pages.Set("Kids", object.Array{indirect(objs, 3, page)})
		pages.Set("Count", object.Integer(1))
		catalog := &object.Dictionary{}
		catalog.Set("Type", object.Name("Catalog"))
		catalog.Set("Pages", indirect(objs, 2, pages))
		objs[1] = &object.IndirectObject{Number: 1, Value: catalog}
		trailer := object.Dictionary{}
		trailer.Set("Root", object.IndirectRef{Number: 1})

		errs := checkNoTransparency(mkView(objs, &trailer), PDFA1b)
		if !hasMessage(errs, "Transparency") {
			t.Errorf("%s /S: a transparency group was not reported at PDF/A-1b: %v", form, errs)
		}
	}
}

// TestAType3FontWrittenIndirectlyIsStillExemptFromEmbedding.
//
// This one fails the other way round, and is worth having for it. A Type 3
// font draws its glyphs with content streams, so it carries no program and the
// embedding rule does not apply to it. Reading /Subtype without resolving gave
// the empty name, which is not "Type3", so a Type 3 font written the legal
// indirect way was reported for a /FontDescriptor it is not required to have.
func TestAType3FontWrittenIndirectlyIsStillExemptFromEmbedding(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}
		font := &object.Dictionary{}
		font.Set("Type", object.Name("Font"))
		font.Set("FontMatrix", object.Array{})
		font.Set("CharProcs", &object.Dictionary{})
		if form == "direct" {
			font.Set("Subtype", object.Name("Type3"))
		} else {
			font.Set("Subtype", indirect(objs, 9, object.Name("Type3")))
		}

		errs := checkOneFontEmbedded(mkView(objs, nil), font, 5, PDFA2b)
		if len(errs) != 0 {
			t.Errorf("%s /Subtype: a Type 3 font, which has no program to embed, "+
				"was reported: %v", form, errs)
		}
	}
}

// TestAnAnnotationWrittenIndirectlyIsStillAnAnnotation is the gate in front of
// all of the above.
//
// core.View.IsAnnotation decides which objects the annotation rules look at,
// and it reads /Type. An indirect /Type made the object invisible to every one
// of them at once, so the subtype fixes above would not have been enough.
func TestAnAnnotationWrittenIndirectlyIsStillAnAnnotation(t *testing.T) {
	for _, form := range []string{"direct", "indirect"} {
		objs := map[int]*object.IndirectObject{}
		annot := &object.Dictionary{}
		annot.Set("Subtype", object.Name("Text"))
		// No /F at all, which 6.3.1 requires and which is what gets reported
		// once the object is seen as an annotation in the first place.
		if form == "direct" {
			annot.Set("Type", object.Name("Annot"))
		} else {
			annot.Set("Type", indirect(objs, 9, object.Name("Annot")))
		}
		objs[5] = &object.IndirectObject{Number: 5, Value: annot}

		errs := checkAnnotationFlags(referenced(mkView(objs, nil), 5), PDFA2b)
		if !hasMessage(errs, "/F") {
			t.Errorf("%s /Type: the annotation was not looked at at all: %v", form, errs)
		}
	}
}
