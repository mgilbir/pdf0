package pdfa

import (
	"bytes"
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"strings"
	"testing"
)

func TestNewPDFADocument(t *testing.T) {
	for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
		t.Run(level.String(), func(t *testing.T) {
			doc := mkPDFAView(level)
			if len(doc.Objects) == 0 {
				t.Fatal("Skeleton returned an empty object graph")
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

			errs := ValidateView(doc, level, nil)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("validation error: %v", e)
				}
			}
		})
	}
}

func TestValidatePDFA_LZW(t *testing.T) {
	t.Run("all levels reject LZW", func(t *testing.T) {
		for _, level := range []PDFALevel{PDFA1b, PDFA2b, PDFA3b, PDFA4} {
			t.Run(level.String(), func(t *testing.T) {
				doc := mkPDFAView(level)
				stream := &object.Stream{Dict: object.Dictionary{}, Data: []byte("test")}
				stream.Dict.Set("Filter", object.Name("LZWDecode"))
				stream.Dict.Set("Length", object.Integer(4))
				doc.Objects[10] = &object.IndirectObject{Number: 10, Value: stream}

				errs := ValidateView(doc, level, nil)
				if !hasRule(errs, filterClause(level)) {
					t.Errorf("expected %s error for LZW filter in %s", filterClause(level), level)
				}
			})
		}
	})
}

func TestValidatePDFA_AnnotationSubtypes(t *testing.T) {
	forbidden := []struct {
		subtype object.Name
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
			doc := mkPDFAView(tt.level)
			annot := &object.Dictionary{}
			annot.Set("Type", object.Name("Annot"))
			annot.Set("Subtype", tt.subtype)
			annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
			annot.Set("F", object.Integer(4))
			annot.Set("AP", &object.Dictionary{Keys: []object.Name{"N"}, Values: []object.Object{&object.Stream{}}})
			doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

			errs := ValidateView(doc, tt.level, nil)
			if !hasRule(errs, annotActionClause("subtype", tt.level)) {
				t.Errorf("expected 6.3.1 error for forbidden subtype /%s", tt.subtype)
			}
		})
	}

	t.Run("allowed subtypes pass", func(t *testing.T) {
		allowed := []object.Name{"Text", "Link", "FreeText", "Widget", "Popup", "Stamp", "FileAttachment"}
		for _, st := range allowed {
			doc := mkPDFAView(PDFA4)
			annot := &object.Dictionary{}
			annot.Set("Type", object.Name("Annot"))
			annot.Set("Subtype", st)
			annot.Set("Rect", object.Array{object.Integer(0), object.Integer(0), object.Integer(100), object.Integer(100)})
			annot.Set("F", object.Integer(4))
			annot.Set("AP", &object.Dictionary{Keys: []object.Name{"N"}, Values: []object.Object{&object.Stream{}}})
			doc.Objects[10] = &object.IndirectObject{Number: 10, Value: annot}

			errs := filterRule(ValidateView(doc, PDFA4, nil), "6.3.1")
			if len(errs) > 0 {
				t.Errorf("subtype /%s should be allowed in PDF/A-4", st)
			}
		}
	})
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

func TestCheckCatalogVersion(t *testing.T) {
	t.Run("no catalog version OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA4)
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("valid 2.0 OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("Version", object.Name("2.0"))
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("invalid 1.7 fails", func(t *testing.T) {
		doc := mkPDFAView(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		catalog.Set("Version", object.Name("1.7"))
		errs := checkCatalogVersion(doc, PDFA4)
		if len(errs) == 0 {
			t.Error("expected error for catalog version 1.7")
		}
	})

	t.Run("non-PDFA4 skipped", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		errs := checkCatalogVersion(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for non-PDFA4: %v", errs[0])
		}
	})
}

func TestCheckExtGState(t *testing.T) {
	t.Run("TR forbidden", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		gs := &object.Dictionary{}
		gs.Set("TR", object.Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) == 0 {
			t.Error("expected error for /TR in ExtGState")
		}
	})

	t.Run("TR2 Default OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		gs := &object.Dictionary{}
		gs.Set("TR2", object.Name("Default"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("TR2 non-Default forbidden", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		gs := &object.Dictionary{}
		gs.Set("TR2", object.Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA2b)
		if len(errs) == 0 {
			t.Error("expected error for /TR2 non-Default in ExtGState")
		}
	})

	t.Run("TR forbidden at PDFA1b under 6.2.8", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		gs := &object.Dictionary{}
		gs.Set("TR", object.Name("Identity"))
		addExtGStateToDoc(doc, gs)

		errs := checkExtGState(doc, PDFA1b)
		if len(errs) == 0 {
			t.Fatal("expected /TR error at PDF/A-1b (ISO 19005-1, 6.2.8)")
		}
		if errs[0].Rule != "6.2.8" {
			t.Errorf("expected rule 6.2.8 at 1b, got %s", errs[0].Rule)
		}
	})
}

func TestCheckEmbeddedFiles(t *testing.T) {
	t.Run("PDFA-1b rejects embedded files", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &object.Dictionary{}
		namesDict.Set("EmbeddedFiles", &object.Dictionary{})
		catalog.Set("Names", namesDict)

		errs := checkEmbeddedFiles(doc, PDFA1b)
		if len(errs) == 0 {
			t.Error("expected error for EmbeddedFiles in PDF/A-1b")
		}
	})

	t.Run("PDFA-2b allows embedded files", func(t *testing.T) {
		// ISO 19005-2 permits embedded files (they must themselves be
		// PDF/A, which is not machine-checkable here).
		doc := mkPDFAView(PDFA2b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &object.Dictionary{}
		namesDict.Set("EmbeddedFiles", &object.Dictionary{})
		catalog.Set("Names", namesDict)

		for _, e := range checkEmbeddedFiles(doc, PDFA2b) {
			if strings.Contains(e.Message, "must not be present") {
				t.Errorf("PDF/A-2b should allow EmbeddedFiles: %v", e)
			}
		}
	})

	t.Run("PDFA-3b allows embedded files with requirements", func(t *testing.T) {
		doc := mkPDFAView(PDFA3b)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
		namesDict := &object.Dictionary{}
		namesDict.Set("EmbeddedFiles", &object.Dictionary{})
		catalog.Set("Names", namesDict)
		catalog.Set("AF", object.Array{})

		errs := checkEmbeddedFiles(doc, PDFA3b)
		// Should not complain about embedded files existing
		for _, e := range errs {
			if strings.Contains(e.Message, "must not be present") {
				t.Errorf("PDF/A-3b should allow EmbeddedFiles: %v", e)
			}
		}
	})

	t.Run("no Names OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		errs := checkEmbeddedFiles(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error when no Names: %v", errs[0])
		}
	})
}

func TestCheckFontSubsets(t *testing.T) {
	t.Run("non-subset font OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		page := &object.Dictionary{}
		page.Set("Type", object.Name("Page"))
		page.Set("Parent", object.IndirectRef{Number: 2})
		page.Set("MediaBox", object.Array{object.Integer(0), object.Integer(0), object.Integer(612), object.Integer(792)})
		page.Set("Resources", object.IndirectRef{Number: 12})

		fd := &object.Dictionary{}
		fd.Set("FontFile", &object.Stream{})

		font := &object.Dictionary{}
		font.Set("Type", object.Name("Font"))
		font.Set("Subtype", object.Name("Type1"))
		font.Set("BaseFont", object.Name("Helvetica"))
		font.Set("FontDescriptor", object.IndirectRef{Number: 13})

		fontDict := &object.Dictionary{}
		fontDict.Set("F1", object.IndirectRef{Number: 11})
		resources := &object.Dictionary{}
		resources.Set("Font", fontDict)

		pagesDict := doc.ResolveDict(doc.ResolveDict(doc.Trailer.Get("Root")).Get("Pages"))
		pagesDict.Set("Kids", object.Array{object.IndirectRef{Number: 10}})
		pagesDict.Set("Count", object.Integer(1))

		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: page}
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: font}
		doc.Objects[12] = &object.IndirectObject{Number: 12, Value: resources}
		doc.Objects[13] = &object.IndirectObject{Number: 13, Value: fd}

		errs := checkFontSubsets(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for non-subset font: %v", errs[0])
		}
	})

	t.Run("skipped for PDFA2b", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		errs := checkFontSubsets(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestCheckImplementationLimits(t *testing.T) {
	t.Run("normal objects OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		errs := checkImplementationLimits(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error for clean doc: %v", errs[0])
		}
	})

	t.Run("long name detected", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
		longName := object.Name(strings.Repeat("A", 128))
		dict := &object.Dictionary{}
		dict.Set("Type", longName)
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: dict}

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
		doc := mkPDFAView(PDFA4)
		errs := checkOptionalContent(doc, PDFA4)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("D without Name fails", func(t *testing.T) {
		doc := mkPDFAView(PDFA4)
		catalog := doc.ResolveDict(doc.Trailer.Get("Root"))

		dConfig := &object.Dictionary{}
		ocgs := object.Array{}
		ocProps := &object.Dictionary{}
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
		doc := mkPDFAView(PDFA2b)
		errs := checkOptionalContent(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})
}

func TestCheckInfoXMPConsistency(t *testing.T) {
	t.Run("no Info dict OK", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		errs := checkInfoXMPConsistency(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("non-PDFA1b skipped", func(t *testing.T) {
		doc := mkPDFAView(PDFA2b)
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
		doc := mkPDFAView(PDFA2b)
		errs := checkTransparencyBlending(doc, PDFA2b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("PDFA1b skipped", func(t *testing.T) {
		doc := mkPDFAView(PDFA1b)
		errs := checkTransparencyBlending(doc, PDFA1b)
		if len(errs) > 0 {
			t.Errorf("unexpected error: %v", errs[0])
		}
	})

	t.Run("PDFA4 skipped", func(t *testing.T) {
		doc := mkPDFAView(PDFA4)
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

// A15: q/Q bytes inside string literals are data, not operators.
func TestQNestingIgnoresStrings(t *testing.T) {
	var content bytes.Buffer
	content.WriteString("BT (")
	for i := 0; i < 40; i++ {
		content.WriteString("q ")
	}
	content.WriteString(") Tj ET\nq Q\n")
	if d := qNestingMaxDepth(core.Canceler{}, content.Bytes()); d != 1 {
		t.Errorf("expected depth 1 (string content ignored), got %d", d)
	}

	// object.Real nesting still counts, including with delimiters after operators.
	real := []byte("q q q(x)Tj Q Q Q")
	if d := qNestingMaxDepth(core.Canceler{}, real); d != 3 {
		t.Errorf("expected depth 3, got %d", d)
	}

	// Inline image binary containing 'q' bytes is skipped.
	img := []byte("q BI /W 1 /H 1 ID q q q q\x00\xff EI Q")
	if d := qNestingMaxDepth(core.Canceler{}, img); d != 1 {
		t.Errorf("expected depth 1 (inline image ignored), got %d", d)
	}
}

// A18: PDF/A-2/3 accept any 1.x header; only A-4 requires 2.x.
func TestValidatePDFA_HeaderEarlyVersionsAllowed(t *testing.T) {
	for _, v := range []string{"1.0", "1.3", "1.7"} {
		doc := mkPDFAView(PDFA2b)
		doc.Version = v
		if hasRule(checkHeader(doc, PDFA2b), "6.1.2") {
			t.Errorf("header %s must be legal at PDF/A-2b", v)
		}
	}
	doc := mkPDFAView(PDFA2b)
	doc.Version = "2.0"
	if !hasRule(checkHeader(doc, PDFA2b), "6.1.2") {
		t.Error("header 2.0 must be rejected at PDF/A-2b")
	}
}

// A14: implementation limits are 6.1.12 at A-1, 6.1.13 at A-2/3, absent at A-4.
func TestValidatePDFA_ImplementationLimitLevels(t *testing.T) {
	longName := object.Name(strings.Repeat("x", 200))
	mk := func(level PDFALevel) core.View {
		doc := mkPDFAView(level)
		d := &object.Dictionary{}
		d.Set("K", longName)
		doc.Objects[40] = &object.IndirectObject{Number: 40, Value: d}
		return doc
	}
	if errs := checkImplementationLimits(mk(PDFA1b), PDFA1b); !hasRule(errs, "6.1.12") {
		t.Errorf("expected 6.1.12 name-length error at 1b, got %v", errs)
	}
	if errs := checkImplementationLimits(mk(PDFA2b), PDFA2b); !hasRule(errs, "6.1.13") {
		t.Errorf("expected 6.1.13 name-length error at 2b, got %v", errs)
	}
	if errs := checkImplementationLimits(mk(PDFA4), PDFA4); len(errs) > 0 {
		t.Errorf("PDF/A-4 has no implementation limits, got %v", errs)
	}

	// object.Real magnitude limit at 1b (PDF 1.4 Annex C).
	doc := mkPDFAView(PDFA1b)
	d := &object.Dictionary{}
	d.Set("V", object.Real(40000))
	doc.Objects[41] = &object.IndirectObject{Number: 41, Value: d}
	if errs := checkImplementationLimits(doc, PDFA1b); !hasRule(errs, "6.1.12") {
		t.Errorf("expected real-magnitude error at 1b, got %v", errs)
	}
}

// A20: 1b ExtGState /TR2 rule.
func TestValidatePDFA_ExtGStateTR2At1b(t *testing.T) {
	doc := mkPDFAView(PDFA1b)
	gs := &object.Dictionary{}
	gs.Set("TR2", object.Name("Identity"))
	addExtGStateToDoc(doc, gs)
	errs := checkExtGState(doc, PDFA1b)
	if !hasRule(errs, "6.2.8") {
		t.Errorf("expected 6.2.8 error for /TR2 at 1b, got %v", errs)
	}
}

// A26: PDF/A-1 forbids /EF on any file specification, not only Names-tree ones.
func TestValidatePDFA_EFAnywhereForbiddenAt1b(t *testing.T) {
	doc := mkPDFAView(PDFA1b)
	fs := &object.Dictionary{}
	fs.Set("Type", object.Name("Filespec"))
	fs.Set("F", object.String{Value: []byte("x.txt")})
	fs.Set("EF", &object.Dictionary{})
	doc.Objects[50] = &object.IndirectObject{Number: 50, Value: fs}
	if !hasRule(checkEmbeddedFiles(doc, PDFA1b), "6.1.11") {
		t.Error("expected 6.1.11 error for /EF filespec at 1b")
	}
}

// A32: a PDF/X-only OutputIntents array is legal when no device color needs
// coverage, but multiple intents with different profiles are not.
func TestValidatePDFA_OutputIntentRules(t *testing.T) {
	doc := mkPDFAView(PDFA2b)
	catalog := doc.ResolveDict(doc.Trailer.Get("Root"))
	pdfx := &object.Dictionary{}
	pdfx.Set("Type", object.Name("OutputIntent"))
	pdfx.Set("S", object.Name("GTS_PDFX"))
	pdfx.Set("OutputConditionIdentifier", object.String{Value: []byte("CGATS TR 001")})
	catalog.Set("OutputIntents", object.Array{pdfx})
	if hasRule(checkOutputIntents(doc, PDFA2b), "6.2.3") {
		t.Error("PDF/X-only OutputIntents must be legal")
	}

	// Two intents with different DestOutputProfile objects.
	doc2 := mkPDFAView(PDFA2b)
	catalog2 := doc2.ResolveDict(doc2.Trailer.Get("Root"))
	i1 := &object.Dictionary{}
	i1.Set("Type", object.Name("OutputIntent"))
	i1.Set("S", object.Name("GTS_PDFA1"))
	i1.Set("OutputConditionIdentifier", object.String{Value: []byte("c")})
	i1.Set("DestOutputProfile", object.IndirectRef{Number: 5})
	i2 := &object.Dictionary{}
	i2.Set("Type", object.Name("OutputIntent"))
	i2.Set("S", object.Name("GTS_PDFX"))
	i2.Set("OutputConditionIdentifier", object.String{Value: []byte("c")})
	i2.Set("DestOutputProfile", object.IndirectRef{Number: 6})
	doc2.Objects[6] = &object.IndirectObject{Number: 6, Value: &object.Stream{}}
	catalog2.Set("OutputIntents", object.Array{i1, i2})
	if !hasRule(checkOutputIntents(doc2, PDFA2b), "6.2.3") {
		t.Error("differing DestOutputProfile objects across intents must be flagged")
	}
}
