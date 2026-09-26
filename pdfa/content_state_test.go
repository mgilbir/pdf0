package pdfa

import (
	"testing"

	"github.com/mgilbir/pdf0/object"
)

// End-to-end checks of the rules that read the content interpreter
// (internal/core/interp.go): the font-embedding rule reads which fonts are
// shown visibly, and the device-colour rule which colour spaces are used.

// fontEmbeddingFindings validates a PDF/A-2b skeleton whose one page runs
// content with a non-embedded Helvetica as /F1, and returns the embedding
// findings.
func fontEmbeddingFindings(t *testing.T, content string) []Violation {
	t.Helper()
	v := mkPDFAViewT(t, PDFA2b)
	page := addTestPage(v)
	font := object.NewDictionary(
		object.Entry{Key: "Type", Value: object.Name("Font")},
		object.Entry{Key: "Subtype", Value: object.Name("Type1")},
		object.Entry{Key: "BaseFont", Value: object.Name("Helvetica")},
		object.Entry{Key: "Encoding", Value: object.Name("WinAnsiEncoding")},
	)
	v.Objects[21] = &object.IndirectObject{Number: 21, Value: font}
	cs := object.NewStream(object.NewDictionary(object.Entry{Key: "Length", Value: object.Integer(len(content))}), []byte(content))
	v.Objects[22] = &object.IndirectObject{Number: 22, Value: cs}
	page.Set("Contents", object.IndirectRef{Number: 22})
	page.Set("Resources", dictWith("Font", dictWith("F1", object.IndirectRef{Number: 21})))
	var out []Violation
	for _, e := range ValidateView(v, PDFA2b, nil) {
		if e.Rule == fontClause("embed", PDFA2b) {
			out = append(out, e)
		}
	}
	return out
}

// TestInvisibleModeEndsAtQ is C74's scenario, end to end. Text in render mode
// 3 is invisible and its font need not be embedded; "q 3 Tr Q" leaves the mode
// it found, so the text after it is visible and its non-embedded font is a
// violation. The mode used to be a running value that Q did not restore, which
// OCR layers — which wrap 3 Tr in q/Q — turned into a way past the rule.
func TestInvisibleModeEndsAtQ(t *testing.T) {
	if got := fontEmbeddingFindings(t, "BT /F1 12 Tf (y) Tj ET"); len(got) != 1 {
		t.Fatalf("visible text in a non-embedded font: %d embedding findings, want 1: %v", len(got), got)
	}
	if got := fontEmbeddingFindings(t, "q 3 Tr Q BT /F1 12 Tf (y) Tj ET"); len(got) != 1 {
		t.Errorf("q 3 Tr Q then visible text: %d embedding findings, want 1: %v", len(got), got)
	}
	if got := fontEmbeddingFindings(t, "BT 3 Tr /F1 12 Tf (y) Tj ET"); len(got) != 0 {
		t.Errorf("invisible text only: %d embedding findings, want none: %v", len(got), got)
	}
}
