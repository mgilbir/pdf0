package pdf0

import (
	"bytes"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
	"os"
	"strings"
	"testing"
)

func countEncryptViolations(errs []pdfa.Violation) int {
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
	cat.Set("AA", &object.Dictionary{})

	before := 0
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if strings.Contains(e.Message, "catalog") && strings.Contains(e.Message, "/AA") {
			before++
		}
	}
	if before == 0 {
		t.Fatal("expected a catalog /AA violation before repair")
	}
	actions, err := doc.Repair(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) == 0 {
		t.Error("Repair reported no actions")
	}
	for _, e := range ValidatePDFA(doc, pdfa.PDFA2b) {
		if strings.Contains(e.Message, "catalog") && strings.Contains(e.Message, "/AA") {
			t.Error("catalog /AA violation still present after repair")
		}
	}
}

// TestRepairEncryption decrypts an encrypted corpus file, repairs away the
// encryption, and confirms the /Encrypt violation is gone.
func TestRepairEncryption(t *testing.T) {
	p := corpusFileNamed(t, "PDF_A-2b/6.1 File structure/6.1.3 File trailer/veraPDF test suite 6-1-3-t02-fail-a")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.security == nil {
		t.Fatal("the pinned encrypted fixture did not decrypt with the empty password")
	}
	if countEncryptViolations(ValidatePDFA(doc, pdfa.PDFA2b)) == 0 {
		t.Fatal("expected an /Encrypt violation before repair")
	}
	if _, err := doc.Repair(pdfa.PDFA2b); err != nil {
		t.Fatal(err)
	}
	if n := countEncryptViolations(ValidatePDFA(doc, pdfa.PDFA2b)); n != 0 {
		t.Errorf("%d /Encrypt violation(s) remain after repair", n)
	}
	// A repaired document must be writable (no longer refused as encrypted).
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Errorf("repaired document is not writable: %v", err)
	}
}

// TestRepairReportsWhatItDoesToTheFile holds the two fixes Repair's
// documentation names besides the additional actions (docs audit 2026-10-11
// D16): it removes the encryption of a document Read decrypted, so the next
// Write is plaintext, and it adds a missing /ID. Each is a RepairAction.
func TestRepairReportsWhatItDoesToTheFile(t *testing.T) {
	has := func(actions []RepairAction, desc string) bool {
		for _, a := range actions {
			if a.Description == desc {
				return true
			}
		}
		return false
	}
	t.Run("encryption", func(t *testing.T) {
		doc := readRaw(t, textAndImagePage())
		if err := doc.SetEncryption("user", "owner"); err != nil {
			t.Fatal(err)
		}
		var enc bytes.Buffer
		if err := doc.Write(&enc); err != nil {
			t.Fatal(err)
		}
		opened, err := ReadWithPassword(bytes.NewReader(enc.Bytes()), int64(enc.Len()), "user")
		if err != nil || opened.Locked() {
			t.Fatalf("ReadWithPassword: %v, locked %v", err, opened.Locked())
		}
		actions, err := opened.Repair(pdfa.PDFA2b)
		if err != nil {
			t.Fatal(err)
		}
		if !has(actions, "removed document encryption (/Encrypt)") {
			t.Errorf("actions %v do not report the encryption removed", actions)
		}
		var out bytes.Buffer
		if err := opened.Write(&out); err != nil {
			t.Fatal(err)
		}
		plain := readRaw(t, out.Bytes())
		if plain.Encrypted || plain.Locked() || plain.Trailer.Has("Encrypt") {
			t.Fatal("the repaired document was written encrypted")
		}
		if text, err := plain.ExtractText(); err != nil || !strings.Contains(text, "Hello") {
			t.Errorf("the repaired file opens without a password to %q, %v; want its text", text, err)
		}
	})
	t.Run("file identifier", func(t *testing.T) {
		doc := readRaw(t, buildMinimalPDF())
		if doc.Trailer.Has("ID") {
			t.Fatal("the fixture already has an /ID")
		}
		actions, err := doc.Repair(pdfa.PDFA2b)
		if err != nil {
			t.Fatal(err)
		}
		if !has(actions, "added a missing file identifier (/ID)") || !doc.Trailer.Has("ID") {
			t.Errorf("actions %v, /ID present %v; want the /ID added and reported", actions, doc.Trailer.Has("ID"))
		}
	})
}
