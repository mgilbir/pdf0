package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLevelACorpus is the FP=0 guard for Level A: validating a Level A corpus
// file at its Level A conformance level must not add any finding beyond what the
// corresponding Level B validation already reports (the Level A rule families
// must not false-positive on conforming files). Gated on the veraPDF corpus.
func TestLevelACorpus(t *testing.T) {
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); err != nil {
		t.Skip("veraPDF corpus not found; run `make corpus`")
	}
	cases := []struct {
		dir  string
		a, b PDFALevel
	}{
		{"PDF_A-1a", PDFA1a, PDFA1b},
		{"PDF_A-2a", PDFA2a, PDFA2b},
	}
	for _, tc := range cases {
		files, _ := filepath.Glob(filepath.Join(corpus, tc.dir, "**", "**", "*.pdf"))
		if len(files) == 0 {
			continue
		}
		aFP := 0
		for _, f := range files {
			base := filepath.Base(f)
			if !strings.Contains(base, "-pass-") {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			doc, err := Read(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				continue
			}
			bmsgs := map[string]bool{}
			for _, e := range ValidatePDFABytes(doc, tc.b, data) {
				bmsgs[e.Rule+e.Message] = true
			}
			for _, e := range ValidatePDFABytes(doc, tc.a, data) {
				if !bmsgs[e.Rule+e.Message] {
					aFP++
					t.Errorf("%s [%s]: Level A false positive: %s %s", base, tc.a, e.Rule, e.Message)
				}
			}
		}
		if aFP == 0 {
			t.Logf("%s: Level A adds no false positives on conforming files", tc.dir)
		}
	}
}

// levelADoc builds a minimal document with the catalog entries the Level A
// checks read: optional MarkInfo/Marked, StructTreeRoot, Lang, and an XMP
// metadata stream declaring the given pdfaid:conformance.
func levelADoc(marked, structTree bool, lang, conformance string) *Document {
	d := &Document{Objects: map[int]*IndirectObject{}, Version: "1.4"}
	cat := &Dictionary{}
	cat.Set("Type", Name("Catalog"))
	if marked {
		mi := &Dictionary{}
		mi.Set("Marked", Boolean(true))
		cat.Set("MarkInfo", mi)
	}
	if structTree {
		cat.Set("StructTreeRoot", IndirectRef{Number: 9})
	}
	if lang != "" {
		cat.Set("Lang", String{Value: []byte(lang)})
	}
	if conformance != "" {
		xmp := `<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">` +
			`<rdf:Description rdf:about="" xmlns:pdfaid="http://www.aiim.org/pdfa/ns/id/">` +
			`<pdfaid:part>1</pdfaid:part><pdfaid:conformance>` + conformance + `</pdfaid:conformance>` +
			`</rdf:Description></rdf:RDF></x:xmpmeta>`
		ms := &Stream{Dict: Dictionary{}, Data: []byte(xmp)}
		ms.Dict.Set("Type", Name("Metadata"))
		ms.Dict.Set("Subtype", Name("XML"))
		d.Objects[2] = &IndirectObject{Number: 2, Value: ms}
		cat.Set("Metadata", IndirectRef{Number: 2})
	}
	d.Objects[1] = &IndirectObject{Number: 1, Value: cat}
	d.Trailer = Dictionary{}
	d.Trailer.Set("Root", IndirectRef{Number: 1})
	return d
}

func hasMsg(errs []ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func TestLevelAStructureCheck(t *testing.T) {
	// Tagged with a structure tree: no structure finding.
	if v := checkLevelAStructure(levelADoc(true, true, "en", "A"), PDFA1a); len(v) != 0 {
		t.Errorf("tagged document flagged: %v", v)
	}
	// Not marked as tagged.
	if v := checkLevelAStructure(levelADoc(false, true, "en", "A"), PDFA1a); !hasMsg(v, "Tagged PDF") {
		t.Errorf("expected a Tagged-PDF finding; got %v", v)
	}
	// No structure tree.
	if v := checkLevelAStructure(levelADoc(true, false, "en", "A"), PDFA1a); !hasMsg(v, "logical structure tree") {
		t.Errorf("expected a structure-tree finding; got %v", v)
	}
}

func TestLevelAConformanceCheck(t *testing.T) {
	if v := checkLevelAConformance(levelADoc(true, true, "en", "A"), PDFA1a); len(v) != 0 {
		t.Errorf("conformance A flagged: %v", v)
	}
	if v := checkLevelAConformance(levelADoc(true, true, "en", "B"), PDFA1a); !hasMsg(v, "must be A") {
		t.Errorf("expected a conformance finding for B at Level A; got %v", v)
	}
}

func TestLevelALanguageCheck(t *testing.T) {
	// A valid tag (including a UTF-16BE-encoded one) is accepted.
	if v := checkLevelALanguage(levelADoc(true, true, "en-GB", ""), PDFA1a); len(v) != 0 {
		t.Errorf("valid /Lang flagged: %v", v)
	}
	utf16 := append([]byte{0xFE, 0xFF}, utf16be("en-GB")[2:]...)
	if v := checkLevelALanguage(levelADoc(true, true, string(utf16), ""), PDFA1a); len(v) != 0 {
		t.Errorf("valid UTF-16 /Lang flagged: %v", v)
	}
	// A syntactically invalid tag is flagged.
	if v := checkLevelALanguage(levelADoc(true, true, "not a tag!", ""), PDFA1a); !hasMsg(v, "not a valid language") {
		t.Errorf("expected an invalid-/Lang finding; got %v", v)
	}
}

// TestLevelAString checks the new level constants stringify correctly.
func TestLevelAString(t *testing.T) {
	for lvl, want := range map[PDFALevel]string{PDFA1a: "PDF/A-1a", PDFA2a: "PDF/A-2a", PDFA3a: "PDF/A-3a"} {
		if got := lvl.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", lvl, got, want)
		}
		if !lvl.isA() {
			t.Errorf("%s should be a Level A level", want)
		}
	}
}
