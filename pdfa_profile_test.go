package pdf0

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// The target profile, through the public API (audit 2026-09-22 C33, C34, C64,
// C139). Each test states what a caller asks for, and what the validator must
// hold the document to.

// profileBytes writes d and reads it back, for the byte-level rules.
func profileBytes(t *testing.T, d *Document) (*Document, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := d.Write(&buf); err != nil {
		t.Fatal(err)
	}
	r, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return r, buf.Bytes()
}

// attachPDF embeds data as an application/pdf associated file.
func attachPDF(t *testing.T, d *Document, name string, data []byte) {
	t.Helper()
	fs := attach(t, d, name, data)
	efDict := d.ResolveDict(d.ResolveDict(fs).Get("EF"))
	ef, _ := d.Resolve(efDict.Get("F")).(*object.Stream)
	ef.Dict.Set("Subtype", object.Name("application/pdf"))
}

func hasViolationMessage(vs []pdfa.Violation, sub string) bool {
	for _, v := range vs {
		if strings.Contains(v.Message, sub) {
			return true
		}
	}
	return false
}

// TestLevelUIsALevel (C33). PDF/A-2u and -3u validate clean at their own
// level and at the level their declaration maps to, a 2u or 2a file satisfies
// a 2b target (Level U and Level A include Level B), and Save writes a 2u
// document instead of refusing it — which is what read-modify-Save of a 3u
// Factur-X invoice needs.
func TestLevelUIsALevel(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA2u, pdfa.PDFA3u} {
		r, _ := profileBytes(t, mustPDFADoc(t, level))
		if vs := ValidatePDFA(r, level); len(vs) != 0 {
			t.Errorf("a %s skeleton at %s: %v", level, level, vs)
		}
		declared, ok := r.Conformance()
		if !ok || declared != level {
			t.Fatalf("a %s document declares (%v, %v)", level, declared, ok)
		}
		if vs := ValidatePDFA(r, declared); len(vs) != 0 {
			t.Errorf("a %s skeleton at its declared level: %v", level, vs)
		}
		var buf bytes.Buffer
		if err := mustPDFADoc(t, level).Save(&buf); err != nil {
			t.Errorf("Save refused a conforming %s document: %v", level, err)
		}
	}
	for _, tc := range []struct{ doc, target pdfa.Level }{
		{pdfa.PDFA2u, pdfa.PDFA2b}, {pdfa.PDFA2a, pdfa.PDFA2b}, {pdfa.PDFA2a, pdfa.PDFA2u},
		{pdfa.PDFA3u, pdfa.PDFA3b}, {pdfa.PDFA1a, pdfa.PDFA1b},
	} {
		r, _ := profileBytes(t, mustPDFADoc(t, tc.doc))
		if vs := ValidatePDFA(r, tc.target); len(vs) != 0 {
			t.Errorf("a %s document at %s: %v", tc.doc, tc.target, vs)
		}
	}
	// And not the other way: a 2b file is not a 2u file.
	r, _ := profileBytes(t, mustPDFADoc(t, pdfa.PDFA2b))
	if vs := ValidatePDFA(r, pdfa.PDFA2u); !hasViolationMessage(vs, `accepts "U" or "A"`) {
		t.Errorf("a 2b document at PDF/A-2u was not reported: %v", vs)
	}
}

// TestEmbeddedFilesAreValidatedAtTheirOwnLevel (C34). A plain PDF/A-4 file
// may embed any PDF/A file; each is validated against the level it declares,
// through the same mapping as everything else, so a conforming a or u file is
// not reported for its letter.
func TestEmbeddedFilesAreValidatedAtTheirOwnLevel(t *testing.T) {
	for _, inner := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA2u, pdfa.PDFA2a, pdfa.PDFA3u, pdfa.PDFA1a, pdfa.PDFA4F} {
		_, innerBytes := profileBytes(t, mustPDFADoc(t, inner))
		outer := mustPDFADoc(t, pdfa.PDFA4)
		attachPDF(t, outer, "inner.pdf", innerBytes)
		r, _ := profileBytes(t, outer)
		if vs := ValidatePDFA(r, pdfa.PDFA4); len(vs) != 0 {
			t.Errorf("a PDF/A-4 file embedding a conforming %s file: %v", inner, vs)
		}
	}
	// The control: an embedded file that declares a level it does not meet is
	// still reported.
	bad := mustPDFADoc(t, pdfa.PDFA2u)
	af := &object.Dictionary{}
	af.Set("Fields", object.Array{})
	af.Set("NeedAppearances", object.Boolean(true))
	bad.ResolveDict(bad.Trailer.Get("Root")).Set("AcroForm", af)
	_, badBytes := profileBytes(t, bad)
	outer := mustPDFADoc(t, pdfa.PDFA4)
	attachPDF(t, outer, "bad.pdf", badBytes)
	r, _ := profileBytes(t, outer)
	if vs := ValidatePDFA(r, pdfa.PDFA4); !hasViolationMessage(vs, "an embedded PDF file is not compliant") {
		t.Errorf("a non-conforming embedded 2u file was not reported: %v", vs)
	}
}

// TestTheVariantIsTheTargets (C64). A PDF/A-4f or -4e target applies its
// variant's requirements to the document whatever it declares — through the
// public entry point, where the run used to flatten the level to PDF/A-4
// before any check saw it.
func TestTheVariantIsTheTargets(t *testing.T) {
	r, _ := profileBytes(t, mustPDFADoc(t, pdfa.PDFA4))
	vs := ValidatePDFA(r, pdfa.PDFA4F)
	if !hasViolationMessage(vs, "must contain an /EmbeddedFiles key") {
		t.Errorf("a plain PDF/A-4 file at PDF/A-4f was not held to the attachment requirement: %v", vs)
	}
	if !hasViolationMessage(vs, "declares no pdfaid:conformance") {
		t.Errorf("a plain PDF/A-4 file at PDF/A-4f was not reported for its declaration: %v", vs)
	}
	for _, v := range vs {
		if v.Level != pdfa.PDFA4F {
			t.Errorf("finding reported at %v, want PDF/A-4f: %v", v.Level, v)
		}
	}

	// A 3D stream in a format 4e does not name, in a file that declares
	// nothing, at PDF/A-4e. The artwork is a 3D annotation's, on a page: a
	// rule judges what the document reaches, and an orphan stream is not
	// part of it (audit 2026-09-22 C83).
	d := mustPDFADoc(t, pdfa.PDFA4)
	artwork := d.Add(object.NewStream(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("3D")},
		object.Entry{Key: "Subtype", Value: object.Name("STL")},
	), []byte("solid")))
	var drawing content.Builder
	drawing.Rect(0, 0, 1, 1).Fill()
	pageRef, err := d.AddPage(Page{Width: 100, Height: 100, Content: &drawing})
	if err != nil {
		t.Fatal(err)
	}
	annot := d.Add(object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Annot")},
		object.Entry{Key: "Subtype", Value: object.Name("3D")},
		object.Entry{Key: "Rect", Value: object.Array{object.Integer(0), object.Integer(0), object.Integer(0), object.Integer(0)}},
		object.Entry{Key: "F", Value: object.Integer(4)},
		object.Entry{Key: "3DD", Value: artwork},
	))
	d.ResolveDict(pageRef).Set("Annots", object.Array{annot})
	r, _ = profileBytes(t, d)
	if vs := ValidatePDFA(r, pdfa.PDFA4E); !hasViolationMessage(vs, "/STL; it must be /U3D or /PRC") {
		t.Errorf("PDF/A-4e did not apply its artwork rule to a file declaring nothing: %v", vs)
	}

	// And the other way: a file that declares F, validated as plain PDF/A-4,
	// gets no 4f relaxation — its non-PDF attachment is reported — and the
	// identification rule says why.
	f := mustPDFADoc(t, pdfa.PDFA4F)
	attach(t, f, "notes.txt", []byte("notes"))
	r, _ = profileBytes(t, f)
	if vs := ValidatePDFA(r, pdfa.PDFA4F); len(vs) != 0 {
		t.Errorf("a conforming 4f file at PDF/A-4f: %v", vs)
	}
	vs = ValidatePDFA(r, pdfa.PDFA4)
	if !hasViolationMessage(vs, "non-PDF type not permitted") || !hasViolationMessage(vs, `pdfaid:conformance is "F"`) {
		t.Errorf("a 4f file at plain PDF/A-4: %v", vs)
	}
}

// TestLevelsThatNameNoProfileAreRefused (C139). An integer that names no
// level is one checker finding, not a run of the part-agnostic rules
// reported as a PDF/A result; LevelDeclared — the zero value — takes the
// target from the document; and a builder refuses both.
func TestLevelsThatNameNoProfileAreRefused(t *testing.T) {
	r, _ := profileBytes(t, mustPDFADoc(t, pdfa.PDFA2b))
	for _, bad := range []pdfa.Level{pdfa.Level(99), pdfa.Level(-1)} {
		vs := ValidatePDFA(r, bad)
		if len(vs) != 1 || !IsCheckerFinding(vs[0]) || !strings.Contains(vs[0].Message, "names no PDF/A level") {
			t.Errorf("%v: want one checker finding, got %v", bad, vs)
		}
		if _, err := NewPDFADocument(bad); err == nil {
			t.Errorf("NewPDFADocument(%v) built a document", bad)
		}
	}
	if _, err := NewPDFADocumentWith(PDFAOptions{}); err == nil {
		t.Error("the zero PDFAOptions built a document; it names no level")
	}

	// The zero Level is LevelDeclared: the document's own claim.
	var unset pdfa.Level
	if unset != pdfa.LevelDeclared {
		t.Fatalf("the zero Level is %v, want LevelDeclared", unset)
	}
	for _, level := range pdfa.Levels() {
		r, _ := profileBytes(t, mustPDFADoc(t, level))
		if vs := ValidatePDFA(r, unset); len(vs) != 0 {
			t.Errorf("a %s document validated as declared: %v", level, vs)
		}
	}
	// A declared 2u document with a violation is reported at 2u.
	d := mustPDFADoc(t, pdfa.PDFA2u)
	af := &object.Dictionary{}
	af.Set("NeedAppearances", object.Boolean(true))
	d.ResolveDict(d.Trailer.Get("Root")).Set("AcroForm", af)
	r, _ = profileBytes(t, d)
	vs := ValidatePDFA(r, pdfa.LevelDeclared)
	if len(vs) == 0 || vs[0].Level != pdfa.PDFA2u {
		t.Errorf("a declared 2u document's findings: %v", vs)
	}
	// A document that declares nothing is not validated against anything.
	plain := NewDocument()
	r, _ = profileBytes(t, plain)
	if vs := ValidatePDFA(r, pdfa.LevelDeclared); len(vs) != 1 || !IsCheckerFinding(vs[0]) {
		t.Errorf("a document declaring no level, as declared: %v", vs)
	}

	// PDF/X likewise refuses an integer that names no level.
	if vs := ValidatePDFX(r, pdfx.Level(99)); len(vs) != 1 || !IsCheckerFinding(vs[0]) {
		t.Errorf("pdfx.Level(99): want one checker finding, got %v", vs)
	}
}

// TestEmbeddedFilesAreFollowedDown (C137). A PDF/A-4 file embedding a PDF/A-4
// file that embeds something that is not a PDF is not conforming, however
// deep the offender sits: the embedded-PDF/A rule validates each embedded
// document with the same rule, file-type requirement included. Below
// pdfa.MaxEmbeddedDepth the rule stops and says so with a checker finding,
// rather than passing what it did not look at.
func TestEmbeddedFilesAreFollowedDown(t *testing.T) {
	// nest wraps inner in n PDF/A-4 documents, each embedding the next.
	nest := func(inner []byte, n int) []byte {
		for i := 0; i < n; i++ {
			d := mustPDFADoc(t, pdfa.PDFA4)
			attachPDF(t, d, "inner.pdf", inner)
			_, inner = profileBytes(t, d)
		}
		return inner
	}
	validate := func(data []byte) []pdfa.Violation {
		r, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		return ValidatePDFA(r, pdfa.PDFA4)
	}
	leaf := mustPDFADoc(t, pdfa.PDFA4)
	attach(t, leaf, "notes.txt", []byte("not a PDF"))
	_, leafBytes := profileBytes(t, leaf)

	// Two levels down: the non-PDF attachment makes the level-1 file
	// non-conforming, which the top-level file reports.
	if vs := validate(nest(leafBytes, 2)); !hasViolationMessage(vs, "an embedded PDF file is not compliant") {
		t.Errorf("a non-PDF attachment two levels down was not reported: %v", vs)
	}

	// A conforming chain deeper than the cap: not reported against the file,
	// and not passed either.
	_, cleanBytes := profileBytes(t, mustPDFADoc(t, pdfa.PDFA4))
	vs := validate(nest(cleanBytes, pdfa.MaxEmbeddedDepth+1))
	if len(vs) == 0 {
		t.Fatal("a chain deeper than the cap validated clean")
	}
	for _, v := range vs {
		if !IsCheckerFinding(v) {
			t.Errorf("a conforming chain deeper than the cap was reported as non-conforming: %v", v)
		}
	}
	// And at the cap exactly, nothing is withheld.
	if vs := validate(nest(cleanBytes, pdfa.MaxEmbeddedDepth)); len(vs) != 0 {
		t.Errorf("a conforming chain %d deep: %v", pdfa.MaxEmbeddedDepth, vs)
	}
}

// TestEmbeddedValidationSharesOneBudget: the documents validated across the
// whole embedding tree are counted together, so fan-out at each level cannot
// multiply the work past maxEmbeddedPDFADocs. Past it, the verdict is
// withheld and the run is incomplete, never "not compliant".
func TestEmbeddedValidationSharesOneBudget(t *testing.T) {
	_, cleanBytes := profileBytes(t, mustPDFADoc(t, pdfa.PDFA4))
	d := mustPDFADoc(t, pdfa.PDFA4)
	for i := 0; i < maxEmbeddedPDFADocs+1; i++ {
		attachPDF(t, d, "inner"+strings.Repeat("x", i)+".pdf", cleanBytes)
	}
	r, _ := profileBytes(t, d)
	vs := ValidatePDFA(r, pdfa.PDFA4)
	if len(vs) == 0 {
		t.Fatal("the embedded files past the budget were passed without a checker finding")
	}
	for _, v := range vs {
		if !IsCheckerFinding(v) {
			t.Errorf("a conforming embedded file past the budget was reported: %v", v)
		}
	}
}
