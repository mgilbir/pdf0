package pdf0

import (
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/pdfa"
)

// TestInlineImageDeclaredLengthOverflow is audit 2026-09-22 C16. The inline
// image skipper trusts a declared /L to jump over the sample data, and the
// length was accumulated with no bound: /L 9223372036854775807 plus the data
// offset wrapped negative, passed the "end <= len" check, and indexed the
// content stream before its start. ExtractText panicked; ValidatePDFA and
// ValidatePDFUA recovered, which turned every tokenizer-based check on the
// page into an "internal" finding, so the file was effectively unvalidated.
//
// The declared length is now read by checked.Decimal and used only when it
// lies inside the stream; otherwise the skipper falls back to the EI search.
// Both runs of text are therefore extracted — the one after the image proves
// the tokenizer resynchronised past it — and no validator reports an
// internal error.
func TestInlineImageDeclaredLengthOverflow(t *testing.T) {
	for _, name := range []string{"inline-image-L-maxint", "inline-image-L-overflow"} {
		t.Run(name, func(t *testing.T) {
			doc := readHostile(t, name)

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("ExtractText panicked: %v", r)
					}
				}()
				text := doc.ExtractText()
				if !strings.Contains(text, "hi") || !strings.Contains(text, "there") {
					t.Errorf("ExtractText = %q, want both runs of text", text)
				}
			}()

			for _, v := range ValidatePDFA(doc, pdfa.PDFA2b) {
				if v.Rule == "internal" {
					t.Errorf("ValidatePDFA: %s", v.Message)
				}
			}
			for _, v := range ValidatePDFUA(doc) {
				if v.Clause == "internal" {
					t.Errorf("ValidatePDFUA: %s", v.Message)
				}
			}
		})
	}
}
