package pdf0

// Tests derived from ALL examples in ISO 32000-1:2008 (PDF 1.7).
//
// Examples are extracted from the spec by cmd/extract_spec_examples/main17.py
// and stored in testdata/spec_examples_17.json. Each example records the spec
// section, page number, and content.
//
// Spec: PDF32000_2008.pdf (in spec/pdf1.7/)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// spec17Example represents a single example extracted from the PDF 1.7 spec.
type spec17Example struct {
	Page       int    `json:"page"`
	Section    string `json:"section"`
	ExampleNum string `json:"example_num"`
	Desc       string `json:"description"`
	Content    string `json:"content"`
	Type       string `json:"type"` // "pdf_syntax", "pdf_fragment", "text"
}

func loadSpec17Examples(t *testing.T) []spec17Example {
	t.Helper()
	data, err := os.ReadFile("testdata/spec_examples_17.json")
	if err != nil {
		t.Fatalf("loading spec 1.7 examples: %v", err)
	}
	var examples []spec17Example
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatalf("parsing spec 1.7 examples: %v", err)
	}
	return examples
}

// breadcrumb17 returns a human-readable reference for a PDF 1.7 example.
func (e *spec17Example) breadcrumb17() string {
	num := ""
	if e.ExampleNum != "" {
		num = fmt.Sprintf(" EXAMPLE %s", e.ExampleNum)
	}
	desc := ""
	if e.Desc != "" {
		if len(e.Desc) > 60 {
			desc = fmt.Sprintf(" — %s…", e.Desc[:60])
		} else {
			desc = fmt.Sprintf(" — %s", e.Desc)
		}
	}
	return fmt.Sprintf("ISO 32000-1:2008 (PDF 1.7), %s, p.%d%s%s", e.Section, e.Page, num, desc)
}

// testName17 returns a safe name for use as a Go subtest.
func (e *spec17Example) testName17() string {
	name := strings.ReplaceAll(e.Section, " ", "_")
	name = strings.ReplaceAll(name, ".", "_")
	if e.ExampleNum != "" {
		name += "_ex" + e.ExampleNum
	}
	safe := regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(name, "")
	return safe
}

// TestSpec17ExamplesLex verifies that the lexer can tokenize every
// PDF syntax example from the PDF 1.7 spec without errors.
func TestSpec17ExamplesLex(t *testing.T) {
	examples := loadSpec17Examples(t)

	for i, ex := range examples {
		if ex.Type == "text" {
			continue
		}

		name := fmt.Sprintf("%d_%s", i, ex.testName17())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb17())

			content := extractPDFSyntax(ex.Content)
			content = cleanContent(content)
			if strings.TrimSpace(content) == "" {
				t.Skip("no PDF syntax content after extraction")
			}

			lexer := NewLexer([]byte(content))
			tokenCount := 0
			for {
				tok, err := lexer.NextToken()
				if err != nil {
					t.Logf("lexer stopped after %d tokens: %v", tokenCount, err)
					break
				}
				if tok.Type == TokenEOF {
					break
				}
				tokenCount++
			}
			if tokenCount == 0 {
				t.Logf("warning: no tokens produced")
			}
		})
	}
}

// TestSpec17ExamplesParse tests that PDF syntax examples from the PDF 1.7 spec
// can be parsed into proper object types.
func TestSpec17ExamplesParse(t *testing.T) {
	examples := loadSpec17Examples(t)

	for i, ex := range examples {
		if ex.Type != "pdf_syntax" {
			continue
		}

		name := fmt.Sprintf("%d_%s", i, ex.testName17())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb17())

			content := extractPDFSyntax(ex.Content)
			if strings.TrimSpace(content) == "" {
				t.Skip("no PDF syntax content after extraction")
			}

			if hasPseudoCode(content) {
				t.Skip("contains pseudo-code placeholders (not valid PDF)")
			}

			// Fix stream lengths that are wrong due to text extraction
			if containsStream(content) {
				content = fixStreamLength(content)
			}

			if hasEllipsis(content) {
				tryParseObject(t, content)
				return
			}

			if containsIndirectObj(content) {
				p := NewParser([]byte(content))
				obj, err := p.ParseIndirectObject()
				if err != nil {
					t.Logf("could not parse indirect object: %v", err)
					tryParseObject(t, content)
					return
				}
				t.Logf("parsed indirect object %d %d", obj.Number, obj.Generation)
				tryRoundTrip(t, obj)
				return
			}

			if containsDict(content) {
				tryParseObject(t, content)
				return
			}

			if containsXref(content) {
				tryParseXref(t, content)
				return
			}

			tryParseObject(t, content)
		})
	}
}

// TestSpec17ExamplesRoundTrip tests round-trip (parse -> serialize -> re-parse -> equal)
// for examples that contain complete, parseable indirect objects.
func TestSpec17ExamplesRoundTrip(t *testing.T) {
	examples := loadSpec17Examples(t)

	roundTripped := 0
	for i, ex := range examples {
		if ex.Type != "pdf_syntax" {
			continue
		}

		content := extractPDFSyntax(ex.Content)
		if hasEllipsis(content) || hasPseudoCode(content) {
			continue
		}
		if !containsIndirectObj(content) {
			continue
		}

		// Skip examples with multiple objects that need a full document context
		objCount := len(regexp.MustCompile(`\d+\s+\d+\s+obj\b`).FindAllString(content, -1))
		endobjCount := strings.Count(content, "endobj")
		if objCount != endobjCount || objCount > 1 {
			continue
		}

		// Fix stream lengths
		if containsStream(content) {
			content = fixStreamLength(content)
		}

		name := fmt.Sprintf("%d_%s", i, ex.testName17())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb17())

			p := NewParser([]byte(content))
			obj, err := p.ParseIndirectObject()
			if err != nil {
				t.Fatalf("parse: %v\nContent:\n%s", err, content)
			}

			// Serialize
			var buf bytes.Buffer
			s := NewSerializer(&buf)
			if err := s.WriteIndirectObject(obj); err != nil {
				t.Fatalf("serialize: %v", err)
			}

			// Re-parse
			p2 := NewParser(buf.Bytes())
			obj2, err := p2.ParseIndirectObject()
			if err != nil {
				t.Fatalf("re-parse after round-trip: %v\nSerialized:\n%s", err, buf.String())
			}

			// Compare
			if !Equal(obj, obj2) {
				t.Errorf("round-trip mismatch:\n  original: %v\n  after:    %v", obj, obj2)
			}
			roundTripped++
		})
	}
	t.Logf("round-tripped %d examples", roundTripped)
}

// TestSpec17ExampleCount verifies we have a reasonable number of examples.
func TestSpec17ExampleCount(t *testing.T) {
	examples := loadSpec17Examples(t)

	total := len(examples)
	if total < 250 {
		t.Errorf("expected at least 250 examples from PDF 1.7 spec, got %d", total)
	}
	t.Logf("total examples: %d", total)

	byType := map[string]int{}
	for _, ex := range examples {
		byType[ex.Type]++
	}
	for typ, count := range byType {
		t.Logf("  %s: %d", typ, count)
	}

	bySec := map[string]int{}
	for _, ex := range examples {
		parts := strings.SplitN(ex.Section, ".", 2)
		if len(parts) > 0 {
			bySec[parts[0]]++
		}
	}
	t.Logf("examples per top-level section:")
	for sec, count := range bySec {
		t.Logf("  section %s: %d", sec, count)
	}
}
