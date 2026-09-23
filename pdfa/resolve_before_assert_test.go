package pdfa

import (
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// A rule input read without resolving it answers "absent" whenever the file
// writes it as an indirect reference, which ISO 32000 permits almost
// everywhere. For a requirement that is a false positive (the value is there,
// the rule cannot see it); for a prohibition it is an evasion (the value is
// there, the rule does not fire). Audit 2026-09-22 C36 found both kinds at
// twenty sites in this package, the previous audit having fixed the ones it
// named (C18).
//
// Each case here builds one document twice, writing one value directly and
// then indirectly, and requires the whole pipeline to give the same findings
// for both — and, so that the comparison is not between two empty lists, the
// direct form to give (or not give) the finding the case is about.
// internal/lint's TestValidatorsResolveBeforeAssert keeps the shape from
// coming back.

type indirectionCase struct {
	name  string
	level Level
	// build mutates a skeleton; wrap writes a value directly or indirectly.
	build func(v core.View, wrap func(object.Object) object.Object)
	// want is a message fragment the direct form must report; empty means the
	// direct form is conforming and must report nothing for the rule.
	want string
	// clean is a message fragment neither form may report.
	clean string
}

func indirectionCases() []indirectionCase {
	gs := func(entries ...object.Entry) func(core.View, func(object.Object) object.Object) {
		return func(v core.View, wrap func(object.Object) object.Object) {
			d := &object.Dictionary{}
			d.Set("Type", object.Name("ExtGState"))
			for _, e := range entries {
				d.Set(e.Key, wrap(e.Value))
			}
			addExtGStateToDoc(v, d)
		}
	}
	return []indirectionCase{
		{
			name: "metadata /Type", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				v.Objects[3].Value.(*object.Stream).Dict.Set("Type", wrap(object.Name("Metadata")))
			},
			clean: "/Type /Metadata",
		},
		{
			name: "metadata /Subtype", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				v.Objects[3].Value.(*object.Stream).Dict.Set("Subtype", wrap(object.Name("XML")))
			},
			clean: "/Subtype /XML",
		},
		{
			name: "ExtGState /TR2 /Default", level: PDFA2b,
			build: gs(object.Entry{Key: "TR2", Value: object.Name("Default")}),
			clean: "/TR2",
		},
		{
			name: "ExtGState /BM /Bogus", level: PDFA2b,
			build: gs(object.Entry{Key: "BM", Value: object.Name("Bogus")}),
			want:  "invalid blend mode /Bogus",
		},
		{
			name: "1b ExtGState /BM /Multiply", level: PDFA1b,
			build: gs(object.Entry{Key: "BM", Value: object.Name("Multiply")}),
			want:  "/BM must be /Normal or /Compatible",
		},
		{
			name: "1b ExtGState /ca 0.5", level: PDFA1b,
			build: gs(object.Entry{Key: "ca", Value: object.Real(0.5)}),
			want:  "/ca must be 1.0",
		},
		{
			name: "1b ExtGState /SMask /None", level: PDFA1b,
			build: gs(object.Entry{Key: "SMask", Value: object.Name("None")}),
			clean: "/SMask must not be used",
		},
		{
			name: "halftone type 6", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				ht := &object.Dictionary{}
				ht.Set("Type", object.Name("Halftone"))
				ht.Set("HalftoneType", wrap(object.Integer(6)))
				d := &object.Dictionary{}
				d.Set("HT", ht)
				addExtGStateToDoc(v, d)
			},
			want: "halftone type must be 1 or 5",
		},
		{
			name: "NeedAppearances true", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				af := &object.Dictionary{}
				af.Set("Fields", object.Array{})
				af.Set("NeedAppearances", wrap(object.Boolean(true)))
				v.Objects[1].Value.(*object.Dictionary).Set("AcroForm", af)
			},
			want: "NeedAppearances must be false",
		},
		{
			name: "/Filter /LZWDecode", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				s := object.NewStream(object.NewDictionary(object.Entry{Key: "Filter", Value: wrap(object.Name("LZWDecode"))}), []byte("x"))
				v.Objects[30] = &object.IndirectObject{Number: 30, Value: s}
			},
			want: "must not use /LZWDecode",
		},
		{
			name: "/Filter [/LZWDecode] element", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				s := object.NewStream(object.NewDictionary(object.Entry{Key: "Filter", Value: object.Array{wrap(object.Name("LZWDecode"))}}), []byte("x"))
				v.Objects[30] = &object.IndirectObject{Number: 30, Value: s}
			},
			want: "must not use /LZWDecode",
		},
		{
			name: "/Filter non-standard", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				s := object.NewStream(object.NewDictionary(object.Entry{Key: "Filter", Value: wrap(object.Name("Crypt2"))}), []byte("x"))
				v.Objects[30] = &object.IndirectObject{Number: 30, Value: s}
			},
			want: "non-standard filter /Crypt2",
		},
		{
			name: "output intent /S", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				v.Objects[4].Value.(*object.Dictionary).Set("S", wrap(object.Name("GTS_PDFA1")))
			},
			clean: "/S must be a name",
		},
		{
			name: "catalog /Version", level: PDFA4,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				v.Objects[1].Value.(*object.Dictionary).Set("Version", wrap(object.Name("1.7")))
			},
			want: "catalog /Version must match 2.N",
		},
		{
			name: "zero-area annotation /Rect", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				page := addTestPage(v)
				annot := &object.Dictionary{}
				annot.Set("Type", object.Name("Annot"))
				annot.Set("Subtype", object.Name("Text"))
				annot.Set("F", object.Integer(4))
				annot.Set("Rect", wrap(object.Array{object.Integer(10), object.Integer(10), object.Integer(10), object.Integer(10)}))
				v.Objects[31] = &object.IndirectObject{Number: 31, Value: annot}
				page.Set("Annots", object.Array{object.IndirectRef{Number: 31}})
			},
			clean: "must have /AP",
		},
		{
			name: "page box out of range", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				page := addTestPage(v)
				page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), wrap(object.Integer(20000)), object.Integer(792)})
			},
			want: "out of range [3, 14400]",
		},
		{
			name: "Separation colorant /None", level: PDFA2b,
			build: func(v core.View, wrap func(object.Object) object.Object) {
				cs := object.Array{wrap(object.Name("Separation")), wrap(object.Name("None")), object.Name("DeviceGray"), object.IndirectRef{Number: 33}}
				fn := &object.Dictionary{}
				fn.Set("FunctionType", object.Integer(2))
				fn.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
				fn.Set("N", object.Integer(1))
				v.Objects[33] = &object.IndirectObject{Number: 33, Value: fn}
				img := object.NewStream(object.NewDictionary(
					object.Entry{Key: "Type", Value: object.Name("XObject")},
					object.Entry{Key: "Subtype", Value: object.Name("Image")},
					object.Entry{Key: "ColorSpace", Value: cs},
				), nil)
				v.Objects[32] = &object.IndirectObject{Number: 32, Value: img}
				// Drawn on a page, so the image is part of the document the
				// rules judge (orphans are not; audit 2026-09-22 C83).
				addTestPage(v).Set("Resources", object.NewDictionary(object.Entry{Key: "XObject",
					Value: object.NewDictionary(object.Entry{Key: "Im0", Value: object.IndirectRef{Number: 32}})}))
			},
			want: "Separation colorant name /None is reserved",
		},
	}
}

func TestRuleInputsAreResolvedBeforeTheyAreRead(t *testing.T) {
	for _, tc := range indirectionCases() {
		t.Run(tc.name, func(t *testing.T) {
			run := func(indirectly bool) []string {
				v := mkPDFAViewT(t, tc.level)
				next := 100
				wrap := func(o object.Object) object.Object {
					if !indirectly {
						return o
					}
					next++
					return indirect(v.Objects, next, o)
				}
				tc.build(v, wrap)
				var out []string
				for _, e := range ValidateView(v, tc.level, nil) {
					out = append(out, e.Rule+" "+e.Message)
				}
				sort.Strings(out)
				return out
			}
			direct, viaRef := run(false), run(true)
			joined := strings.Join(direct, "\n")
			if tc.want != "" && !strings.Contains(joined, tc.want) {
				t.Fatalf("the direct form did not report %q, so the case tests nothing:\n%s", tc.want, joined)
			}
			if tc.clean != "" && strings.Contains(joined, tc.clean) {
				t.Fatalf("the direct form reported %q on a conforming value:\n%s", tc.clean, joined)
			}
			if strings.Join(viaRef, "\n") != joined {
				t.Errorf("findings differ when the value is written indirectly\n direct:\n  %s\n indirect:\n  %s",
					strings.Join(direct, "\n  "), strings.Join(viaRef, "\n  "))
			}
		})
	}
}
