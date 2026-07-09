package pdf0

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func countEncryptViolations(errs []ValidationError) int {
	n := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "/Encrypt") {
			n++
		}
	}
	return n
}

// TestRepairCatalogAA removes a forbidden catalog /AA and confirms the violation
// is gone afterwards.
func TestRepairCatalogAA(t *testing.T) {
	base := buildMinimalPDF()
	doc, err := Read(bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("AA", &Dictionary{})

	before := 0
	for _, e := range ValidatePDFA(doc, PDFA2b) {
		if strings.Contains(e.Message, "catalog") && strings.Contains(e.Message, "/AA") {
			before++
		}
	}
	if before == 0 {
		t.Fatal("expected a catalog /AA violation before repair")
	}
	actions := doc.Repair(PDFA2b)
	if len(actions) == 0 {
		t.Error("Repair reported no actions")
	}
	for _, e := range ValidatePDFA(doc, PDFA2b) {
		if strings.Contains(e.Message, "catalog") && strings.Contains(e.Message, "/AA") {
			t.Error("catalog /AA violation still present after repair")
		}
	}
}

// TestRepairEncryption decrypts an encrypted corpus file, repairs away the
// encryption, and confirms the /Encrypt violation is gone.
func TestRepairEncryption(t *testing.T) {
	corpus := os.Getenv("VERAPDF_CORPUS")
	if corpus == "" {
		corpus = "testdata/verapdf-corpus"
	}
	if _, err := os.Stat(corpus); err != nil {
		t.Skip("corpus not present")
	}
	p := findCorpusFile(corpus, "PDF_A-2b/6.1 File structure/6.1.3 File trailer/veraPDF test suite 6-1-3-t02-fail-a")
	if p == "" {
		t.Skip("encrypted sample not found")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.security == nil {
		t.Skip("file did not decrypt")
	}
	if countEncryptViolations(ValidatePDFA(doc, PDFA2b)) == 0 {
		t.Fatal("expected an /Encrypt violation before repair")
	}
	doc.Repair(PDFA2b)
	if n := countEncryptViolations(ValidatePDFA(doc, PDFA2b)); n != 0 {
		t.Errorf("%d /Encrypt violation(s) remain after repair", n)
	}
	// A repaired document must be writable (no longer refused as encrypted).
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Errorf("repaired document is not writable: %v", err)
	}
}
