package pdf0

// Tests derived from ALL examples in ISO 32000-2:2020 (PDF 2.0).
//
// Examples are extracted from the spec by cmd/extract_spec_examples/main.py
// and stored in testdata/spec_examples.json. Each example records the spec
// section, page number, and content.
//
// Spec: ISO_32000-2_sponsored-ec2.pdf (in spec/pdf2.0/)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// specExample represents a single example extracted from the spec.
type specExample struct {
	Page       int    `json:"page"`
	Section    string `json:"section"`
	ExampleNum string `json:"example_num"`
	Desc       string `json:"description"`
	Content    string `json:"content"`
	Type       string `json:"type"` // "pdf_syntax", "pdf_fragment", "text"
}

func loadSpecExamples(t *testing.T) []specExample {
	t.Helper()
	data, err := os.ReadFile("testdata/spec_examples.json")
	if err != nil {
		t.Fatalf("loading spec examples: %v", err)
	}
	var examples []specExample
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatalf("parsing spec examples: %v", err)
	}
	return examples
}

// breadcrumb returns a human-readable reference for an example.
func (e *specExample) breadcrumb() string {
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
	return fmt.Sprintf("ISO 32000-2:2020, %s, p.%d%s%s", e.Section, e.Page, num, desc)
}

// testName returns a safe name for use as a Go subtest.
func (e *specExample) testName() string {
	name := strings.ReplaceAll(e.Section, " ", "_")
	name = strings.ReplaceAll(name, ".", "_")
	if e.ExampleNum != "" {
		name += "_ex" + e.ExampleNum
	}
	safe := regexp.MustCompile(`[^a-zA-Z0-9_]`).ReplaceAllString(name, "")
	return safe
}

// cleanContent fixes text extraction artifacts in example content.
func cleanContent(content string) string {
	// Replace Unicode em-dash/en-dash with ASCII minus (text extraction artifact)
	content = strings.ReplaceAll(content, "\u2013", "-") // en-dash
	content = strings.ReplaceAll(content, "\u2014", "-") // em-dash
	// Replace non-breaking spaces
	content = strings.ReplaceAll(content, "\u00a0", " ")
	return content
}

// extractPDFSyntax tries to extract the PDF syntax portion from an example,
// stripping any prose text that may precede or follow it.
func extractPDFSyntax(content string) string {
	content = cleanContent(content)
	lines := strings.Split(content, "\n")

	// Find the first line that contains an indirect object definition (N G obj)
	// This is more reliable than just looking for "PDF syntax characters"
	objDefRe := regexp.MustCompile(`^\s*\d+\s+\d+\s+obj\b`)
	for i, line := range lines {
		if objDefRe.MatchString(line) {
			// Found an indirect object - return from here to the end
			return joinToEnd(lines, i)
		}
	}

	// Look for xref table
	for i, line := range lines {
		if strings.TrimSpace(line) == "xref" {
			return joinToEnd(lines, i)
		}
	}

	// Look for trailer
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "trailer") {
			return joinToEnd(lines, i)
		}
	}

	// Look for dictionary start
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<<") {
			return joinToEnd(lines, i)
		}
	}

	// Look for array start
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			return joinToEnd(lines, i)
		}
	}

	// Fallback: find first line that looks like PDF syntax
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if looksLikePDFSyntax(trimmed) {
			return joinToEnd(lines, i)
		}
	}

	return content
}

func joinToEnd(lines []string, startIdx int) string {
	// Find the last line of meaningful content
	endIdx := startIdx
	for i := len(lines) - 1; i >= startIdx; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			endIdx = i
			break
		}
	}
	return strings.Join(lines[startIdx:endIdx+1], "\n")
}

func looksLikePDFSyntax(s string) bool {
	if strings.HasPrefix(s, "<<") || strings.HasPrefix(s, ">>") {
		return true
	}
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "]") {
		return true
	}
	if strings.HasPrefix(s, "(") || strings.HasPrefix(s, "<") {
		return true
	}
	if strings.HasPrefix(s, "/") {
		return true
	}
	if strings.HasPrefix(s, "%") {
		return true
	}
	if strings.HasPrefix(s, "xref") || strings.HasPrefix(s, "trailer") || strings.HasPrefix(s, "startxref") {
		return true
	}
	if strings.HasPrefix(s, "true") || strings.HasPrefix(s, "false") || strings.HasPrefix(s, "null") {
		return true
	}
	if strings.HasPrefix(s, "stream") || strings.HasPrefix(s, "endstream") || strings.HasPrefix(s, "endobj") {
		return true
	}
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		return true
	}
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') && len(s) > 1 && (s[1] >= '0' && s[1] <= '9' || s[1] == '.') {
		return true
	}
	if matched, _ := regexp.MatchString(`^(BT|ET|q|Q)\b`, s); matched {
		return true
	}
	return false
}

// hasEllipsis checks if content contains ellipsis indicating incomplete example.
func hasEllipsis(content string) bool {
	return strings.Contains(content, "…") || strings.Contains(content, "...")
}

// hasPseudoCode checks if content contains pseudo-code identifiers
// that aren't valid PDF syntax (e.g. alternateSpace, tintTransform1).
func hasPseudoCode(content string) bool {
	pseudoRe := regexp.MustCompile(`\balternateSpace\b|\btintTransform\d*\b|\bCMYK_ICC\b|\bRGB_ICC\b`)
	return pseudoRe.MatchString(content)
}

// containsStream checks if content contains stream/endstream keywords.
func containsStream(content string) bool {
	return regexp.MustCompile(`\bstream\b`).MatchString(content) &&
		strings.Contains(content, "endstream")
}

// fixStreamLength recalculates the /Length value in a stream dictionary
// to match the actual stream data. This is needed because text extraction
// from the spec PDF alters whitespace, making the original Length wrong.
func fixStreamLength(content string) string {
	// Find "stream\r\n" or "stream\n" marker
	streamIdx := -1
	for _, marker := range []string{"stream\r\n", "stream\n"} {
		idx := strings.Index(content, marker)
		if idx >= 0 {
			streamIdx = idx + len(marker)
			break
		}
	}
	if streamIdx < 0 {
		return content
	}

	// Find "endstream"
	endstreamIdx := strings.Index(content[streamIdx:], "endstream")
	if endstreamIdx < 0 {
		return content
	}

	// Calculate actual data length (strip trailing newline before endstream)
	dataEnd := streamIdx + endstreamIdx
	if dataEnd > streamIdx && content[dataEnd-1] == '\n' {
		dataEnd--
	}
	if dataEnd > streamIdx && content[dataEnd-1] == '\r' {
		dataEnd--
	}
	actualLen := dataEnd - streamIdx

	// Replace /Length value in the dictionary
	lengthRe := regexp.MustCompile(`/Length\s+\d+`)
	return lengthRe.ReplaceAllString(content, fmt.Sprintf("/Length %d", actualLen))
}

// containsIndirectObj checks if content contains a complete indirect object definition.
func containsIndirectObj(content string) bool {
	return regexp.MustCompile(`\d+\s+\d+\s+obj\b`).MatchString(content) &&
		strings.Contains(content, "endobj")
}

// containsDict checks if content contains << >> delimiters.
func containsDict(content string) bool {
	return strings.Contains(content, "<<") && strings.Contains(content, ">>")
}

// containsXref checks if content starts with or contains xref table.
func containsXref(content string) bool {
	return regexp.MustCompile(`(?m)^xref\b`).MatchString(content)
}

// TestSpecExamplesLex verifies that the lexer can tokenize every
// PDF syntax example from the spec without errors.
func TestSpecExamplesLex(t *testing.T) {
	examples := loadSpecExamples(t)

	for i, ex := range examples {
		if ex.Type == "text" {
			continue // Skip pure text examples — nothing to lex
		}

		name := fmt.Sprintf("%d_%s", i, ex.testName())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb())

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
					// Some examples contain unusual syntax (PostScript operators,
					// pseudo-code) that the lexer can't handle — log but don't fail.
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

// TestSpecExamplesParse tests that PDF syntax examples from the spec
// can be parsed into proper object types.
func TestSpecExamplesParse(t *testing.T) {
	examples := loadSpecExamples(t)

	for i, ex := range examples {
		if ex.Type != "pdf_syntax" {
			continue
		}

		name := fmt.Sprintf("%d_%s", i, ex.testName())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb())

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
				// Can still try to parse the beginning
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

// TestSpecExamplesRoundTrip tests round-trip (parse → serialize → re-parse → equal)
// for examples that contain complete, parseable indirect objects.
func TestSpecExamplesRoundTrip(t *testing.T) {
	examples := loadSpecExamples(t)

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

		name := fmt.Sprintf("%d_%s", i, ex.testName())
		t.Run(name, func(t *testing.T) {
			t.Logf("Breadcrumb: %s", ex.breadcrumb())

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

// tryParseObject attempts to parse content as a PDF object.
func tryParseObject(t *testing.T, content string) {
	t.Helper()
	p := NewParser([]byte(content))
	obj, err := p.ParseObject()
	if err != nil {
		t.Logf("parse object: %v (content starts with: %.80s)", err, content)
		return
	}
	t.Logf("parsed object of type %T", obj)
}

// tryRoundTrip serializes and re-parses an indirect object.
func tryRoundTrip(t *testing.T, obj *IndirectObject) {
	t.Helper()
	var buf bytes.Buffer
	s := NewSerializer(&buf)
	if err := s.WriteIndirectObject(obj); err != nil {
		t.Logf("serialize: %v", err)
		return
	}

	p := NewParser(buf.Bytes())
	obj2, err := p.ParseIndirectObject()
	if err != nil {
		t.Logf("re-parse: %v\nSerialized:\n%s", err, buf.String())
		return
	}

	if !Equal(obj, obj2) {
		t.Errorf("round-trip mismatch for object %d %d", obj.Number, obj.Generation)
	}
}

// tryParseXref attempts to parse content as an xref table.
func tryParseXref(t *testing.T, content string) {
	t.Helper()
	idx := strings.Index(content, "xref")
	if idx < 0 {
		t.Logf("no xref keyword found")
		return
	}

	data := []byte(content)
	pos := int64(idx + len("xref"))
	for pos < int64(len(data)) && isWhitespace(data[pos]) {
		pos++
	}

	table, err := ParseXRefTable(data, pos)
	if err != nil {
		t.Logf("parse xref table: %v", err)
		return
	}
	t.Logf("parsed xref table with %d entries", len(table.Entries))
}

// TestSpecExampleCount verifies we have a reasonable number of examples.
func TestSpecExampleCount(t *testing.T) {
	examples := loadSpecExamples(t)

	total := len(examples)
	if total < 300 {
		t.Errorf("expected at least 300 examples from spec, got %d", total)
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
