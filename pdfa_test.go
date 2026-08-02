package pdf0

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type corpusFile struct {
	path   string
	rel    string
	isPass bool
}

func TestValidatePDFA_NoEncrypt(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	doc.Trailer.Set("Encrypt", &object.Dictionary{})

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.1.3") {
		t.Error("expected 6.1.3 error for /Encrypt in trailer")
	}
}

func TestValidatePDFA_FileID(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	doc.Trailer.Delete("ID")

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.1.3") {
		t.Error("expected 6.1.3 error for missing /ID")
	}
}

func TestValidatePDFA_Header(t *testing.T) {
	tests := []struct {
		level   pdfa.Level
		version string
		wantErr bool
	}{
		{pdfa.PDFA1b, "1.4", false},
		{pdfa.PDFA1b, "1.7", false},
		{pdfa.PDFA1b, "2.0", false},
		{pdfa.PDFA2b, "1.4", false},
		{pdfa.PDFA2b, "1.5", false},
		{pdfa.PDFA2b, "1.7", false},
		{pdfa.PDFA2b, "2.0", true},
		{pdfa.PDFA3b, "1.7", false},
		{pdfa.PDFA3b, "2.0", true},
		{pdfa.PDFA4, "2.0", false},
		{pdfa.PDFA4, "1.7", true},
	}

	for _, tt := range tests {
		t.Run(tt.level.String()+"/"+tt.version, func(t *testing.T) {
			doc := NewPDFADocument(tt.level)
			doc.Version = tt.version

			errs := filterRule(ValidatePDFA(doc, tt.level), "6.1.2")
			if tt.wantErr && len(errs) == 0 {
				t.Error("expected version error")
			}
			if !tt.wantErr && len(errs) > 0 {
				t.Errorf("unexpected version error: %v", errs[0])
			}
		})
	}
}

func TestValidatePDFA_TrailerInfo(t *testing.T) {
	t.Run("Info without PieceInfo", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		infoDict := &object.Dictionary{}
		infoDict.Set("ModDate", object.String{Value: []byte("D:20240101")})
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", object.IndirectRef{Number: 20})

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.1.3") {
			t.Error("expected 6.1.3 error for Info without PieceInfo")
		}
	})

	t.Run("Info with non-ModDate key", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("PieceInfo", &object.Dictionary{})
		infoDict := &object.Dictionary{}
		infoDict.Set("Title", object.String{Value: []byte("Test")})
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", object.IndirectRef{Number: 20})

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.1.3") {
			t.Error("expected 6.1.3 error for Info with non-ModDate key")
		}
	})

	t.Run("Info with only ModDate and PieceInfo", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("PieceInfo", &object.Dictionary{})
		infoDict := &object.Dictionary{}
		infoDict.Set("ModDate", object.String{Value: []byte("D:20240101")})
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", object.IndirectRef{Number: 20})

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.1.3")
		// Should not have a trailer info error (may have others for Encrypt/ID)
		for _, e := range errs {
			if strings.Contains(e.Message, "Info") || strings.Contains(e.Message, "PieceInfo") || strings.Contains(e.Message, "ModDate") {
				t.Errorf("unexpected info-related error: %v", e)
			}
		}
	})
}

func TestValidatePDFA_MetadataStream(t *testing.T) {
	t.Run("missing metadata", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Delete("Metadata")

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.7.2") {
			t.Error("expected 6.7.2 error for missing metadata")
		}
	})

	t.Run("metadata with filter", func(t *testing.T) {
		// A Filter on the metadata stream is forbidden only in PDF/A-1
		// (ISO 19005-1 6.7.2); PDF/A-2/3/4 permit a permitted filter.
		filterErr := func(level pdfa.Level) bool {
			doc := NewPDFADocument(level)
			cat := doc.ResolveDict(doc.Trailer.Get("Root"))
			ms := doc.Resolve(cat.Get("Metadata")).(*object.Stream)
			ms.Dict.Set("Filter", object.Name("FlateDecode"))
			for _, e := range ValidatePDFA(doc, level) {
				if strings.Contains(e.Message, "must not have /Filter") {
					return true
				}
			}
			return false
		}
		if !filterErr(pdfa.PDFA1b) {
			t.Error("PDF/A-1b: expected a metadata /Filter error")
		}
		if filterErr(pdfa.PDFA4) {
			t.Error("PDF/A-4: a metadata /Filter must be permitted")
		}
	})
}

func TestValidatePDFA_OutputIntents(t *testing.T) {
	t.Run("missing output intents OK for all levels", func(t *testing.T) {
		for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
			doc := NewPDFADocument(level)
			catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
			catalog.Delete("OutputIntents")

			errs := filterRule(ValidatePDFA(doc, level), "6.2.3")
			if len(errs) > 0 {
				t.Errorf("%s should not require OutputIntents when absent, got: %v", level, errs[0])
			}
		}
	})

	t.Run("empty OutputIntents OK", func(t *testing.T) {
		for _, level := range []pdfa.Level{pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
			doc := NewPDFADocument(level)
			catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
			catalog.Set("OutputIntents", object.Array{})

			errs := filterRule(ValidatePDFA(doc, level), "6.2.3")
			if len(errs) > 0 {
				t.Errorf("%s should allow empty OutputIntents array, got: %v", level, errs[0])
			}
		}
	})

	t.Run("validates OutputIntents structure when present", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		// Set OutputIntents to array with invalid entry
		badOI := &object.Dictionary{}
		catalog.Set("OutputIntents", object.Array{badOI})

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.2.3")
		if len(errs) == 0 {
			t.Error("expected 6.2.3 error for OutputIntent without /S")
		}
	})
}

func TestValidatePDFA_CatalogAA(t *testing.T) {
	t.Run("PDFA-2b rejects AA", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("AA", &object.Dictionary{})

		errs := ValidatePDFA(doc, pdfa.PDFA2b)
		if !hasRule(errs, "6.5.2") {
			t.Error("expected 6.5.2 error for /AA in catalog")
		}
	})

	t.Run("PDFA-4 allows AA", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("AA", &object.Dictionary{})

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.6.3")
		if len(errs) > 0 {
			t.Error("PDF/A-4 should allow /AA in catalog")
		}
	})
}

func TestValidatePDFA_OCProperties(t *testing.T) {
	t.Run("PDFA-1b rejects OCProperties", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA1b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("OCProperties", &object.Dictionary{})

		errs := ValidatePDFA(doc, pdfa.PDFA1b)
		if !hasRule(errs, "6.1.13") {
			t.Error("expected 6.1.13 error for /OCProperties")
		}
	})

	t.Run("PDFA-2b allows OCProperties", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("OCProperties", &object.Dictionary{})

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.1.13")
		if len(errs) > 0 {
			t.Error("PDF/A-2b should allow /OCProperties")
		}
	})
}

func TestValidatePDFA_ExternalStreams(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte("test")}
	stream.Dict.Set("F", object.String{Value: []byte("external.dat")})
	stream.Dict.Set("Length", object.Integer(4))
	doc.Objects[10] = &object.IndirectObject{Number: 10, Value: stream}

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.1.6") {
		t.Error("expected 6.1.6 error for external stream reference")
	}
}

func TestValidatePDFA_FontsEmbedded(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Resources", object.IndirectRef{Number: 12})

	font := &object.Dictionary{}
	font.Set("Type", object.Name("Font"))
	font.Set("Subtype", object.Name("Type1"))
	font.Set("BaseFont", object.Name("Helvetica"))

	fontDict := &object.Dictionary{}
	fontDict.Set("F1", object.IndirectRef{Number: 11})

	resources := &object.Dictionary{}
	resources.Set("Font", fontDict)

	pages := doc.ResolveDict(doc.Trailer.Get("Root"))
	pagesDict := doc.ResolveDict(pages.Get("Pages"))
	pagesDict.Set("Kids", object.Array{object.IndirectRef{Number: 10}})
	pagesDict.Set("Count", object.Integer(1))

	doc.Objects[10] = &object.IndirectObject{Number: 10, Value: page}
	doc.Objects[11] = &object.IndirectObject{Number: 11, Value: font}
	doc.Objects[12] = &object.IndirectObject{Number: 12, Value: resources}

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.2.10.4.1") {
		t.Error("expected 6.2.10.4.1 error for non-embedded font")
	}
}

func TestValidatePDFA_ForbiddenActions(t *testing.T) {
	// All these are forbidden in PDF/A-4
	forbiddenTypes := []object.Name{
		"Launch", "Sound", "Movie", "ResetForm", "ImportData",
		"Hide", "Rendition", "Trans", "SetOCGState", "GoTo3DView",
	}

	for _, actionType := range forbiddenTypes {
		t.Run(string(actionType), func(t *testing.T) {
			doc := NewPDFADocument(pdfa.PDFA4)
			action := &object.Dictionary{}
			action.Set("S", actionType)
			doc.Objects[10] = &object.IndirectObject{Number: 10, Value: action}

			errs := ValidatePDFA(doc, pdfa.PDFA4)
			if !hasRule(errs, "6.6.1") {
				t.Errorf("expected 6.6.1 error for forbidden action type /%s", actionType)
			}
		})
	}

	t.Run("allowed actions pass", func(t *testing.T) {
		allowed := []object.Name{"GoTo", "GoToR", "URI", "Named", "SubmitForm", "JavaScript"}
		for _, s := range allowed {
			doc := NewPDFADocument(pdfa.PDFA4)
			action := &object.Dictionary{}
			action.Set("S", s)
			if s == "Named" {
				action.Set("N", object.Name("NextPage"))
			}
			doc.Objects[10] = &object.IndirectObject{Number: 10, Value: action}

			errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.6.3")
			if len(errs) > 0 {
				t.Errorf("action /%s should be allowed in PDF/A-4, got: %v", s, errs[0])
			}
		}
	})

	t.Run("JavaScript forbidden in PDFA-1b", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA1b)
		action := &object.Dictionary{}
		action.Set("S", object.Name("JavaScript"))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: action}

		errs := ValidatePDFA(doc, pdfa.PDFA1b)
		if !hasRule(errs, "6.6.1") {
			t.Error("expected 6.6.1 error for JavaScript in PDF/A-1b")
		}
	})
}

func TestValidatePDFA_OpenAction(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	action := &object.Dictionary{}
	action.Set("S", object.Name("ImportData"))
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: action}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	catalog.Set("OpenAction", object.IndirectRef{Number: 20})

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.6.1") {
		t.Error("expected 6.6.1 error for forbidden action in /OpenAction")
	}
}

func TestValidatePDFA_NamedActions(t *testing.T) {
	t.Run("allowed named actions", func(t *testing.T) {
		for _, name := range []object.Name{"NextPage", "PrevPage", "FirstPage", "LastPage"} {
			doc := NewPDFADocument(pdfa.PDFA4)
			action := &object.Dictionary{}
			action.Set("S", object.Name("Named"))
			action.Set("N", name)
			doc.Objects[10] = &object.IndirectObject{Number: 10, Value: action}

			errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.6.3")
			if len(errs) > 0 {
				t.Errorf("named action /%s should be allowed", name)
			}
		}
	})

	t.Run("forbidden named action", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		action := &object.Dictionary{}
		action.Set("S", object.Name("Named"))
		action.Set("N", object.Name("Print"))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: action}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.6.1") {
			t.Error("expected 6.6.1 error for named action /Print")
		}
	})
}

func TestValidatePDFA_WidgetAA(t *testing.T) {
	t.Run("PDFA-2b rejects widget AA", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA2b)
		widget := &object.Dictionary{}
		widget.Set("Subtype", object.Name("Widget"))
		widget.Set("AA", &object.Dictionary{})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: widget}

		errs := ValidatePDFA(doc, pdfa.PDFA2b)
		if !hasRule(errs, "6.6.3") {
			t.Error("expected 6.6.3 error for widget with /AA in PDF/A-2b")
		}
	})

	t.Run("PDFA-4 allows widget AA", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		widget := &object.Dictionary{}
		widget.Set("Subtype", object.Name("Widget"))
		widget.Set("AA", &object.Dictionary{})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: widget}

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.6.3")
		if len(errs) > 0 {
			t.Error("PDF/A-4 should allow widget /AA")
		}
	})
}

func TestValidatePDFA_WidgetNoAction(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	widget := &object.Dictionary{}
	widget.Set("Subtype", object.Name("Widget"))
	widget.Set("A", &object.Dictionary{})
	doc.Objects[10] = &object.IndirectObject{Number: 10, Value: widget}

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.4.1") {
		t.Error("expected 6.4.1 error for widget with /A")
	}
}

func TestValidatePDFA_NoXFA(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	acroForm := &object.Dictionary{}
	acroForm.Set("XFA", &object.Stream{})
	catalog.Set("AcroForm", acroForm)

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.4.2") {
		t.Error("expected 6.4.2 error for XFA in AcroForm")
	}
}

func TestValidatePDFA_NeedAppearances(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA4)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	acroForm := &object.Dictionary{}
	acroForm.Set("NeedAppearances", object.Boolean(true))
	catalog.Set("AcroForm", acroForm)

	errs := ValidatePDFA(doc, pdfa.PDFA4)
	if !hasRule(errs, "6.4.1") {
		t.Error("expected 6.4.1 error for NeedAppearances=true")
	}
}

// 6.4.3 (parts 2/3): a signature's /ByteRange must start at 0 and cover to the
// end of the file; a range that stops short leaves unsigned trailing bytes.
func TestValidatePDFA_SignatureByteRange(t *testing.T) {
	raw := make([]byte, 1000)
	mk := func(br object.Array) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		sig := &object.Dictionary{}
		sig.Set("Type", object.Name("Sig"))
		sig.Set("SubFilter", object.Name("adbe.pkcs7.detached"))
		sig.Set("Contents", object.String{Value: []byte("0000"), IsHex: true})
		sig.Set("ByteRange", br)
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: sig}
		return doc
	}
	flagged := func(br object.Array) bool {
		return hasRule(ValidatePDFABytes(mk(br), pdfa.PDFA2b, raw), "6.4.3")
	}
	// [start1 len1 start2 len2]; the gap is /Contents.
	if flagged(object.Array{object.Integer(0), object.Integer(400), object.Integer(600), object.Integer(400)}) {
		t.Error("a range reaching EOF (1000) must not be flagged")
	}
	if flagged(object.Array{object.Integer(0), object.Integer(400), object.Integer(600), object.Integer(500)}) {
		t.Error("a range overshooting the file must not be flagged (covers all)")
	}
	if !flagged(object.Array{object.Integer(0), object.Integer(400), object.Integer(600), object.Integer(300)}) {
		t.Error("a range stopping short of EOF (900<1000) must be flagged")
	}
	if !flagged(object.Array{object.Integer(10), object.Integer(400), object.Integer(600), object.Integer(390)}) {
		t.Error("a range not starting at byte 0 must be flagged")
	}
}

// Form XObject rules: /OPI is forbidden and a /Ref key (reference XObject) is
// forbidden outright, each cited under the level's clause.
func TestValidatePDFA_FormXObjectRules(t *testing.T) {
	mk := func(key object.Name) *Document {
		doc := NewPDFADocument(pdfa.PDFA4)
		form := &object.Stream{Dict: object.Dictionary{}}
		form.Dict.Set("Type", object.Name("XObject"))
		form.Dict.Set("Subtype", object.Name("Form"))
		form.Dict.Set(key, &object.Dictionary{})
		doc.Objects[20] = &object.IndirectObject{Number: 20, Value: form}
		return doc
	}
	if !hasRule(ValidatePDFA(mk("OPI"), pdfa.PDFA4), "6.2.8.1") {
		t.Error("form XObject /OPI must be flagged as 6.2.8.1 at PDF/A-4")
	}
	if !hasRule(ValidatePDFA(mk("Ref"), pdfa.PDFA4), "6.2.8.2") {
		t.Error("reference XObject (/Ref) must be flagged as 6.2.8.2 at PDF/A-4")
	}
	if !hasRule(ValidatePDFA(mk("Ref"), pdfa.PDFA2b), "6.2.9") {
		t.Error("reference XObject (/Ref) must be flagged as 6.2.9 at PDF/A-2b")
	}
	// A plain form XObject with neither key is clean.
	clean := NewPDFADocument(pdfa.PDFA4)
	form := &object.Stream{Dict: object.Dictionary{}}
	form.Dict.Set("Subtype", object.Name("Form"))
	clean.Objects[20] = &object.IndirectObject{Number: 20, Value: form}
	if hasRule(ValidatePDFA(clean, pdfa.PDFA4), "6.2.8.2") {
		t.Error("a form XObject without /Ref must not be flagged")
	}
}

// 6.5.3: at PDF/A-1 an annotation /CA (opacity) must be 1.0; other levels allow it.
func TestValidatePDFA_AnnotationOpacity(t *testing.T) {
	mk := func(ca object.Object) *Document {
		doc := NewPDFADocument(pdfa.PDFA1b)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Text"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		annot.Set("F", object.Integer(4)) // Print
		annot.Set("AP", &object.Dictionary{Keys: []object.Name{"N"}, Values: []object.Object{&object.Stream{}}})
		if ca != nil {
			annot.Set("CA", ca)
		}
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}
		return doc
	}
	if !hasRule(ValidatePDFA(mk(object.Real(0.5)), pdfa.PDFA1b), "6.5.3") {
		t.Error("CA=0.5 must be flagged at PDF/A-1b")
	}
	if hasRule(ValidatePDFA(mk(object.Integer(1)), pdfa.PDFA1b), "6.5.3") {
		t.Error("CA=1 must not be flagged")
	}
	if hasRule(ValidatePDFA(mk(nil), pdfa.PDFA1b), "6.5.3") {
		t.Error("absent CA must not be flagged")
	}
	if hasRule(ValidatePDFA(mk(object.Real(0.5)), pdfa.PDFA2b), "6.5.3") {
		t.Error("CA=0.5 must not be flagged at PDF/A-2b (transparency allowed)")
	}
}

func TestValidatePDFA_AnnotationFlags(t *testing.T) {
	t.Run("missing Print flag", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Text"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		annot.Set("F", object.Integer(0))
		annot.Set("AP", &object.Dictionary{Keys: []object.Name{"N"}, Values: []object.Object{&object.Stream{}}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.3.2") {
			t.Error("expected 6.3.2 error for missing Print flag")
		}
	})

	t.Run("Hidden flag set", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Text"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		annot.Set("F", object.Integer(4|2)) // Print + Hidden
		annot.Set("AP", &object.Dictionary{Keys: []object.Name{"N"}, Values: []object.Object{&object.Stream{}}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.3.2") {
			t.Error("expected 6.3.2 error for Hidden flag")
		}
	})

	t.Run("Popup exempt from F requirement", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Popup"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		// No /F — should be OK for Popup
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.3.2")
		if len(errs) > 0 {
			t.Error("Popup should be exempt from /F requirement")
		}
	})
}

func TestValidatePDFA_AnnotationAppearance(t *testing.T) {
	t.Run("missing AP", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Text"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		annot.Set("F", object.Integer(4))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.3.3") {
			t.Error("expected 6.3.3 error for missing /AP")
		}
	})

	t.Run("Link exempt from AP", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Link"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		annot.Set("F", object.Integer(4))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.3.3")
		if len(errs) > 0 {
			t.Error("Link should be exempt from /AP requirement")
		}
	})

	t.Run("Popup exempt from AP", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		annot := &object.Dictionary{}
		annot.Set("Type", object.Name("Annot"))
		annot.Set("Subtype", object.Name("Popup"))
		annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.3.3")
		if len(errs) > 0 {
			t.Error("Popup should be exempt from /AP requirement")
		}
	})
}

func TestValidatePDFA_MetadataVersion(t *testing.T) {
	t.Run("PDFA-4 missing rev", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		// Replace metadata with one missing pdfaid:rev
		xmp := []byte(`<?xpacket begin="` + "\xEF\xBB\xBF" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about=""
      xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
      <pdfaid:part>4</pdfaid:part>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`)
		doc.Objects[3].Value.(*object.Stream).Data = xmp

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.7.3") {
			t.Error("expected 6.7.3 error for missing pdfaid:rev")
		}
	})

	t.Run("PDFA-4 wrong rev", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		xmp := []byte(`<?xpacket begin="` + "\xEF\xBB\xBF" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about=""
      xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
      <pdfaid:part>4</pdfaid:part>
      <pdfaid:rev>20_y</pdfaid:rev>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`)
		doc.Objects[3].Value.(*object.Stream).Data = xmp

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.7.3")
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "rev") {
				found = true
			}
		}
		if !found {
			t.Error("expected 6.7.3 error for wrong pdfaid:rev")
		}
	})

	t.Run("PDFA-4 with conformance", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		xmp := []byte(`<?xpacket begin="` + "\xEF\xBB\xBF" + `" id="W5M0MpCehiHzreSzNTczkc9d"?>
<x:xmpmeta xmlns:x="adobe:ns:meta/">
  <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
    <rdf:Description rdf:about=""
      xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">
      <pdfaid:part>4</pdfaid:part>
      <pdfaid:conformance>B</pdfaid:conformance>
      <pdfaid:rev>2020</pdfaid:rev>
    </rdf:Description>
  </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>`)
		doc.Objects[3].Value.(*object.Stream).Data = xmp

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA4), "6.7.3")
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "conformance") {
				found = true
			}
		}
		if !found {
			t.Error("expected 6.7.3 error for pdfaid:conformance in PDF/A-4")
		}
	})
}

// addExtGStateToDoc adds an ExtGState dict to the test doc's page Resources.
// It creates a page (obj 20) with Resources/ExtGState referencing gsObj (obj 10).
func addExtGStateToDoc(doc *Document, gs *object.Dictionary) {
	doc.Objects[10] = &object.IndirectObject{Number: 10, Value: gs}

	gsDict := &object.Dictionary{}
	gsDict.Set("GS0", object.IndirectRef{Number: 10})

	resDict := &object.Dictionary{}
	resDict.Set("ExtGState", gsDict)

	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	page.Set("Resources", resDict)

	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: page}

	// Update page tree to include this page
	pagesDict := doc.ResolveDict(object.IndirectRef{Number: 2})
	pagesDict.Set("Kids", object.Array{object.IndirectRef{Number: 20}})
	pagesDict.Set("Count", object.Integer(1))
}

func TestValidatePDFA_Transparency(t *testing.T) {
	t.Run("PDFA-1b rejects SMask", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA1b)
		gs := &object.Dictionary{}
		gs.Set("SMask", &object.Dictionary{})
		addExtGStateToDoc(doc, gs)

		errs := ValidatePDFA(doc, pdfa.PDFA1b)
		if !hasRule(errs, "6.4") {
			t.Error("expected 6.4 error for transparency")
		}
	})

	t.Run("PDFA-1b allows SMask None", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA1b)
		gs := &object.Dictionary{}
		gs.Set("SMask", object.Name("None"))
		addExtGStateToDoc(doc, gs)

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA1b), "6.4")
		if len(errs) > 0 {
			t.Error("SMask /None should be allowed in PDF/A-1b")
		}
	})

	t.Run("PDFA-1b rejects non-Normal BM", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA1b)
		gs := &object.Dictionary{}
		gs.Set("BM", object.Name("Multiply"))
		addExtGStateToDoc(doc, gs)

		errs := ValidatePDFA(doc, pdfa.PDFA1b)
		if !hasRule(errs, "6.4") {
			t.Error("expected 6.4 error for non-Normal blend mode")
		}
	})

	t.Run("PDFA-2b allows transparency", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA2b)
		gs := &object.Dictionary{}
		gs.Set("SMask", &object.Dictionary{})
		gs.Set("BM", object.Name("Multiply"))
		addExtGStateToDoc(doc, gs)

		errs := filterRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.4")
		if len(errs) > 0 {
			t.Error("PDF/A-2b should allow transparency")
		}
	})
}

func TestValidatePDFA_ImageChecks(t *testing.T) {
	t.Run("alternate images", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		img := &object.Stream{Dict: object.Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", object.Name("Image"))
		img.Dict.Set("Alternates", object.Array{})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.2.7.1") {
			t.Error("expected 6.2.7 error for /Alternates")
		}
	})

	t.Run("interpolate true", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		img := &object.Stream{Dict: object.Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", object.Name("Image"))
		img.Dict.Set("Interpolate", object.Boolean(true))
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.2.7.1") {
			t.Error("expected 6.2.7 error for /Interpolate true")
		}
	})

	t.Run("OPI in XObject", func(t *testing.T) {
		doc := NewPDFADocument(pdfa.PDFA4)
		img := &object.Stream{Dict: object.Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", object.Name("Image"))
		img.Dict.Set("OPI", &object.Dictionary{})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, pdfa.PDFA4)
		if !hasRule(errs, "6.2.7.1") {
			t.Error("expected 6.2.7 error for /OPI")
		}
	})
}

func TestValidatePDFA_RoundTrip(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)

			var buf bytes.Buffer
			if err := doc.Write(&buf); err != nil {
				t.Fatalf("Write: %v", err)
			}

			data := buf.Bytes()
			r := bytes.NewReader(data)
			doc2, err := Read(r, int64(len(data)))
			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			errs := ValidatePDFA(doc2, level)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("validation error after round-trip: %v", e)
				}
			}
		})
	}
}

func TestGenerateXMPMetadata(t *testing.T) {
	t.Run("PDFA-4", func(t *testing.T) {
		xmp := GenerateXMPMetadata(pdfa.PDFA4, "Test Title", "Test Author")
		s := string(xmp)

		if !strings.Contains(s, "<pdfaid:part>4</pdfaid:part>") {
			t.Error("missing pdfaid:part=4")
		}
		if !strings.Contains(s, "<pdfaid:rev>2020</pdfaid:rev>") {
			t.Error("missing pdfaid:rev=2020")
		}
		if strings.Contains(s, "pdfaid:conformance") {
			t.Error("PDF/A-4 should not have conformance level")
		}
		if !strings.Contains(s, "Test Title") {
			t.Error("missing title")
		}
		if !strings.Contains(s, "Test Author") {
			t.Error("missing author")
		}
		if !strings.Contains(s, "<?xpacket") {
			t.Error("missing xpacket header")
		}
	})

	t.Run("PDFA-1b", func(t *testing.T) {
		xmp := GenerateXMPMetadata(pdfa.PDFA1b, "", "")
		s := string(xmp)

		if !strings.Contains(s, "<pdfaid:part>1</pdfaid:part>") {
			t.Error("missing pdfaid:part=1")
		}
		if !strings.Contains(s, "<pdfaid:conformance>B</pdfaid:conformance>") {
			t.Error("missing pdfaid:conformance=B")
		}
	})

	t.Run("XML escaping", func(t *testing.T) {
		xmp := GenerateXMPMetadata(pdfa.PDFA4, "Title <with> & \"special\" chars", "")
		s := string(xmp)

		if strings.Contains(s, "<with>") {
			t.Error("angle brackets not escaped")
		}
		if !strings.Contains(s, "&lt;with&gt;") {
			t.Error("expected escaped angle brackets")
		}
	})
}

func TestDefaultSRGBProfile(t *testing.T) {
	profile := DefaultSRGBProfile()

	if len(profile) < 128 {
		t.Fatalf("profile too short: %d bytes", len(profile))
	}

	size := uint32(profile[0])<<24 | uint32(profile[1])<<16 | uint32(profile[2])<<8 | uint32(profile[3])
	if size != uint32(len(profile)) {
		t.Errorf("profile size field = %d, actual = %d", size, len(profile))
	}

	if string(profile[36:40]) != "acsp" {
		t.Errorf("missing 'acsp' signature, got %q", string(profile[36:40]))
	}

	if string(profile[16:20]) != "RGB " {
		t.Errorf("color space = %q, want 'RGB '", string(profile[16:20]))
	}

	if string(profile[12:16]) != "mntr" {
		t.Errorf("device class = %q, want 'mntr'", string(profile[12:16]))
	}
}

func TestResolve(t *testing.T) {
	doc := &Document{
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: object.Name("Test")},
			2: {Number: 2, Value: &object.Dictionary{}},
		},
	}

	t.Run("resolves indirect ref", func(t *testing.T) {
		obj := doc.Resolve(object.IndirectRef{Number: 1})
		if n, ok := obj.(object.Name); !ok || n != "Test" {
			t.Errorf("got %v, want Name(Test)", obj)
		}
	})

	t.Run("passes through non-ref", func(t *testing.T) {
		obj := doc.Resolve(object.Name("Direct"))
		if n, ok := obj.(object.Name); !ok || n != "Direct" {
			t.Errorf("got %v, want Name(Direct)", obj)
		}
	})

	t.Run("returns nil for missing ref", func(t *testing.T) {
		obj := doc.Resolve(object.IndirectRef{Number: 99})
		if obj != nil {
			t.Errorf("got %v, want nil", obj)
		}
	})
}

func TestResolveDict(t *testing.T) {
	dict := &object.Dictionary{}
	dict.Set("Key", object.Name("Value"))

	doc := &Document{
		Objects: map[int]*object.IndirectObject{
			1: {Number: 1, Value: dict},
			2: {Number: 2, Value: object.Name("NotADict")},
		},
	}

	t.Run("resolves to dictionary", func(t *testing.T) {
		d := doc.ResolveDict(object.IndirectRef{Number: 1})
		if d == nil {
			t.Fatal("expected non-nil dictionary")
		}
		if d.Get("Key") == nil {
			t.Error("resolved dict missing expected key")
		}
	})

	t.Run("returns nil for non-dict", func(t *testing.T) {
		d := doc.ResolveDict(object.IndirectRef{Number: 2})
		if d != nil {
			t.Error("expected nil for non-dictionary object")
		}
	})

	t.Run("returns nil for missing ref", func(t *testing.T) {
		d := doc.ResolveDict(object.IndirectRef{Number: 99})
		if d != nil {
			t.Error("expected nil for missing ref")
		}
	})
}

func TestValidatePDFA_CleanDocument(t *testing.T) {
	for _, level := range []pdfa.Level{pdfa.PDFA1b, pdfa.PDFA2b, pdfa.PDFA3b, pdfa.PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			errs := ValidatePDFA(doc, level)
			if len(errs) > 0 {
				t.Errorf("clean %s document has %d validation errors:", level, len(errs))
				for _, e := range errs {
					t.Errorf("  %v", e)
				}
			}
		})
	}
}

func TestValidationErrorString(t *testing.T) {
	t.Run("without object", func(t *testing.T) {
		e := pdfa.ValidationError{Rule: "6.1.3", Level: pdfa.PDFA4, Message: "test message"}
		s := e.Error()
		if !strings.Contains(s, "PDF/A-4") || !strings.Contains(s, "6.1.3") || !strings.Contains(s, "test message") {
			t.Errorf("unexpected Error() output: %s", s)
		}
	})

	t.Run("with object", func(t *testing.T) {
		e := pdfa.ValidationError{Rule: "6.2.10", Level: pdfa.PDFA1b, Message: "font error", Object: 42}
		s := e.Error()
		if !strings.Contains(s, "object 42") {
			t.Errorf("expected 'object 42' in output: %s", s)
		}
	})
}

func TestPDFALevelString(t *testing.T) {
	tests := map[pdfa.Level]string{
		pdfa.PDFA1b: "PDF/A-1b",
		pdfa.PDFA2b: "PDF/A-2b",
		pdfa.PDFA3b: "PDF/A-3b",
		pdfa.PDFA4:  "PDF/A-4",
	}
	for level, want := range tests {
		if got := level.String(); got != want {
			t.Errorf("PDFALevel(%d).String() = %q, want %q", int(level), got, want)
		}
	}
}

func TestExtractXMPValue(t *testing.T) {
	xmp := `<pdfaid:part>4</pdfaid:part>
      <pdfaid:rev>2020</pdfaid:rev>
      pdfaid:conformance="B"`

	if v := core.ExtractXMPValue(xmp, "pdfaid:part"); v != "4" {
		t.Errorf("part = %q, want 4", v)
	}
	if v := core.ExtractXMPValue(xmp, "pdfaid:rev"); v != "2020" {
		t.Errorf("rev = %q, want 2020", v)
	}
	if v := core.ExtractXMPValue(xmp, "pdfaid:conformance"); v != "B" {
		t.Errorf("conformance = %q, want B", v)
	}
	if v := core.ExtractXMPValue(xmp, "pdfaid:nonexistent"); v != "" {
		t.Errorf("nonexistent = %q, want empty", v)
	}
}

// --- Corpus tests ---

func corpusLevel(dirName string) (pdfa.Level, bool) {
	switch dirName {
	case "PDF_A-1b":
		return pdfa.PDFA1b, true
	case "PDF_A-2b":
		return pdfa.PDFA2b, true
	case "PDF_A-3b":
		return pdfa.PDFA3b, true
	case "PDF_A-4":
		return pdfa.PDFA4, true
	default:
		return 0, false
	}
}

// Ratcheting baselines for the veraPDF corpus. The validator is a work in
// progress and does not yet implement every PDF/A rule, so it cannot pass the
// whole corpus. Rather than assert per-file (which is permanently red and hides
// regressions), TestCorpus measures aggregate outcomes and fails only if they
// get WORSE than these recorded numbers. Tighten them as coverage improves; a
// change that pushes any count above its baseline is a regression to
// investigate. Update with the values TestCorpus logs after an intended change.
const (
	// Pass files the validator wrongly rejects (false positives). Keep at 0.
	corpusMaxFalsePositives = 0
	// Fail files the validator fails to flag (false negatives / unimplemented
	// rules). This is the headline coverage gap; drive it down over time.
	corpusMaxMissed = 0
	// Every PDF_A-* corpus file now parses; the parser recovers from the
	// deliberately-malformed structure the fail files exercise (wrong stream
	// Length, header-offset convention, corrupt object streams) and reports
	// the defect rather than aborting.
	corpusMaxParseErrors = 0

	// Isartor fail files (all PDF/A-1b) that the validator does not yet flag.
	// TestCorpus validates only the PDF_A-* suites, so without this the Isartor
	// gaps regress invisibly. Drive it down; each drop means a newly covered
	// rule. (18 originally; 16 after the transparency fix; 13 after enabling the
	// XMP packet-header / well-formedness rules at PDF/A-1b; 11 after enabling
	// the TrueType-encoding rule at 1b; 8 after flagging damaged embedded font
	// programs; 7 after the symbolic-TrueType single-cmap rule; 6 after
	// scanning annotation appearance streams for undefined operators; 5 after
	// the PDF/A-1 CMap-embedding rule; 4 after validating extension-schema
	// field value types; 3 after flagging an XMP packet whose rdf:RDF
	// namespace prefix is undeclared; 2 after the linearized-file /ID
	// consistency rule; 1 after the byte-level stream /Length check.)
	corpusMaxIsartorMissed = 1

	// Level A baselines (TestCorpusLevelA). The PDF_A-1a / PDF_A-2a suites
	// declare pdfaid:conformance A, so they are only measured meaningfully
	// when validated at PDF/A-1a / PDF/A-2a — see the comment on
	// TestCorpusLevelA for why validating them at Level B measures nothing.
	//
	// Pass files wrongly rejected at Level A. Like corpusMaxFalsePositives,
	// this is the hard invariant: never raise it.
	corpusMaxLevelAFalsePositives = 0
	// Fail files not flagged at Level A. These are a known COVERAGE GAP, not
	// a target: the Level A rule families pdf0 implements (Tagged PDF /
	// StructTreeRoot, catalog /Lang syntax, pdfaid:conformance) do not cover
	// Unicode character maps (ISO 19005-1 6.3.8 — when a ToUnicode entry is
	// mandatory), ActualText for Private Use Area code points (-2 6.2.11.7.3),
	// structure-type RoleMap validity (6.8.3.4 / 6.7.3.4), or a language
	// identifier carried on a structure element or marked-content property
	// list rather than the catalog (6.8.4 / 6.7.4). Drive them down as those
	// rules land; each drop locks in a newly covered Level A requirement.
	corpusMaxLevelA1aMissed = 9
	corpusMaxLevelA2aMissed = 9
)

// TestCorpusParsesEntirely asserts that every PDF in the whole veraPDF corpus
// — not just the PDF_A-* conformance subset that TestCorpus validates, but the
// Isartor and PDF/UA suites too — reads without error. The parser recovery
// work (PR "parser-recovery") fixed the same malformations across all suites,
// and this guards against regressing that.
func TestCorpusParsesEntirely(t *testing.T) {
	corpusDir := os.Getenv("VERAPDF_CORPUS")
	if corpusDir == "" {
		corpusDir = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpusDir); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not found; run `make corpus` to download")
	}
	var total int
	var failures []string
	filepath.Walk(corpusDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
			return nil
		}
		base := filepath.Base(path)
		if !strings.Contains(base, "-pass-") && !strings.Contains(base, "-fail-") {
			return nil
		}
		total++
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if _, e := Read(bytes.NewReader(data), int64(len(data))); e != nil {
			rel, _ := filepath.Rel(corpusDir, path)
			failures = append(failures, rel+" :: "+e.Error())
		}
		return nil
	})
	t.Logf("corpus parse: %d files, %d failures", total, len(failures))
	if len(failures) > 0 {
		t.Errorf("%d corpus files failed to parse:\n  %s", len(failures), strings.Join(failures, "\n  "))
	}
}

// TestCorpusIsartor ratchets the Isartor PDF/A-1b fail suite, which TestCorpus
// (PDF_A-* only) does not cover. Every Isartor file is a known 1b violation, so
// a file validating with zero errors is a missed detection. The suite has no
// pass files, so this guards detection (missed <= baseline) and any future
// false positive (fp must stay 0).
//
// The PDF_A-4f / PDF_A-4e suites are deliberately NOT ratcheted yet: validating
// them at PDF/A-4 yields false positives because the A-4e/A-4f feature
// relaxations (embedded 3D/RichMedia for 4e, arbitrary embedded files for 4f)
// are not modelled. Baking those false positives into a baseline would lower
// the FP=0 bar; they can join once those relaxations exist.
func TestCorpusIsartor(t *testing.T) {
	corpusDir := os.Getenv("VERAPDF_CORPUS")
	if corpusDir == "" {
		corpusDir = "testdata/verapdf-corpus"
	}
	root := filepath.Join(corpusDir, "Isartor test files")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not found; run `make corpus` to download")
	}

	var fail, missed, fp, parseErrors int
	var missedFiles []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
			return nil
		}
		base := filepath.Base(path)
		isPass := strings.Contains(base, "-pass-")
		isFail := strings.Contains(base, "-fail-")
		if !isPass && !isFail {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		doc, e := Read(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			parseErrors++
			return nil
		}
		// Isartor is a PDF/A-1b test suite.
		errs := ValidatePDFABytes(doc, pdfa.PDFA1b, data)
		if isPass {
			if len(errs) > 0 {
				fp++
			}
			return nil
		}
		fail++
		if len(errs) == 0 {
			missed++
			missedFiles = append(missedFiles, base)
		}
		return nil
	})

	t.Logf("Isartor results: fail=%d | falsePositives=%d missed=%d parseErrors=%d",
		fail, fp, missed, parseErrors)

	if fp > 0 {
		t.Errorf("Isartor false positives %d exceed baseline 0 (regression)", fp)
	}
	if missed > corpusMaxIsartorMissed {
		t.Errorf("Isartor missed %d exceed baseline %d (detection regressed):\n  %s",
			missed, corpusMaxIsartorMissed, strings.Join(missedFiles, "\n  "))
	}
	if parseErrors > 0 {
		t.Errorf("Isartor parse errors %d exceed baseline 0 (regression)", parseErrors)
	}
}

// TestCorpusConformanceSuites ratchets the FAIL files of the remaining
// conformance suites that TestCorpus does not cover, guarding detection from
// regressing across the whole corpus.
//
// It deliberately counts only fail files. The pass files of these suites cannot
// be ratcheted at FP=0: they are minimal per-clause fixtures (a "1a-pass" file
// passes the one accessibility clause it targets but is not a complete 1b
// document), and the 4e/4f feature relaxations (embedded 3D/RichMedia,
// arbitrary embedded files) are not modelled — so validating their pass files
// yields expected false positives. Baking those in would lower the FP=0 bar.
//
// A further caveat: many of these fail files are caught incidentally (they trip
// an implemented PDF/A rule unrelated to the clause they were built for, and
// PDF/UA is a different standard entirely). This is a regression net, not a
// claim of 1a/2a/2u/UA conformance coverage — that needs new rule families.
func TestCorpusConformanceSuites(t *testing.T) {
	corpusDir := os.Getenv("VERAPDF_CORPUS")
	if corpusDir == "" {
		corpusDir = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpusDir); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not found; run `make corpus` to download")
	}

	suites := []struct {
		dir       string
		level     pdfa.Level
		maxMissed int
		// checkPassFP asserts FP=0 on the suite's pass files. Only enabled where
		// the validator models the suite's conformance well enough (the 4f/4e
		// relaxations); the a/u/UA pass files are minimal per-clause fixtures
		// that false-positive by design, so they remain untracked.
		checkPassFP bool
	}{
		// The 1a/2a rows validate Level A files at Level B. Every file in
		// those suites declares pdfaid:conformance A, which the Level B
		// pipeline rejects outright, so their fail files are all "caught"
		// on that one finding and missed=0 here is an artifact rather than
		// detection. They are kept as a cheap regression net (a change that
		// broke the conformance rule would show up), but the meaningful
		// measurement of these suites is TestCorpusLevelA.
		{"PDF_A-1a", pdfa.PDFA1b, 0, false},
		{"PDF_A-2a", pdfa.PDFA2b, 0, false},
		{"PDF_A-2u", pdfa.PDFA2b, 0, false},
		{"PDF_A-4f", pdfa.PDFA4, 2, true},
		{"PDF_A-4e", pdfa.PDFA4, 3, true},
		{"PDF_UA-1", pdfa.PDFA2b, 0, false},
		{"PDF_UA-2", pdfa.PDFA4, 0, false},
	}

	for _, s := range suites {
		root := filepath.Join(corpusDir, s.dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		var fail, missed, parseErrors, falsePositives int
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
				return nil
			}
			base := filepath.Base(path)
			isPass := strings.Contains(base, "-pass-")
			isFail := strings.Contains(base, "-fail-")
			if !isPass && !isFail {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			doc, e := Read(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				parseErrors++
				return nil
			}
			errs := ValidatePDFABytes(doc, s.level, data)
			if isPass {
				if s.checkPassFP && len(errs) > 0 {
					falsePositives++
				}
				return nil
			}
			fail++
			if len(errs) == 0 {
				missed++
			}
			return nil
		})
		t.Logf("%-10s @ %-8v : fail=%d missed=%d falsePositives=%d parseErrors=%d", s.dir, s.level, fail, missed, falsePositives, parseErrors)
		if missed > s.maxMissed {
			t.Errorf("%s: missed %d exceed baseline %d (detection regressed)", s.dir, missed, s.maxMissed)
		}
		if s.checkPassFP && falsePositives > 0 {
			t.Errorf("%s: false positives %d exceed baseline 0 (regression)", s.dir, falsePositives)
		}
		if parseErrors > 0 {
			t.Errorf("%s: parse errors %d exceed baseline 0 (regression)", s.dir, parseErrors)
		}
	}
}

// TestCorpusLevelA ratchets the PDF/A Level A conformance suites at their own
// conformance level, which is the only level at which they measure anything.
//
// TestCorpusConformanceSuites walks PDF_A-1a and PDF_A-2a at PDFA1b/PDFA2b.
// Every file in those suites declares pdfaid:conformance A, and the Level B
// pipeline requires B — so at Level B every fail file is flagged for that one
// unrelated reason (missed=0 is guaranteed, not earned) and every pass file is
// a false positive (which is why that test has checkPassFP disabled for them).
// Validating at PDFA1a/PDFA2a drops that finding and puts validatePDFALevelA
// and its checks under measurement for the first time.
//
// Like TestCorpus this is a ratchet: it fails only when a count gets worse.
// falsePositives is the hard invariant at 0 — a Level A pass file is a
// conforming document and rejecting it means a rule is wrong.
func TestCorpusLevelA(t *testing.T) {
	corpusDir := os.Getenv("VERAPDF_CORPUS")
	if corpusDir == "" {
		corpusDir = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpusDir); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not found; run `make corpus` to download")
	}

	suites := []struct {
		dir       string
		level     pdfa.Level
		maxMissed int
	}{
		{"PDF_A-1a", pdfa.PDFA1a, corpusMaxLevelA1aMissed},
		{"PDF_A-2a", pdfa.PDFA2a, corpusMaxLevelA2aMissed},
	}

	for _, s := range suites {
		root := filepath.Join(corpusDir, s.dir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		var pass, fail, missed, falsePositives, parseErrors int
		var fpFiles, missedFiles []string
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
				return nil
			}
			base := filepath.Base(path)
			isPass := strings.Contains(base, "-pass-")
			isFail := strings.Contains(base, "-fail-")
			if !isPass && !isFail {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			doc, e := Read(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				parseErrors++
				return nil
			}
			errs := ValidatePDFABytes(doc, s.level, data)
			if isPass {
				pass++
				if len(errs) > 0 {
					falsePositives++
					fpFiles = append(fpFiles, base+" :: "+errs[0].Error())
				}
				return nil
			}
			fail++
			if len(errs) == 0 {
				missed++
				missedFiles = append(missedFiles, base)
			}
			return nil
		})
		t.Logf("%-10s @ %-8v : pass=%d fail=%d missed=%d falsePositives=%d parseErrors=%d",
			s.dir, s.level, pass, fail, missed, falsePositives, parseErrors)

		if falsePositives > corpusMaxLevelAFalsePositives {
			t.Errorf("%s: false positives %d exceed baseline %d (regression). Offending pass files:\n  %s",
				s.dir, falsePositives, corpusMaxLevelAFalsePositives, strings.Join(fpFiles, "\n  "))
		}
		if missed > s.maxMissed {
			t.Errorf("%s: missed %d exceed baseline %d (detection regressed):\n  %s",
				s.dir, missed, s.maxMissed, strings.Join(missedFiles, "\n  "))
		}
		if parseErrors > 0 {
			t.Errorf("%s: parse errors %d exceed baseline 0 (regression)", s.dir, parseErrors)
		}
	}
}

func TestCorpus(t *testing.T) {
	corpusDir := os.Getenv("VERAPDF_CORPUS")
	if corpusDir == "" {
		corpusDir = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpusDir); os.IsNotExist(err) {
		t.Skip("veraPDF corpus not found; run `make corpus` to download")
	}

	levels := []string{"PDF_A-4", "PDF_A-1b", "PDF_A-2b", "PDF_A-3b"}

	var (
		passTotal, failTotal   int
		falsePositives, missed int
		parseErrors            int
	)
	// Record the specific files behind each regression bucket so a baseline
	// breach is debuggable.
	var fpFiles, parseErrFiles []string

	for _, levelDir := range levels {
		level, ok := corpusLevel(levelDir)
		if !ok {
			t.Fatalf("unknown level dir: %s", levelDir)
		}
		root := filepath.Join(corpusDir, levelDir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}

		// Collect paths first, then iterate. This avoids holding all parsed
		// Documents in memory at once (which caused OOM kills with the full
		// 2900+ file corpus).
		var files []corpusFile
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".pdf") {
				return nil
			}
			rel, _ := filepath.Rel(corpusDir, path)
			isPass := strings.Contains(filepath.Base(path), "-pass-")
			isFail := strings.Contains(filepath.Base(path), "-fail-")
			if !isPass && !isFail {
				return nil
			}
			files = append(files, corpusFile{path: path, rel: rel, isPass: isPass})
			return nil
		})

		for i, f := range files {
			data, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatalf("read %s: %v", f.rel, err)
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				parseErrors++
				parseErrFiles = append(parseErrFiles, f.rel)
			} else {
				errs := ValidatePDFABytes(doc, level, data)
				if f.isPass {
					passTotal++
					if len(errs) > 0 {
						falsePositives++
						fpFiles = append(fpFiles, f.rel)
					}
				} else {
					failTotal++
					if len(errs) == 0 {
						missed++
					}
				}
			}
			// Force GC every 100 files to keep memory bounded.
			if (i+1)%100 == 0 {
				runtime.GC()
			}
		}
	}

	t.Logf("corpus results: pass=%d fail=%d | falsePositives=%d missed=%d parseErrors=%d",
		passTotal, failTotal, falsePositives, missed, parseErrors)

	if falsePositives > corpusMaxFalsePositives {
		t.Errorf("false positives %d exceed baseline %d (regression). Offending pass files:\n  %s",
			falsePositives, corpusMaxFalsePositives, strings.Join(fpFiles, "\n  "))
	}
	if missed > corpusMaxMissed {
		t.Errorf("missed violations %d exceed baseline %d (detection regressed)", missed, corpusMaxMissed)
	}
	if parseErrors > corpusMaxParseErrors {
		t.Errorf("parse errors %d exceed baseline %d (regression). Offending files:\n  %s",
			parseErrors, corpusMaxParseErrors, strings.Join(parseErrFiles, "\n  "))
	}
}

func TestDecodeXMPToUTF8(t *testing.T) {
	t.Run("plain UTF-8", func(t *testing.T) {
		data := []byte("<pdfaid:part>4</pdfaid:part>")
		got := core.DecodeXMPToUTF8(data)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in output, got %q", got)
		}
	})

	t.Run("UTF-8 with BOM", func(t *testing.T) {
		data := append([]byte{0xEF, 0xBB, 0xBF}, []byte("<pdfaid:part>4</pdfaid:part>")...)
		got := core.DecodeXMPToUTF8(data)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in output, got %q", got)
		}
	})

	t.Run("UTF-16 BE with BOM", func(t *testing.T) {
		// Encode "<p>" as UTF-16 BE
		src := "<pdfaid:part>4</pdfaid:part>"
		var utf16be []byte
		utf16be = append(utf16be, 0xFE, 0xFF) // BOM
		for _, c := range []byte(src) {
			utf16be = append(utf16be, 0x00, c)
		}
		got := core.DecodeXMPToUTF8(utf16be)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in decoded UTF-16 BE, got %q", got)
		}
	})

	t.Run("UTF-16 LE with BOM", func(t *testing.T) {
		src := "<pdfaid:part>4</pdfaid:part>"
		var utf16le []byte
		utf16le = append(utf16le, 0xFF, 0xFE) // BOM
		for _, c := range []byte(src) {
			utf16le = append(utf16le, c, 0x00)
		}
		got := core.DecodeXMPToUTF8(utf16le)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in decoded UTF-16 LE, got %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got := core.DecodeXMPToUTF8(nil)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func hasRule(errs []pdfa.ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func filterRule(errs []pdfa.ValidationError, rule string) []pdfa.ValidationError {
	var result []pdfa.ValidationError
	for _, e := range errs {
		if e.Rule == rule {
			result = append(result, e)
		}
	}
	return result
}

// addTestPage inserts a page (object 20) into a NewPDFADocument's empty page
// tree and returns its dictionary for further mutation.
func addTestPage(doc *Document) *object.Dictionary {
	page := &object.Dictionary{}
	page.Set("Type", object.Name("Page"))
	page.Set("Parent", object.IndirectRef{Number: 2})
	page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
	doc.Objects[20] = &object.IndirectObject{Number: 20, Value: page}
	pages := doc.Objects[2].Value.(*object.Dictionary)
	pages.Set("Kids", object.Array{object.IndirectRef{Number: 20}})
	pages.Set("Count", object.Integer(1))
	return page
}

// A7: Resolve must follow ref->ref chains and bail out on cycles.
func TestResolveChainsAndCycles(t *testing.T) {
	doc := &Document{Objects: map[int]*object.IndirectObject{
		1: {Number: 1, Value: object.IndirectRef{Number: 2}},
		2: {Number: 2, Value: object.IndirectRef{Number: 3}},
		3: {Number: 3, Value: object.Integer(42)},
		7: {Number: 7, Value: object.IndirectRef{Number: 8}},
		8: {Number: 8, Value: object.IndirectRef{Number: 7}},
	}}
	if v, ok := doc.Resolve(object.IndirectRef{Number: 1}).(object.Integer); !ok || v != 42 {
		t.Errorf("chained resolve: expected 42, got %#v", doc.Resolve(object.IndirectRef{Number: 1}))
	}
	if v := doc.Resolve(object.IndirectRef{Number: 7}); v != nil {
		t.Errorf("cyclic resolve: expected nil, got %#v", v)
	}
	if v, ok := doc.Resolve(object.Integer(5)).(object.Integer); !ok || v != 5 {
		t.Error("non-ref must resolve to itself")
	}
}

// A9: annotations written as direct dictionaries in a page's /Annots must be
// subject to the same checks as top-level annotation objects.
func TestValidatePDFA_DirectAnnotationsChecked(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)

	annot := &object.Dictionary{}
	annot.Set("Subtype", object.Name("Screen")) // forbidden at 2b
	annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
	// no /F, no /AP: should also trip 6.3.2 and 6.3.3
	page.Set("Annots", object.Array{annot})

	errs := ValidatePDFA(doc, pdfa.PDFA2b)
	for _, rule := range []string{"6.3.1", "6.3.2", "6.3.3"} {
		if !hasRule(errs, rule) {
			t.Errorf("expected %s error for direct-dict annotation, got %v", rule, errs)
		}
	}
}

// A9: direct annotations with direct forbidden actions must be flagged.
func TestValidatePDFA_DirectAnnotationForbiddenAction(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)

	action := &object.Dictionary{}
	action.Set("S", object.Name("Launch"))
	annot := &object.Dictionary{}
	annot.Set("Subtype", object.Name("Link"))
	annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
	annot.Set("F", object.Integer(4))
	annot.Set("A", action)
	page.Set("Annots", object.Array{annot})

	errs := ValidatePDFA(doc, pdfa.PDFA2b)
	if !hasRule(errs, "6.5.1") {
		t.Errorf("expected 6.6.1 error for direct annotation's Launch action, got %v", errs)
	}
}

// A13: Separation/DeviceN rules must fire when Resources is a direct
// dictionary on the page (the common case).
func TestValidatePDFA_SeparationInDirectResources(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)

	// DeviceN with 33 colorants exceeds the PDF/A-2 limit of 32.
	var colorants object.Array
	for i := 0; i < 33; i++ {
		colorants = append(colorants, object.Name(fmt.Sprintf("C%d", i)))
	}
	deviceN := object.Array{object.Name("DeviceN"), colorants, object.Name("DeviceCMYK"), object.IndirectRef{Number: 5}}
	csDict := &object.Dictionary{}
	csDict.Set("CS0", deviceN)
	resources := &object.Dictionary{}
	resources.Set("ColorSpace", csDict)
	page.Set("Resources", resources) // direct, not an indirect object

	errs := ValidatePDFA(doc, pdfa.PDFA2b)
	if !hasRule(errs, "6.2.4") {
		t.Errorf("expected 6.2.4 error for 33-colorant DeviceN in direct Resources, got %v", errs)
	}
}

// Separation tint transforms: equal-by-content duplicates are conformant;
// genuinely different transforms for the same colorant are not.
func TestValidatePDFA_TintTransformConsistency(t *testing.T) {
	build := func(fn2Body object.Object) *Document {
		doc := NewPDFADocument(pdfa.PDFA2b)
		page := addTestPage(doc)

		fn := &object.Dictionary{}
		fn.Set("FunctionType", object.Integer(2))
		fn.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
		fn.Set("N", object.Integer(1))
		doc.Objects[30] = &object.IndirectObject{Number: 30, Value: fn}
		doc.Objects[31] = &object.IndirectObject{Number: 31, Value: fn2Body}

		// The alternate must be CIE-based: a device alternate would need
		// OutputIntent coverage and trip the device-colour rule instead.
		alt := object.Array{object.Name("ICCBased"), object.IndirectRef{Number: 5}}
		sep1 := object.Array{object.Name("Separation"), object.Name("Spot"), alt, object.IndirectRef{Number: 30}}
		sep2 := object.Array{object.Name("Separation"), object.Name("Spot"), alt, object.IndirectRef{Number: 31}}
		csDict := &object.Dictionary{}
		csDict.Set("CS0", sep1)
		csDict.Set("CS1", sep2)
		resources := &object.Dictionary{}
		resources.Set("ColorSpace", csDict)
		page.Set("Resources", resources)
		return doc
	}

	identical := &object.Dictionary{}
	identical.Set("FunctionType", object.Integer(2))
	identical.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	identical.Set("N", object.Integer(1))
	errs := filterRule(ValidatePDFA(build(identical), pdfa.PDFA2b), "6.2.4.4")
	if len(errs) > 0 {
		t.Errorf("identical tint transforms in different objects must pass, got %v", errs)
	}

	different := &object.Dictionary{}
	different.Set("FunctionType", object.Integer(2))
	different.Set("Domain", object.Array{object.Integer(0), object.Integer(1)})
	different.Set("N", object.Integer(2))
	if !hasRule(ValidatePDFA(build(different), pdfa.PDFA2b), "6.2.4.4") {
		t.Error("differing tint transforms for the same colorant must be flagged")
	}
}

// A19: forbidden actions hiding behind /Next chains must be found.
func TestValidatePDFA_ActionNextChain(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	launch := &object.Dictionary{}
	launch.Set("S", object.Name("Launch"))
	action := &object.Dictionary{}
	action.Set("S", object.Name("GoTo"))
	action.Set("Next", object.Array{launch})
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	catalog.Set("OpenAction", action)

	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.5.1") {
		t.Error("expected 6.6.1 error for Launch action in /Next chain")
	}

	// A /Next cycle must terminate.
	a := &object.Dictionary{}
	a.Set("S", object.Name("GoTo"))
	a.Set("Next", a)
	doc2 := NewPDFADocument(pdfa.PDFA2b)
	catalog2 := doc2.ResolveDict(doc2.Trailer.Get("Root"))
	catalog2.Set("OpenAction", a)
	ValidatePDFA(doc2, pdfa.PDFA2b) // must not hang
}

// A19: page dictionaries must not carry /AA at 1b/2b/3b.
func TestValidatePDFA_PageAA(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)
	page.Set("AA", &object.Dictionary{})
	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.5.2") {
		t.Error("expected 6.6.2 error for page /AA")
	}
}

// A22: UTF-16BE Info strings must compare equal to their UTF-8 XMP values.
func TestDecodePDFTextString(t *testing.T) {
	utf16 := []byte{0xFE, 0xFF, 0x00, 'H', 0x00, 'i', 0x20, 0xAC >> 8, 0xAC & 0xFF}
	_ = utf16
	if got := core.DecodePDFTextString([]byte{0xFE, 0xFF, 0x00, 'H', 0x00, 'i'}); got != "Hi" {
		t.Errorf("UTF-16BE decode: expected 'Hi', got %q", got)
	}
	if got := core.DecodePDFTextString([]byte{0xEF, 0xBB, 0xBF, 'H', 'i'}); got != "Hi" {
		t.Errorf("UTF-8 BOM decode: expected 'Hi', got %q", got)
	}
	if got := core.DecodePDFTextString([]byte("plain")); got != "plain" {
		t.Errorf("plain decode: expected 'plain', got %q", got)
	}
	// Surrogate pair: U+1D11E MUSICAL SYMBOL G CLEF
	if got := core.DecodePDFTextString([]byte{0xFE, 0xFF, 0xD8, 0x34, 0xDD, 0x1E}); got != "\U0001D11E" {
		t.Errorf("surrogate decode: got %q", got)
	}
}

// Validation output must be deterministic (checks iterate Go maps).
func TestValidatePDFA_DeterministicOutput(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)
	// Provoke several errors from different checks.
	page.Set("AA", &object.Dictionary{})
	annot := &object.Dictionary{}
	annot.Set("Subtype", object.Name("Screen"))
	annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(10), object.Integer(10)})
	page.Set("Annots", object.Array{annot})

	first := ValidatePDFA(doc, pdfa.PDFA2b)
	for i := 0; i < 5; i++ {
		again := ValidatePDFA(doc, pdfa.PDFA2b)
		if len(again) != len(first) {
			t.Fatalf("run %d: %d errors vs %d", i, len(again), len(first))
		}
		for j := range again {
			if again[j] != first[j] {
				t.Fatalf("run %d: error %d differs: %v vs %v", i, j, again[j], first[j])
			}
		}
	}
}

// A24: content wrapped in a filter ARRAY must still be scanned.
func TestContentScanHandlesFilterArrays(t *testing.T) {
	var raw bytes.Buffer
	for i := 0; i < 30; i++ {
		raw.WriteString("q ")
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()

	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)
	content := &object.Stream{Dict: object.Dictionary{}, Data: z.Bytes()}
	content.Dict.Set("Filter", object.Array{object.Name("FlateDecode")})
	content.Dict.Set("Length", object.Integer(z.Len()))
	doc.Objects[21] = &object.IndirectObject{Number: 21, Value: content}
	page.Set("Contents", object.IndirectRef{Number: 21})

	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.1.13") {
		t.Error("q/Q nesting inside a filter-array stream must be detected")
	}
}

// A31: inheritable page attributes come from the Pages ancestors.
func TestPageSizeLimitInherited(t *testing.T) {
	doc := NewPDFADocument(pdfa.PDFA2b)
	page := addTestPage(doc)
	page.Delete("MediaBox")
	pages := doc.Objects[2].Value.(*object.Dictionary)
	pages.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(1), object.Integer(1)}) // 1x1: below 3-unit floor

	if !hasRule(ValidatePDFA(doc, pdfa.PDFA2b), "6.1.13") {
		t.Error("undersized inherited MediaBox must be detected")
	}
}

// C21: builder accepts title/author and stays conformant.
func TestNewPDFADocumentWithInfo(t *testing.T) {
	doc := NewPDFADocumentWithInfo(pdfa.PDFA2b, "My Title", "An Author")
	meta := doc.Objects[3].Value.(*object.Stream)
	if !bytes.Contains(meta.Data, []byte("My Title")) || !bytes.Contains(meta.Data, []byte("An Author")) {
		t.Error("title/author missing from generated XMP")
	}
	if errs := ValidatePDFA(doc, pdfa.PDFA2b); len(errs) > 0 {
		t.Errorf("document with info should validate clean: %v", errs)
	}
}

// C30: XML-illegal control characters are stripped from XMP values.
func TestXMLEscapeControlChars(t *testing.T) {
	got := pdfa.XMLEscape("a\x00b\x1Fc\td\ne")
	if got != "abc\td\ne" {
		t.Errorf("expected control chars stripped, got %q", got)
	}
	if pdfa.XMLEscape("<&>") != "&lt;&amp;&gt;" {
		t.Error("metacharacter escaping broken")
	}
}

// C22: Integer-Real equality uses the same epsilon as Real-Real.
func TestEqualNumericEpsilonConsistency(t *testing.T) {
	if !Equal(object.Real(1.0), object.Real(1.0+1e-12)) {
		t.Error("Real-Real epsilon expected")
	}
	if !Equal(object.Integer(1), object.Real(1.0+1e-12)) {
		t.Error("Integer-Real must use the same epsilon as Real-Real")
	}
	if !Equal(object.Real(1.0+1e-12), object.Integer(1)) {
		t.Error("Real-Integer must use the same epsilon as Real-Real")
	}
	if Equal(object.Integer(1), object.Real(1.5)) {
		t.Error("distinct values must not be equal")
	}
}
