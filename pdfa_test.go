package pdf0

import (
	"bytes"
	"fmt"
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

func TestNewPDFADocument(t *testing.T) {
	for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := NewPDFADocument(level)
			if doc == nil {
				t.Fatal("NewPDFADocument returned nil")
			}

			expected := pdfaVersion(level)
			if doc.Version != expected {
				t.Errorf("version = %q, want %q", doc.Version, expected)
			}

			if doc.Trailer.Get("Root") == nil {
				t.Error("trailer missing /Root")
			}
			if doc.Trailer.Get("ID") == nil {
				t.Error("trailer missing /ID")
			}

			errs := ValidatePDFA(doc, level)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("validation error: %v", e)
				}
			}
		})
	}
}

func TestValidatePDFA_NoEncrypt(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	doc.Trailer.Set("Encrypt", &Dictionary{})

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.1.3") {
		t.Error("expected 6.1.3 error for /Encrypt in trailer")
	}
}

func TestValidatePDFA_FileID(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	doc.Trailer.Delete("ID")

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.1.3") {
		t.Error("expected 6.1.3 error for missing /ID")
	}
}

func TestValidatePDFA_Header(t *testing.T) {
	tests := []struct {
		level   PDFALevel
		version string
		wantErr bool
	}{
		{PDFA1b, "1.4", false},
		{PDFA1b, "1.7", false},
		{PDFA1b, "2.0", false},
		{PDFA2b, "1.4", false},
		{PDFA2b, "1.5", false},
		{PDFA2b, "1.7", false},
		{PDFA2b, "2.0", true},
		{PDFA3b, "1.7", false},
		{PDFA3b, "2.0", true},
		{PDFA4, "2.0", false},
		{PDFA4, "1.7", true},
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
		doc := NewPDFADocument(PDFA4)
		infoDict := &Dictionary{}
		infoDict.Set("ModDate", String{Value: []byte("D:20240101")})
		doc.Objects[20] = &IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", IndirectRef{Number: 20})

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.1.3") {
			t.Error("expected 6.1.3 error for Info without PieceInfo")
		}
	})

	t.Run("Info with non-ModDate key", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("PieceInfo", &Dictionary{})
		infoDict := &Dictionary{}
		infoDict.Set("Title", String{Value: []byte("Test")})
		doc.Objects[20] = &IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", IndirectRef{Number: 20})

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.1.3") {
			t.Error("expected 6.1.3 error for Info with non-ModDate key")
		}
	})

	t.Run("Info with only ModDate and PieceInfo", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("PieceInfo", &Dictionary{})
		infoDict := &Dictionary{}
		infoDict.Set("ModDate", String{Value: []byte("D:20240101")})
		doc.Objects[20] = &IndirectObject{Number: 20, Value: infoDict}
		doc.Trailer.Set("Info", IndirectRef{Number: 20})

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.1.3")
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
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Delete("Metadata")

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.7.2") {
			t.Error("expected 6.7.2 error for missing metadata")
		}
	})

	t.Run("metadata with filter", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		metaStream := doc.Objects[3].Value.(*Stream)
		metaStream.Dict.Set("Filter", Name("FlateDecode"))

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.7.2") {
			t.Error("expected 6.7.2 error for filtered metadata")
		}
	})
}

func TestValidatePDFA_OutputIntents(t *testing.T) {
	t.Run("missing output intents OK for all levels", func(t *testing.T) {
		for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
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
		for _, level := range []PDFALevel{PDFA2b, PDFA3b, PDFA4} {
			doc := NewPDFADocument(level)
			catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
			catalog.Set("OutputIntents", Array{})

			errs := filterRule(ValidatePDFA(doc, level), "6.2.3")
			if len(errs) > 0 {
				t.Errorf("%s should allow empty OutputIntents array, got: %v", level, errs[0])
			}
		}
	})

	t.Run("validates OutputIntents structure when present", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		// Set OutputIntents to array with invalid entry
		badOI := &Dictionary{}
		catalog.Set("OutputIntents", Array{badOI})

		errs := filterRule(ValidatePDFA(doc, PDFA2b), "6.2.3")
		if len(errs) == 0 {
			t.Error("expected 6.2.3 error for OutputIntent without /S")
		}
	})
}

func TestValidatePDFA_CatalogAA(t *testing.T) {
	t.Run("PDFA-2b rejects AA", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("AA", &Dictionary{})

		errs := ValidatePDFA(doc, PDFA2b)
		if !hasRule(errs, "6.6.1") {
			t.Error("expected 6.6.1 error for /AA in catalog")
		}
	})

	t.Run("PDFA-4 allows AA", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("AA", &Dictionary{})

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.6.1")
		if len(errs) > 0 {
			t.Error("PDF/A-4 should allow /AA in catalog")
		}
	})
}

func TestValidatePDFA_OCProperties(t *testing.T) {
	t.Run("PDFA-1b rejects OCProperties", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("OCProperties", &Dictionary{})

		errs := ValidatePDFA(doc, PDFA1b)
		if !hasRule(errs, "6.1.13") {
			t.Error("expected 6.1.13 error for /OCProperties")
		}
	})

	t.Run("PDFA-2b allows OCProperties", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("OCProperties", &Dictionary{})

		errs := filterRule(ValidatePDFA(doc, PDFA2b), "6.1.13")
		if len(errs) > 0 {
			t.Error("PDF/A-2b should allow /OCProperties")
		}
	})
}

func TestValidatePDFA_LZW(t *testing.T) {
	t.Run("all levels reject LZW", func(t *testing.T) {
		for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
			t.Run(level.String(), func(t *testing.T) {
				doc := NewPDFADocument(level)
				stream := &Stream{Dict: Dictionary{}, Data: []byte("test")}
				stream.Dict.Set("Filter", Name("LZWDecode"))
				stream.Dict.Set("Length", Integer(4))
				doc.Objects[10] = &IndirectObject{Number: 10, Value: stream}

				errs := ValidatePDFA(doc, level)
				if !hasRule(errs, "6.1.6") {
					t.Errorf("expected 6.1.6 error for LZW filter in %s", level)
				}
			})
		}
	})
}

func TestValidatePDFA_ExternalStreams(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	stream := &Stream{Dict: Dictionary{}, Data: []byte("test")}
	stream.Dict.Set("F", String{Value: []byte("external.dat")})
	stream.Dict.Set("Length", Integer(4))
	doc.Objects[10] = &IndirectObject{Number: 10, Value: stream}

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.1.6") {
		t.Error("expected 6.1.6 error for external stream reference")
	}
}

func TestValidatePDFA_FontsEmbedded(t *testing.T) {
	doc := NewPDFADocument(PDFA4)

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	page.Set("Resources", IndirectRef{Number: 12})

	font := &Dictionary{}
	font.Set("Type", Name("Font"))
	font.Set("Subtype", Name("Type1"))
	font.Set("BaseFont", Name("Helvetica"))

	fontDict := &Dictionary{}
	fontDict.Set("F1", IndirectRef{Number: 11})

	resources := &Dictionary{}
	resources.Set("Font", fontDict)

	pages := doc.ResolveDict(doc.Trailer.Get("Root"))
	pagesDict := doc.ResolveDict(pages.Get("Pages"))
	pagesDict.Set("Kids", Array{IndirectRef{Number: 10}})
	pagesDict.Set("Count", Integer(1))

	doc.Objects[10] = &IndirectObject{Number: 10, Value: page}
	doc.Objects[11] = &IndirectObject{Number: 11, Value: font}
	doc.Objects[12] = &IndirectObject{Number: 12, Value: resources}

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.2.10") {
		t.Error("expected 6.2.10 error for non-embedded font")
	}
}

func TestValidatePDFA_AnnotationSubtypes(t *testing.T) {
	forbidden := []struct {
		subtype Name
		level   PDFALevel
	}{
		{"Movie", PDFA4},
		{"Sound", PDFA4},
		{"Screen", PDFA4},
		{"3D", PDFA4},
		{"RichMedia", PDFA4},
		// FileAttachment is forbidden in PDF/A-1b (which bans embedded files)
		// but allowed in PDF/A-2/3/4 (it is the PDF/A-3 embedding mechanism).
		{"FileAttachment", PDFA1b},
	}

	for _, tt := range forbidden {
		t.Run(string(tt.subtype)+"/"+tt.level.String(), func(t *testing.T) {
			doc := NewPDFADocument(tt.level)
			annot := &Dictionary{}
			annot.Set("Type", Name("Annot"))
			annot.Set("Subtype", tt.subtype)
			annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
			annot.Set("F", Integer(4))
			annot.Set("AP", &Dictionary{Keys: []Name{"N"}, Values: []Object{&Stream{}}})
			doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

			errs := ValidatePDFA(doc, tt.level)
			if !hasRule(errs, "6.3.1") {
				t.Errorf("expected 6.3.1 error for forbidden subtype /%s", tt.subtype)
			}
		})
	}

	t.Run("allowed subtypes pass", func(t *testing.T) {
		allowed := []Name{"Text", "Link", "FreeText", "Widget", "Popup", "Stamp", "FileAttachment"}
		for _, st := range allowed {
			doc := NewPDFADocument(PDFA4)
			annot := &Dictionary{}
			annot.Set("Type", Name("Annot"))
			annot.Set("Subtype", st)
			annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
			annot.Set("F", Integer(4))
			annot.Set("AP", &Dictionary{Keys: []Name{"N"}, Values: []Object{&Stream{}}})
			doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

			errs := filterRule(ValidatePDFA(doc, PDFA4), "6.3.1")
			if len(errs) > 0 {
				t.Errorf("subtype /%s should be allowed in PDF/A-4", st)
			}
		}
	})
}

func TestValidatePDFA_ForbiddenActions(t *testing.T) {
	// All these are forbidden in PDF/A-4
	forbiddenTypes := []Name{
		"Launch", "Sound", "Movie", "ResetForm", "ImportData",
		"Hide", "Rendition", "Trans", "SetOCGState", "GoTo3DView",
	}

	for _, actionType := range forbiddenTypes {
		t.Run(string(actionType), func(t *testing.T) {
			doc := NewPDFADocument(PDFA4)
			action := &Dictionary{}
			action.Set("S", actionType)
			doc.Objects[10] = &IndirectObject{Number: 10, Value: action}

			errs := ValidatePDFA(doc, PDFA4)
			if !hasRule(errs, "6.6.1") {
				t.Errorf("expected 6.6.1 error for forbidden action type /%s", actionType)
			}
		})
	}

	t.Run("allowed actions pass", func(t *testing.T) {
		allowed := []Name{"GoTo", "GoToR", "URI", "Named", "SubmitForm", "JavaScript"}
		for _, s := range allowed {
			doc := NewPDFADocument(PDFA4)
			action := &Dictionary{}
			action.Set("S", s)
			if s == "Named" {
				action.Set("N", Name("NextPage"))
			}
			doc.Objects[10] = &IndirectObject{Number: 10, Value: action}

			errs := filterRule(ValidatePDFA(doc, PDFA4), "6.6.1")
			if len(errs) > 0 {
				t.Errorf("action /%s should be allowed in PDF/A-4, got: %v", s, errs[0])
			}
		}
	})

	t.Run("JavaScript forbidden in PDFA-1b", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		action := &Dictionary{}
		action.Set("S", Name("JavaScript"))
		doc.Objects[10] = &IndirectObject{Number: 10, Value: action}

		errs := ValidatePDFA(doc, PDFA1b)
		if !hasRule(errs, "6.6.1") {
			t.Error("expected 6.6.1 error for JavaScript in PDF/A-1b")
		}
	})
}

func TestValidatePDFA_OpenAction(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	action := &Dictionary{}
	action.Set("S", Name("ImportData"))
	doc.Objects[20] = &IndirectObject{Number: 20, Value: action}
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	catalog.Set("OpenAction", IndirectRef{Number: 20})

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.6.1") {
		t.Error("expected 6.6.1 error for forbidden action in /OpenAction")
	}
}

func TestValidatePDFA_NamedActions(t *testing.T) {
	t.Run("allowed named actions", func(t *testing.T) {
		for _, name := range []Name{"NextPage", "PrevPage", "FirstPage", "LastPage"} {
			doc := NewPDFADocument(PDFA4)
			action := &Dictionary{}
			action.Set("S", Name("Named"))
			action.Set("N", name)
			doc.Objects[10] = &IndirectObject{Number: 10, Value: action}

			errs := filterRule(ValidatePDFA(doc, PDFA4), "6.6.1")
			if len(errs) > 0 {
				t.Errorf("named action /%s should be allowed", name)
			}
		}
	})

	t.Run("forbidden named action", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		action := &Dictionary{}
		action.Set("S", Name("Named"))
		action.Set("N", Name("Print"))
		doc.Objects[10] = &IndirectObject{Number: 10, Value: action}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.6.1") {
			t.Error("expected 6.6.1 error for named action /Print")
		}
	})
}

func TestValidatePDFA_WidgetAA(t *testing.T) {
	t.Run("PDFA-2b rejects widget AA", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		widget := &Dictionary{}
		widget.Set("Subtype", Name("Widget"))
		widget.Set("AA", &Dictionary{})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: widget}

		errs := ValidatePDFA(doc, PDFA2b)
		if !hasRule(errs, "6.6.3") {
			t.Error("expected 6.6.3 error for widget with /AA in PDF/A-2b")
		}
	})

	t.Run("PDFA-4 allows widget AA", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		widget := &Dictionary{}
		widget.Set("Subtype", Name("Widget"))
		widget.Set("AA", &Dictionary{})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: widget}

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.6.3")
		if len(errs) > 0 {
			t.Error("PDF/A-4 should allow widget /AA")
		}
	})
}

func TestValidatePDFA_WidgetNoAction(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	widget := &Dictionary{}
	widget.Set("Subtype", Name("Widget"))
	widget.Set("A", &Dictionary{})
	doc.Objects[10] = &IndirectObject{Number: 10, Value: widget}

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.4.1") {
		t.Error("expected 6.4.1 error for widget with /A")
	}
}

func TestValidatePDFA_NoXFA(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	acroForm := &Dictionary{}
	acroForm.Set("XFA", &Stream{})
	catalog.Set("AcroForm", acroForm)

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.4.2") {
		t.Error("expected 6.4.2 error for XFA in AcroForm")
	}
}

func TestValidatePDFA_NeedAppearances(t *testing.T) {
	doc := NewPDFADocument(PDFA4)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	acroForm := &Dictionary{}
	acroForm.Set("NeedAppearances", Boolean(true))
	catalog.Set("AcroForm", acroForm)

	errs := ValidatePDFA(doc, PDFA4)
	if !hasRule(errs, "6.4.1") {
		t.Error("expected 6.4.1 error for NeedAppearances=true")
	}
}

func TestValidatePDFA_AnnotationFlags(t *testing.T) {
	t.Run("missing Print flag", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Text"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		annot.Set("F", Integer(0))
		annot.Set("AP", &Dictionary{Keys: []Name{"N"}, Values: []Object{&Stream{}}})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.3.2") {
			t.Error("expected 6.3.2 error for missing Print flag")
		}
	})

	t.Run("Hidden flag set", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Text"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		annot.Set("F", Integer(4|2)) // Print + Hidden
		annot.Set("AP", &Dictionary{Keys: []Name{"N"}, Values: []Object{&Stream{}}})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.3.2") {
			t.Error("expected 6.3.2 error for Hidden flag")
		}
	})

	t.Run("Popup exempt from F requirement", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Popup"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		// No /F — should be OK for Popup
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.3.2")
		if len(errs) > 0 {
			t.Error("Popup should be exempt from /F requirement")
		}
	})
}

func TestValidatePDFA_AnnotationAppearance(t *testing.T) {
	t.Run("missing AP", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Text"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		annot.Set("F", Integer(4))
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.3.3") {
			t.Error("expected 6.3.3 error for missing /AP")
		}
	})

	t.Run("Link exempt from AP", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Link"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		annot.Set("F", Integer(4))
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.3.3")
		if len(errs) > 0 {
			t.Error("Link should be exempt from /AP requirement")
		}
	})

	t.Run("Popup exempt from AP", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		annot := &Dictionary{}
		annot.Set("Type", Name("Annot"))
		annot.Set("Subtype", Name("Popup"))
		annot.Set("Rect", Array{Integer(0), Integer(0), Integer(100), Integer(100)})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: annot}

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.3.3")
		if len(errs) > 0 {
			t.Error("Popup should be exempt from /AP requirement")
		}
	})
}

func TestValidatePDFA_MetadataVersion(t *testing.T) {
	t.Run("PDFA-4 missing rev", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
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
		doc.Objects[3].Value.(*Stream).Data = xmp

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.7.3") {
			t.Error("expected 6.7.3 error for missing pdfaid:rev")
		}
	})

	t.Run("PDFA-4 wrong rev", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
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
		doc.Objects[3].Value.(*Stream).Data = xmp

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.7.3")
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
		doc := NewPDFADocument(PDFA4)
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
		doc.Objects[3].Value.(*Stream).Data = xmp

		errs := filterRule(ValidatePDFA(doc, PDFA4), "6.7.3")
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
func addExtGStateToDoc(doc *Document, gs *Dictionary) {
	doc.Objects[10] = &IndirectObject{Number: 10, Value: gs}

	gsDict := &Dictionary{}
	gsDict.Set("GS0", IndirectRef{Number: 10})

	resDict := &Dictionary{}
	resDict.Set("ExtGState", gsDict)

	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	page.Set("Resources", resDict)

	doc.Objects[20] = &IndirectObject{Number: 20, Value: page}

	// Update page tree to include this page
	pagesDict := doc.ResolveDict(IndirectRef{Number: 2})
	pagesDict.Set("Kids", Array{IndirectRef{Number: 20}})
	pagesDict.Set("Count", Integer(1))
}

func TestValidatePDFA_Transparency(t *testing.T) {
	t.Run("PDFA-1b rejects SMask", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		gs := &Dictionary{}
		gs.Set("SMask", &Dictionary{})
		addExtGStateToDoc(doc, gs)

		errs := ValidatePDFA(doc, PDFA1b)
		if !hasRule(errs, "6.4") {
			t.Error("expected 6.4 error for transparency")
		}
	})

	t.Run("PDFA-1b allows SMask None", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		gs := &Dictionary{}
		gs.Set("SMask", Name("None"))
		addExtGStateToDoc(doc, gs)

		errs := filterRule(ValidatePDFA(doc, PDFA1b), "6.4")
		if len(errs) > 0 {
			t.Error("SMask /None should be allowed in PDF/A-1b")
		}
	})

	t.Run("PDFA-1b rejects non-Normal BM", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		gs := &Dictionary{}
		gs.Set("BM", Name("Multiply"))
		addExtGStateToDoc(doc, gs)

		errs := ValidatePDFA(doc, PDFA1b)
		if !hasRule(errs, "6.4") {
			t.Error("expected 6.4 error for non-Normal blend mode")
		}
	})

	t.Run("PDFA-2b allows transparency", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		gs := &Dictionary{}
		gs.Set("SMask", &Dictionary{})
		gs.Set("BM", Name("Multiply"))
		addExtGStateToDoc(doc, gs)

		errs := filterRule(ValidatePDFA(doc, PDFA2b), "6.4")
		if len(errs) > 0 {
			t.Error("PDF/A-2b should allow transparency")
		}
	})
}

func TestValidatePDFA_ImageChecks(t *testing.T) {
	t.Run("alternate images", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		img := &Stream{Dict: Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", Name("Image"))
		img.Dict.Set("Alternates", Array{})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.2.7") {
			t.Error("expected 6.2.7 error for /Alternates")
		}
	})

	t.Run("interpolate true", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		img := &Stream{Dict: Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", Name("Image"))
		img.Dict.Set("Interpolate", Boolean(true))
		doc.Objects[10] = &IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.2.7") {
			t.Error("expected 6.2.7 error for /Interpolate true")
		}
	})

	t.Run("OPI in XObject", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		img := &Stream{Dict: Dictionary{}, Data: []byte{0xFF}}
		img.Dict.Set("Subtype", Name("Image"))
		img.Dict.Set("OPI", &Dictionary{})
		doc.Objects[10] = &IndirectObject{Number: 10, Value: img}

		errs := ValidatePDFA(doc, PDFA4)
		if !hasRule(errs, "6.2.7") {
			t.Error("expected 6.2.7 error for /OPI")
		}
	})
}

func TestValidatePDFA_RoundTrip(t *testing.T) {
	for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
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
		xmp := GenerateXMPMetadata(PDFA4, "Test Title", "Test Author")
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
		xmp := GenerateXMPMetadata(PDFA1b, "", "")
		s := string(xmp)

		if !strings.Contains(s, "<pdfaid:part>1</pdfaid:part>") {
			t.Error("missing pdfaid:part=1")
		}
		if !strings.Contains(s, "<pdfaid:conformance>B</pdfaid:conformance>") {
			t.Error("missing pdfaid:conformance=B")
		}
	})

	t.Run("XML escaping", func(t *testing.T) {
		xmp := GenerateXMPMetadata(PDFA4, "Title <with> & \"special\" chars", "")
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
		Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: Name("Test")},
			2: {Number: 2, Value: &Dictionary{}},
		},
	}

	t.Run("resolves indirect ref", func(t *testing.T) {
		obj := doc.Resolve(IndirectRef{Number: 1})
		if n, ok := obj.(Name); !ok || n != "Test" {
			t.Errorf("got %v, want Name(Test)", obj)
		}
	})

	t.Run("passes through non-ref", func(t *testing.T) {
		obj := doc.Resolve(Name("Direct"))
		if n, ok := obj.(Name); !ok || n != "Direct" {
			t.Errorf("got %v, want Name(Direct)", obj)
		}
	})

	t.Run("returns nil for missing ref", func(t *testing.T) {
		obj := doc.Resolve(IndirectRef{Number: 99})
		if obj != nil {
			t.Errorf("got %v, want nil", obj)
		}
	})
}

func TestResolveDict(t *testing.T) {
	dict := &Dictionary{}
	dict.Set("Key", Name("Value"))

	doc := &Document{
		Objects: map[int]*IndirectObject{
			1: {Number: 1, Value: dict},
			2: {Number: 2, Value: Name("NotADict")},
		},
	}

	t.Run("resolves to dictionary", func(t *testing.T) {
		d := doc.ResolveDict(IndirectRef{Number: 1})
		if d == nil {
			t.Fatal("expected non-nil dictionary")
		}
		if d.Get("Key") == nil {
			t.Error("resolved dict missing expected key")
		}
	})

	t.Run("returns nil for non-dict", func(t *testing.T) {
		d := doc.ResolveDict(IndirectRef{Number: 2})
		if d != nil {
			t.Error("expected nil for non-dictionary object")
		}
	})

	t.Run("returns nil for missing ref", func(t *testing.T) {
		d := doc.ResolveDict(IndirectRef{Number: 99})
		if d != nil {
			t.Error("expected nil for missing ref")
		}
	})
}

func TestValidatePDFA_CleanDocument(t *testing.T) {
	for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
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
		e := ValidationError{Rule: "6.1.3", Level: PDFA4, Message: "test message"}
		s := e.Error()
		if !strings.Contains(s, "PDF/A-4") || !strings.Contains(s, "6.1.3") || !strings.Contains(s, "test message") {
			t.Errorf("unexpected Error() output: %s", s)
		}
	})

	t.Run("with object", func(t *testing.T) {
		e := ValidationError{Rule: "6.2.10", Level: PDFA1b, Message: "font error", Object: 42}
		s := e.Error()
		if !strings.Contains(s, "object 42") {
			t.Errorf("expected 'object 42' in output: %s", s)
		}
	})
}

func TestPDFALevelString(t *testing.T) {
	tests := map[PDFALevel]string{
		PDFA1b: "PDF/A-1b",
		PDFA2b: "PDF/A-2b",
		PDFA3b: "PDF/A-3b",
		PDFA4:  "PDF/A-4",
	}
	for level, want := range tests {
		if got := level.String(); got != want {
			t.Errorf("PDFALevel(%d).String() = %q, want %q", int(level), got, want)
		}
	}
}

func TestXmpHasKey(t *testing.T) {
	tests := []struct {
		name   string
		xmp    string
		key    string
		expect bool
	}{
		{"element present", `<pdfaid:conformance>B</pdfaid:conformance>`, "pdfaid:conformance", true},
		{"attribute present", `pdfaid:conformance="B"`, "pdfaid:conformance", true},
		{"attribute empty", `pdfaid:conformance=""`, "pdfaid:conformance", true},
		{"not present", `<pdfaid:part>4</pdfaid:part>`, "pdfaid:conformance", false},
		{"self-closing element", `<pdfaid:conformance/>`, "pdfaid:conformance", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := xmpHasKey(tt.xmp, tt.key); got != tt.expect {
				t.Errorf("xmpHasKey(%q, %q) = %v, want %v", tt.xmp, tt.key, got, tt.expect)
			}
		})
	}
}

func TestValidatePDFA_NoDataAfterEOF(t *testing.T) {
	t.Run("clean EOF", func(t *testing.T) {
		data := []byte("%PDF-2.0\n%%EOF\n")
		errs := checkNoDataAfterEOF(data, PDFA4)
		if len(errs) > 0 {
			t.Error("unexpected error for clean EOF")
		}
	})

	t.Run("data after EOF", func(t *testing.T) {
		data := []byte("%PDF-2.0\n%%EOF\nSomeData")
		errs := checkNoDataAfterEOF(data, PDFA4)
		if len(errs) == 0 {
			t.Error("expected error for data after EOF marker")
		}
	})

	t.Run("no EOF marker", func(t *testing.T) {
		data := []byte("%PDF-2.0\n")
		errs := checkNoDataAfterEOF(data, PDFA4)
		if len(errs) == 0 {
			t.Error("expected error for missing EOF marker")
		}
	})
}

func TestExtractXMPValue(t *testing.T) {
	xmp := `<pdfaid:part>4</pdfaid:part>
      <pdfaid:rev>2020</pdfaid:rev>
      pdfaid:conformance="B"`

	if v := extractXMPValue(xmp, "pdfaid:part"); v != "4" {
		t.Errorf("part = %q, want 4", v)
	}
	if v := extractXMPValue(xmp, "pdfaid:rev"); v != "2020" {
		t.Errorf("rev = %q, want 2020", v)
	}
	if v := extractXMPValue(xmp, "pdfaid:conformance"); v != "B" {
		t.Errorf("conformance = %q, want B", v)
	}
	if v := extractXMPValue(xmp, "pdfaid:nonexistent"); v != "" {
		t.Errorf("nonexistent = %q, want empty", v)
	}
}

// --- Corpus tests ---

func corpusLevel(dirName string) (PDFALevel, bool) {
	switch dirName {
	case "PDF_A-1b":
		return PDFA1b, true
	case "PDF_A-2b":
		return PDFA2b, true
	case "PDF_A-3b":
		return PDFA3b, true
	case "PDF_A-4":
		return PDFA4, true
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
	corpusMaxMissed = 790
	// Files the parser cannot read at all. All five are deliberately broken
	// fail files: four with malformed stream keywords/lengths and one whose
	// object stream holds corrupt zlib data.
	corpusMaxParseErrors = 5
)

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
		got := decodeXMPToUTF8(data)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in output, got %q", got)
		}
	})

	t.Run("UTF-8 with BOM", func(t *testing.T) {
		data := append([]byte{0xEF, 0xBB, 0xBF}, []byte("<pdfaid:part>4</pdfaid:part>")...)
		got := decodeXMPToUTF8(data)
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
		got := decodeXMPToUTF8(utf16be)
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
		got := decodeXMPToUTF8(utf16le)
		if !strings.Contains(got, "pdfaid:part") {
			t.Errorf("expected pdfaid:part in decoded UTF-16 LE, got %q", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got := decodeXMPToUTF8(nil)
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})
}

func TestCheckCatalogVersion(t *testing.T) {
	t.Run("no catalog version OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("valid 2.0 OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("Version", Name("2.0"))
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("invalid 1.7 fails", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("Version", Name("1.7"))
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) == 0 {
			t.Error("expected error for catalog version 1.7")
		}
	})

	t.Run("non-PDFA4 skipped", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkCatalogVersion(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for non-PDFA4: %v", errs[0])
		}
	})
}

func TestCheckExtGState(t *testing.T) {
	t.Run("TR forbidden", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		gs := &Dictionary{}
		gs.Set("TR", Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) == 0 {
			t.Error("expected error for /TR in ExtGState")
		}
	})

	t.Run("TR2 Default OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		gs := &Dictionary{}
		gs.Set("TR2", Name("Default"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("TR2 non-Default forbidden", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		gs := &Dictionary{}
		gs.Set("TR2", Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) == 0 {
			t.Error("expected error for /TR2 non-Default in ExtGState")
		}
	})

	t.Run("skipped for PDFA1b", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		gs := &Dictionary{}
		gs.Set("TR", Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for PDFA1b: %v", errs[0])
		}
	})
}

func TestCheckEmbeddedFiles(t *testing.T) {
	t.Run("PDFA-1b rejects embedded files", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &Dictionary{}
		namesDict.Set("EmbeddedFiles", &Dictionary{})
		catalog.Set("Names", namesDict)

		errs := checkEmbeddedFiles(doc, PDFA1b)
		if len(errs) == 0 {
			t.Error("expected error for EmbeddedFiles in PDF/A-1b")
		}
	})

	t.Run("PDFA-2b rejects embedded files", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &Dictionary{}
		namesDict.Set("EmbeddedFiles", &Dictionary{})
		catalog.Set("Names", namesDict)

		errs := checkEmbeddedFiles(doc, PDFA2b)
		if len(errs) == 0 {
			t.Error("expected error for EmbeddedFiles in PDF/A-2b")
		}
	})

	t.Run("PDFA-3b allows embedded files with requirements", func(t *testing.T) {
		doc := NewPDFADocument(PDFA3b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &Dictionary{}
		namesDict.Set("EmbeddedFiles", &Dictionary{})
		catalog.Set("Names", namesDict)
		catalog.Set("AF", Array{})

		errs := checkEmbeddedFiles(doc, PDFA3b)
		// Should not complain about embedded files existing
		for _, e := range errs {
			if strings.Contains(e.Message, "must not be present") {
				t.Errorf("PDF/A-3b should allow EmbeddedFiles: %v", e)
			}
		}
	})

	t.Run("no Names OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		errs := checkEmbeddedFiles(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error when no Names: %v", errs[0])
		}
	})
}

func TestCheckFontSubsets(t *testing.T) {
	t.Run("non-subset font OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		page := &Dictionary{}
		page.Set("Type", Name("Page"))
		page.Set("Parent", IndirectRef{Number: 2})
		page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
		page.Set("Resources", IndirectRef{Number: 12})

		fd := &Dictionary{}
		fd.Set("FontFile", &Stream{})

		font := &Dictionary{}
		font.Set("Type", Name("Font"))
		font.Set("Subtype", Name("Type1"))
		font.Set("BaseFont", Name("Helvetica"))
		font.Set("FontDescriptor", IndirectRef{Number: 13})

		fontDict := &Dictionary{}
		fontDict.Set("F1", IndirectRef{Number: 11})
		resources := &Dictionary{}
		resources.Set("Font", fontDict)

		pagesDict := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
		pagesDict.Set("Kids", Array{IndirectRef{Number: 10}})
		pagesDict.Set("Count", Integer(1))

		doc.Objects[10] = &IndirectObject{Number: 10, Value: page}
		doc.Objects[11] = &IndirectObject{Number: 11, Value: font}
		doc.Objects[12] = &IndirectObject{Number: 12, Value: resources}
		doc.Objects[13] = &IndirectObject{Number: 13, Value: fd}

		errs := checkFontSubsets(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for non-subset font: %v", errs[0])
		}
	})

	t.Run("skipped for PDFA2b", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkFontSubsets(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestCheckImplementationLimits(t *testing.T) {
	t.Run("normal objects OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkImplementationLimits(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for clean doc: %v", errs[0])
		}
	})

	t.Run("long name detected", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		longName := Name(strings.Repeat("A", 128))
		dict := &Dictionary{}
		dict.Set("Type", longName)
		doc.Objects[10] = &IndirectObject{Number: 10, Value: dict}

		errs := checkImplementationLimits(doc, PDFA2b)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "name length") {
				found = true
			}
		}
		if !found {
			t.Error("expected error for name exceeding 127 bytes")
		}
	})
}

func TestCheckOptionalContent(t *testing.T) {
	t.Run("no OCProperties OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		errs := checkOptionalContent(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("D without Name fails", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))

		dConfig := &Dictionary{}
		ocgs := Array{}
		ocProps := &Dictionary{}
		ocProps.Set("D", dConfig)
		ocProps.Set("OCGs", ocgs)
		catalog.Set("OCProperties", ocProps)

		errs := checkOptionalContent(doc, PDFA4)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "/Name") {
				found = true
			}
		}
		if !found {
			t.Error("expected error for missing /Name in default config")
		}
	})

	t.Run("non-PDFA4 skipped", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkOptionalContent(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestCheckInfoXMPConsistency(t *testing.T) {
	t.Run("no Info dict OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		errs := checkInfoXMPConsistency(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("non-PDFA1b skipped", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkInfoXMPConsistency(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestNormalizePDFDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"D:20240101120000Z", "2024-01-01T12:00:00Z"},
		{"D:20240615", "2024-06-15T00:00:00Z"},
		{"D:2024", "2024-01-01T00:00:00Z"},
		{"D:20240101120000+05'30'", "2024-01-01T12:00:00+05:30"},
		{"D:20221125132309+00'00'", "2022-11-25T13:23:09Z"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizePDFDate(tt.input)
			if got != tt.want {
				t.Errorf("normalizePDFDate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCheckTransparencyBlending(t *testing.T) {
	t.Run("no transparency OK", func(t *testing.T) {
		doc := NewPDFADocument(PDFA2b)
		errs := checkTransparencyBlending(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("PDFA1b skipped", func(t *testing.T) {
		doc := NewPDFADocument(PDFA1b)
		errs := checkTransparencyBlending(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("PDFA4 skipped", func(t *testing.T) {
		doc := NewPDFADocument(PDFA4)
		errs := checkTransparencyBlending(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestExtractXMPListValue(t *testing.T) {
	xmp := `<dc:title><rdf:Alt><rdf:li xml:lang="x-default">My Title</rdf:li></rdf:Alt></dc:title>`
	got := extractXMPListValue(xmp, "dc:title")
	if got != "My Title" {
		t.Errorf("extractXMPListValue = %q, want %q", got, "My Title")
	}
}

// --- test helpers ---

func hasRule(errs []ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func filterRule(errs []ValidationError, rule string) []ValidationError {
	var result []ValidationError
	for _, e := range errs {
		if e.Rule == rule {
			result = append(result, e)
		}
	}
	return result
}

// addTestPage inserts a page (object 20) into a NewPDFADocument's empty page
// tree and returns its dictionary for further mutation.
func addTestPage(doc *Document) *Dictionary {
	page := &Dictionary{}
	page.Set("Type", Name("Page"))
	page.Set("Parent", IndirectRef{Number: 2})
	page.Set("MediaBox", Array{Integer(0), Integer(0), Integer(612), Integer(792)})
	doc.Objects[20] = &IndirectObject{Number: 20, Value: page}
	pages := doc.Objects[2].Value.(*Dictionary)
	pages.Set("Kids", Array{IndirectRef{Number: 20}})
	pages.Set("Count", Integer(1))
	return page
}

// A7: Resolve must follow ref->ref chains and bail out on cycles.
func TestResolveChainsAndCycles(t *testing.T) {
	doc := &Document{Objects: map[int]*IndirectObject{
		1: {Number: 1, Value: IndirectRef{Number: 2}},
		2: {Number: 2, Value: IndirectRef{Number: 3}},
		3: {Number: 3, Value: Integer(42)},
		7: {Number: 7, Value: IndirectRef{Number: 8}},
		8: {Number: 8, Value: IndirectRef{Number: 7}},
	}}
	if v, ok := doc.Resolve(IndirectRef{Number: 1}).(Integer); !ok || v != 42 {
		t.Errorf("chained resolve: expected 42, got %#v", doc.Resolve(IndirectRef{Number: 1}))
	}
	if v := doc.Resolve(IndirectRef{Number: 7}); v != nil {
		t.Errorf("cyclic resolve: expected nil, got %#v", v)
	}
	if v, ok := doc.Resolve(Integer(5)).(Integer); !ok || v != 5 {
		t.Error("non-ref must resolve to itself")
	}
}

// A9: annotations written as direct dictionaries in a page's /Annots must be
// subject to the same checks as top-level annotation objects.
func TestValidatePDFA_DirectAnnotationsChecked(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	page := addTestPage(doc)

	annot := &Dictionary{}
	annot.Set("Subtype", Name("Screen")) // forbidden at 2b
	annot.Set("Rect", Array{Integer(0), Integer(0), Integer(10), Integer(10)})
	// no /F, no /AP: should also trip 6.3.2 and 6.3.3
	page.Set("Annots", Array{annot})

	errs := ValidatePDFA(doc, PDFA2b)
	for _, rule := range []string{"6.3.1", "6.3.2", "6.3.3"} {
		if !hasRule(errs, rule) {
			t.Errorf("expected %s error for direct-dict annotation, got %v", rule, errs)
		}
	}
}

// A9: direct annotations with direct forbidden actions must be flagged.
func TestValidatePDFA_DirectAnnotationForbiddenAction(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	page := addTestPage(doc)

	action := &Dictionary{}
	action.Set("S", Name("Launch"))
	annot := &Dictionary{}
	annot.Set("Subtype", Name("Link"))
	annot.Set("Rect", Array{Integer(0), Integer(0), Integer(10), Integer(10)})
	annot.Set("F", Integer(4))
	annot.Set("A", action)
	page.Set("Annots", Array{annot})

	errs := ValidatePDFA(doc, PDFA2b)
	if !hasRule(errs, "6.6.1") {
		t.Errorf("expected 6.6.1 error for direct annotation's Launch action, got %v", errs)
	}
}

// A13: Separation/DeviceN rules must fire when Resources is a direct
// dictionary on the page (the common case).
func TestValidatePDFA_SeparationInDirectResources(t *testing.T) {
	doc := NewPDFADocument(PDFA2b)
	page := addTestPage(doc)

	// DeviceN with 33 colorants exceeds the PDF/A-2 limit of 32.
	var colorants Array
	for i := 0; i < 33; i++ {
		colorants = append(colorants, Name(fmt.Sprintf("C%d", i)))
	}
	deviceN := Array{Name("DeviceN"), colorants, Name("DeviceCMYK"), IndirectRef{Number: 5}}
	csDict := &Dictionary{}
	csDict.Set("CS0", deviceN)
	resources := &Dictionary{}
	resources.Set("ColorSpace", csDict)
	page.Set("Resources", resources) // direct, not an indirect object

	errs := ValidatePDFA(doc, PDFA2b)
	if !hasRule(errs, "6.2.4") {
		t.Errorf("expected 6.2.4 error for 33-colorant DeviceN in direct Resources, got %v", errs)
	}
}

// A15: q/Q bytes inside string literals are data, not operators.
func TestQNestingIgnoresStrings(t *testing.T) {
	var content bytes.Buffer
	content.WriteString("BT (")
	for i := 0; i < 40; i++ {
		content.WriteString("q ")
	}
	content.WriteString(") Tj ET\nq Q\n")
	if d := qNestingMaxDepth(content.Bytes()); d != 1 {
		t.Errorf("expected depth 1 (string content ignored), got %d", d)
	}

	// Real nesting still counts, including with delimiters after operators.
	real := []byte("q q q(x)Tj Q Q Q")
	if d := qNestingMaxDepth(real); d != 3 {
		t.Errorf("expected depth 3, got %d", d)
	}

	// Inline image binary containing 'q' bytes is skipped.
	img := []byte("q BI /W 1 /H 1 ID q q q q\x00\xff EI Q")
	if d := qNestingMaxDepth(img); d != 1 {
		t.Errorf("expected depth 1 (inline image ignored), got %d", d)
	}
}

// Separation tint transforms: equal-by-content duplicates are conformant;
// genuinely different transforms for the same colorant are not.
func TestValidatePDFA_TintTransformConsistency(t *testing.T) {
	build := func(fn2Body Object) *Document {
		doc := NewPDFADocument(PDFA2b)
		page := addTestPage(doc)

		fn := &Dictionary{}
		fn.Set("FunctionType", Integer(2))
		fn.Set("Domain", Array{Integer(0), Integer(1)})
		fn.Set("N", Integer(1))
		doc.Objects[30] = &IndirectObject{Number: 30, Value: fn}
		doc.Objects[31] = &IndirectObject{Number: 31, Value: fn2Body}

		sep1 := Array{Name("Separation"), Name("Spot"), Name("DeviceCMYK"), IndirectRef{Number: 30}}
		sep2 := Array{Name("Separation"), Name("Spot"), Name("DeviceCMYK"), IndirectRef{Number: 31}}
		csDict := &Dictionary{}
		csDict.Set("CS0", sep1)
		csDict.Set("CS1", sep2)
		resources := &Dictionary{}
		resources.Set("ColorSpace", csDict)
		page.Set("Resources", resources)
		return doc
	}

	identical := &Dictionary{}
	identical.Set("FunctionType", Integer(2))
	identical.Set("Domain", Array{Integer(0), Integer(1)})
	identical.Set("N", Integer(1))
	errs := filterRule(ValidatePDFA(build(identical), PDFA2b), "6.2.4")
	if len(errs) > 0 {
		t.Errorf("identical tint transforms in different objects must pass, got %v", errs)
	}

	different := &Dictionary{}
	different.Set("FunctionType", Integer(2))
	different.Set("Domain", Array{Integer(0), Integer(1)})
	different.Set("N", Integer(2))
	if !hasRule(ValidatePDFA(build(different), PDFA2b), "6.2.4") {
		t.Error("differing tint transforms for the same colorant must be flagged")
	}
}
