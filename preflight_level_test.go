package pdf0

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/internal/hostile"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// Repair(level) honours the level, and reaches every /AA the validator checks
// (audit 2026-09-22 C93).

// aaDoc is a conforming PDF/A document at level, then given additional actions
// everywhere one can be: the catalog, a page, a direct and an indirect
// annotation, and a form field reached through an indirect /Kids array. Each
// /AA holds one event PDF/A-4 forbids and, where there is one, one it allows.
type aaDoc struct {
	d                       *Document
	catalog, page           *object.Dictionary
	direct, indirect, field *object.Dictionary
}

func goTo(page object.IndirectRef) *object.Dictionary {
	return object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Action")},
		object.Entry{Key: "S", Value: object.Name("GoTo")},
		object.Entry{Key: "D", Value: object.Array{page, object.Name("Fit")}},
	)
}

func newAADoc(t *testing.T, level pdfa.Level) *aaDoc {
	t.Helper()
	x := &aaDoc{d: mustPDFADoc(t, level)}
	var b content.Builder
	b.Rect(0, 0, 1, 1).EndPath()
	ref, err := x.d.AddPage(Page{Width: 200, Height: 200, Content: &b})
	if err != nil {
		t.Fatal(err)
	}
	x.catalog = x.d.ResolveDict(x.d.Trailer.Get("Root"))
	x.page = x.d.ResolveDict(ref)
	aa := func(events ...object.Name) *object.Dictionary {
		dict := object.NewDictionary()
		for _, e := range events {
			dict.Set(e, goTo(ref))
		}
		return dict
	}
	// Forbidden at PDF/A-4 (6.6.3): WC, O, PO, PV; allowed: E, D, K.
	x.catalog.Set("AA", aa("WC"))
	x.page.Set("AA", aa("O"))

	appearance := func() object.IndirectRef {
		st := object.NewStream(object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("XObject")},
			object.Entry{Key: "Subtype", Value: object.Name("Form")},
			object.Entry{Key: "BBox", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
		), nil)
		return x.d.Add(st)
	}
	annot := func(subtype object.Name, aaDict *object.Dictionary) *object.Dictionary {
		a := object.NewDictionary(
			object.Entry{Key: "Type", Value: object.Name("Annot")},
			object.Entry{Key: "Subtype", Value: subtype},
			object.Entry{Key: "Rect", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)}},
			object.Entry{Key: "F", Value: object.Integer(4)},
			object.Entry{Key: "AA", Value: aaDict},
		)
		if subtype != "Link" {
			a.Set("AP", object.NewDictionary(object.Entry{Key: "N", Value: appearance()}))
		}
		return a
	}
	x.direct = annot("Link", aa("E", "PO"))
	x.direct.Set("Dest", object.Array{ref, object.Name("Fit")})
	x.indirect = annot("Link", aa("D", "PV"))
	x.indirect.Set("Dest", object.Array{ref, object.Name("Fit")})

	// A text field whose widget is a separate annotation, under a parent field
	// that is itself reached through an indirect /Kids array.
	widget := annot("Widget", object.NewDictionary())
	widget.Delete("AA")
	widget.Set("P", ref)
	widgetRef := x.d.Add(widget)
	x.field = object.NewDictionary(
		object.Entry{Key: "FT", Value: object.Name("Tx")},
		object.Entry{Key: "T", Value: object.String{Value: []byte("name")}},
		object.Entry{Key: "Kids", Value: object.Array{widgetRef}},
		object.Entry{Key: "AA", Value: aa("K", "PC")},
	)
	fieldRef := x.d.Add(x.field)
	widget.Set("Parent", fieldRef)
	parent := object.NewDictionary(
		object.Entry{Key: "T", Value: object.String{Value: []byte("form")}},
		object.Entry{Key: "Kids", Value: x.d.Add(object.Array{fieldRef})},
	)
	x.field.Set("Parent", x.d.Add(parent))
	x.catalog.Set("AcroForm", object.NewDictionary(
		object.Entry{Key: "Fields", Value: object.Array{x.field.Get("Parent")}},
	))
	x.page.Set("Annots", x.d.Add(object.Array{x.direct, x.d.Add(x.indirect), widgetRef}))
	return x
}

func aaViolations(v []pdfa.Violation) []string {
	var out []string
	for _, e := range v {
		if strings.Contains(e.Message, "/AA") || strings.Contains(e.Message, "additional") || strings.Contains(e.Message, "trigger") {
			out = append(out, e.Error())
		}
	}
	return out
}

func writeAndValidate(t *testing.T, d *Document, level pdfa.Level) []pdfa.Violation {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatalf("write: %v", err)
	}
	rd, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	return ValidatePDFA(rd, level)
}

// TestRepairValidatesCleanAtItsLevel is the claim Repair makes: what it
// removes is what the level forbids, so afterwards the level's validator finds
// nothing — here, on a document that conforms but for its additional actions.
func TestRepairValidatesCleanAtItsLevel(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4, pdfa.PDFA4F} {
		t.Run(level.String(), func(t *testing.T) {
			x := newAADoc(t, level)
			if len(aaViolations(writeAndValidate(t, x.d, level))) == 0 {
				t.Fatal("the fixture has no /AA violation before repair; it tests nothing")
			}
			actions, err := x.d.Repair(level)
			if err != nil {
				t.Fatalf("Repair: %v", err)
			}
			if len(actions) == 0 {
				t.Error("Repair reported no actions")
			}
			if v := writeAndValidate(t, x.d, level); len(v) != 0 {
				for _, e := range v {
					t.Errorf("after repair: %s", e.Error())
				}
			}
		})
	}
}

// TestRepairKeepsWhatPDFA4Permits pins the level dependence. PDF/A-4 forbids
// only some trigger events (6.6.3) — document lifecycle, page open/close, and
// annotation page-visibility events — and keeps interaction events like E, D
// and a field's K; Repair at PDF/A-4 removes those events and nothing else,
// where at the earlier parts every /AA goes.
func TestRepairKeepsWhatPDFA4Permits(t *testing.T) {
	x := newAADoc(t, pdfa.PDFA4)
	if _, err := x.d.Repair(pdfa.PDFA4); err != nil {
		t.Fatal(err)
	}
	if x.catalog.Has("AA") || x.page.Has("AA") {
		t.Error("an /AA holding only forbidden events was kept (it should be removed once empty)")
	}
	for name, c := range map[string]struct {
		owner     *object.Dictionary
		kept, cut object.Name
	}{
		"direct annotation":   {x.direct, "E", "PO"},
		"indirect annotation": {x.indirect, "D", "PV"},
		"form field":          {x.field, "K", "PC"},
	} {
		aa := x.d.ResolveDict(c.owner.Get("AA"))
		if aa == nil {
			t.Errorf("%s: its /AA was removed, although %v is permitted at PDF/A-4", name, c.kept)
			continue
		}
		if !aa.Has(c.kept) {
			t.Errorf("%s: the permitted event %v was removed", name, c.kept)
		}
		if aa.Has(c.cut) {
			t.Errorf("%s: the forbidden event %v was kept", name, c.cut)
		}
	}

	y := newAADoc(t, pdfa.PDFA2b)
	if _, err := y.d.Repair(pdfa.PDFA2b); err != nil {
		t.Fatal(err)
	}
	for name, owner := range map[string]*object.Dictionary{
		"catalog": y.catalog, "page": y.page, "direct annotation": y.direct,
		"indirect annotation": y.indirect, "form field": y.field,
	} {
		if owner.Has("AA") {
			t.Errorf("PDF/A-2b: the %s kept its /AA", name)
		}
	}
}

// TestRepairRefusesWhatItCannotRepair pins the two refusals: a level that is
// not one, and a document still encrypted — adding an /ID to it would change
// the key its content is encrypted under.
func TestRepairRefusesWhatItCannotRepair(t *testing.T) {
	d := mustPDFADoc(t, pdfa.PDFA2b)
	if _, err := d.Repair(pdfa.Level(99)); err == nil {
		t.Error("Repair accepted an unknown level")
	}
	if _, err := (*Document)(nil).Repair(pdfa.PDFA2b); err == nil {
		t.Error("Repair accepted a nil document")
	}
	locked := mustPDFADoc(t, pdfa.PDFA2b)
	locked.Encrypted = true
	locked.lockReason = errNilDocument // any reason: Locked reports it
	locked.Trailer.Delete("ID")
	before := docSnapshot(t, locked)
	if _, err := locked.Repair(pdfa.PDFA2b); err == nil {
		t.Error("Repair accepted a Locked document")
	}
	if docSnapshot(t, locked) != before {
		t.Error("the refused repair changed the document")
	}
}

// TestRepairFieldWalkEndsOnSharedAndCyclicKids pins the bound on the field
// walk. A read document's field tree can list one node twice at every level —
// a diamond whose paths double with each level — or list its own ancestor;
// each field is walked once, so either ends at once.
func TestRepairFieldWalkEndsOnSharedAndCyclicKids(t *testing.T) {
	hostile.Run(t, hostile.Limits{MaxRSS: 256 << 20, Timeout: 30 * time.Second}, func(t *testing.T) {
		d := mustPDFADoc(t, pdfa.PDFA2b)
		// 60 levels, each field listing the next one twice.
		next := d.Add(object.NewDictionary(object.Entry{Key: "T", Value: object.String{Value: []byte("leaf")}},
			object.Entry{Key: "AA", Value: object.NewDictionary()}))
		for i := 0; i < 60; i++ {
			next = d.Add(object.NewDictionary(object.Entry{Key: "Kids", Value: object.Array{next, next}}))
		}
		// And one that lists itself.
		self := object.NewDictionary()
		selfRef := d.Add(self)
		self.Set("Kids", object.Array{selfRef})
		d.ResolveDict(d.Trailer.Get("Root")).Set("AcroForm", object.NewDictionary(
			object.Entry{Key: "Fields", Value: object.Array{next, selfRef}}))
		actions, err := d.Repair(pdfa.PDFA2b)
		if err != nil {
			t.Fatal(err)
		}
		removed := 0
		for _, a := range actions {
			if strings.Contains(a.Description, "form field") {
				removed++
			}
		}
		if removed != 1 {
			t.Errorf("removed a form field /AA %d times, want once: %v", removed, actions)
		}
	})
}
