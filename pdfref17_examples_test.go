package pdf0

// Tests over the example code of Adobe's PDF Reference, sixth edition,
// version 1.7: every indirect object and bare object in it that is complete
// and written out in PDF is lexed, parsed, written and parsed again, and every
// cross-reference section is parsed.
//
// The code is committed, in testdata/pdfref17_examples.json, under the
// permission the Reference gives in its section 1.4 (THIRD_PARTY_NOTICES
// quotes it), so unlike the ISO 32000 examples, which are extracted from
// hand-placed PDFs and skip without them, these run everywhere, CI included.
// cmd/extract_spec_examples/pdfref17.py made the file; docs/testing.md says
// how to make it again.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// pdfRef17Example is one example of the Reference: its code and where it is.
type pdfRef17Example struct {
	Example string `json:"example"` // its number, "3.1" or "G.4"
	Page    int    `json:"page"`    // the page printed on it
	Source  string `json:"source"`  // the two, as a reader looks them up
	Content string `json:"content"`
	Type    string `json:"type"` // "pdf_syntax" or "pdf_fragment", as main17.py classifies
}

const pdfRef17Title = "PDF Reference, sixth edition, version 1.7 (Adobe, November 2006)"

// What the fixture holds, and what of it the tests below get to check. The
// fixture is committed and changes only when it is extracted again, so these
// are exact: a count that moves means the file or the selection below did.
const (
	pdfRef17ExampleCount = 146
	// Indirect objects that are complete (they reach their endobj), written
	// out in full (no "…" for what the Reference leaves out) and in PDF (no
	// placeholder such as tintTransform1).
	pdfRef17CheckedObjects = 250
	// Bare objects: examples that are one dictionary or array.
	pdfRef17CheckedBare = 11
	// Cross-reference sections.
	pdfRef17CheckedXRef = 2
)

// pdfRef17Misprints are objects the Reference prints wrong, by example and
// object number ("bare" for an example that is one bare object). Each was
// checked against the page, so it is not the extraction's doing. They must
// fail to parse; one that parses has been fixed, in the fixture or in the
// parser, and comes off the list.
var pdfRef17Misprints = map[string]string{
	"8.16 1 0":   "the /AP dictionary is never closed: three << and two >>",
	"8.22 bare":  "the /X array's dictionary is never closed: << and then ]",
	"G.1 2 0":    "/Type Outlines: Outlines is missing its slash",
	"G.11 400 0": "/S TOC: TOC is missing its slash",
}

var (
	pdfRef17Caption   = regexp.MustCompile(`(?m)^\s*Example [0-9A-Z]+\.[0-9]+\b`)
	pdfRef17Number    = regexp.MustCompile(`^(?:[0-9]+|[A-Z])\.[0-9]+$`)
	pdfRef17ObjStart  = regexp.MustCompile(`(?m)^[ \t]*(\d+)[ \t]+(\d+)[ \t]+obj\b`)
	pdfRef17StreamKw  = regexp.MustCompile(`(?m)^[ \t]*stream[ \t]*\r?\n`)
	pdfRef17EndStream = regexp.MustCompile(`\r?\n[ \t]*endstream\b`)
	pdfRef17Length    = regexp.MustCompile(`/Length[ \t\r\n]+\d+\b(?:[ \t\r\n]+\d+[ \t\r\n]+R\b)?`)
)

func loadPDFRef17Examples(t *testing.T) []pdfRef17Example {
	t.Helper()
	data, err := os.ReadFile(testfiles.Committed(t, "testdata/pdfref17_examples.json"))
	if err != nil {
		t.Fatal(err)
	}
	var examples []pdfRef17Example
	if err := json.Unmarshal(data, &examples); err != nil {
		t.Fatalf("parsing testdata/pdfref17_examples.json: %v", err)
	}
	return examples
}

// TestPDFRef17ExamplesFixture: the fixture is what its notice says it is —
// the Reference's example code, each example named by its number and page,
// and nothing else: no caption, and no example without code.
func TestPDFRef17ExamplesFixture(t *testing.T) {
	examples := loadPDFRef17Examples(t)
	if len(examples) != pdfRef17ExampleCount {
		t.Errorf("the fixture holds %d examples, want %d", len(examples), pdfRef17ExampleCount)
	}
	seen := map[string]bool{}
	lastPage := 0
	for _, ex := range examples {
		if !pdfRef17Number.MatchString(ex.Example) || seen[ex.Example] {
			t.Errorf("example number %q is malformed or repeated", ex.Example)
		}
		seen[ex.Example] = true
		if ex.Page < lastPage {
			t.Errorf("Example %s is on page %d, before the previous example's page %d", ex.Example, ex.Page, lastPage)
		}
		lastPage = ex.Page
		if want := fmt.Sprintf("%s, Example %s, page %d", pdfRef17Title, ex.Example, ex.Page); ex.Source != want {
			t.Errorf("Example %s: source %q, want %q", ex.Example, ex.Source, want)
		}
		if ex.Type != "pdf_syntax" && ex.Type != "pdf_fragment" {
			t.Errorf("Example %s is of type %q: the fixture holds code only", ex.Example, ex.Type)
		}
		if strings.TrimSpace(ex.Content) == "" {
			t.Errorf("Example %s has no code", ex.Example)
		}
		if m := pdfRef17Caption.FindString(ex.Content); m != "" {
			t.Errorf("Example %s carries a caption, %q", ex.Example, strings.TrimSpace(m))
		}
	}
}

// TestPDFRef17Examples lexes, parses, writes and parses again every object
// of the Reference's examples that is complete and written out in PDF, and
// every cross-reference section, and fails on any that does not come back as
// it went in.
func TestPDFRef17Examples(t *testing.T) {
	examples := loadPDFRef17Examples(t)
	var objects, bare, xrefs, misprints int
	for _, ex := range examples {
		t.Run("Example_"+strings.ReplaceAll(ex.Example, ".", "_"), func(t *testing.T) {
			t.Logf("%s", ex.Source)
			// misprinted reports whether the Reference prints this object
			// wrong, and fails if it is listed so and parses all the same.
			misprinted := func(what, text string, indirect bool) bool {
				why, ok := pdfRef17Misprints[ex.Example+" "+what]
				if !ok {
					return false
				}
				misprints++
				p := syntax.NewParser([]byte(text))
				var err error
				if indirect {
					_, err = p.ParseIndirectObject()
				} else {
					_, err = p.ParseObject()
				}
				if err == nil {
					t.Errorf("%s parses, and is listed as misprinted (%s): take it off pdfRef17Misprints", what, why)
				}
				return true
			}
			content := cleanContent(ex.Content)
			objs := pdfRef17Objects(content)
			for _, obj := range objs {
				if !obj.complete || pdfRef17Elided(obj.text) || hasPseudoCode(obj.text) || misprinted(obj.id, obj.text, true) {
					continue
				}
				objects++
				pdfRef17CheckObject(t, obj.id, setStreamLength(obj.text), true)
			}
			if len(objs) > 0 {
				return
			}
			text := strings.TrimSpace(content)
			switch {
			case strings.HasPrefix(text, "<<") || strings.HasPrefix(text, "["):
				if pdfRef17Elided(text) || hasPseudoCode(text) || misprinted("bare", text, false) {
					return
				}
				bare++
				pdfRef17CheckObject(t, "the example", text, false)
			case strings.HasPrefix(text, "xref"):
				xrefs++
				pdfRef17CheckXRef(t, text)
			}
		})
	}
	t.Logf("checked %d indirect objects, %d bare objects and %d cross-reference sections; %d misprinted objects fail as they should",
		objects, bare, xrefs, misprints)
	if objects != pdfRef17CheckedObjects || bare != pdfRef17CheckedBare || xrefs != pdfRef17CheckedXRef {
		t.Errorf("checked %d indirect objects, %d bare objects and %d cross-reference sections; want %d, %d and %d.\n"+
			"The fixture changed, or what decides which objects are checked did",
			objects, bare, xrefs, pdfRef17CheckedObjects, pdfRef17CheckedBare, pdfRef17CheckedXRef)
	}
	if misprints != len(pdfRef17Misprints) {
		t.Errorf("met %d of the %d listed misprints: a listed one is no longer in the fixture, or no longer checked", misprints, len(pdfRef17Misprints))
	}
}

// pdfRef17Object is one "N G obj" of an example, up to its endobj.
type pdfRef17Object struct {
	id       string // "N G"
	text     string
	complete bool // it reaches its endobj before the next object starts
}

// pdfRef17Objects splits an example into its indirect objects.
func pdfRef17Objects(content string) []pdfRef17Object {
	locs := pdfRef17ObjStart.FindAllStringSubmatchIndex(content, -1)
	var out []pdfRef17Object
	for i, loc := range locs {
		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		seg := content[loc[0]:end]
		o := pdfRef17Object{id: content[loc[2]:loc[3]] + " " + content[loc[4]:loc[5]], text: seg}
		if k := strings.Index(seg, "endobj"); k >= 0 {
			o.text, o.complete = seg[:k+len("endobj")], true
		}
		out = append(out, o)
	}
	return out
}

// pdfRef17Elided reports whether the Reference left something out of text:
// "…", or the same written as three dots, spaced or not.
func pdfRef17Elided(text string) bool {
	return hasEllipsis(text) || strings.Contains(text, ". . .")
}

// setStreamLength makes a stream's /Length the length of its data as the
// fixture has it. The Reference's lengths count the bytes of a file the
// example is cut from, which its indentation here does not reproduce. The
// stream keyword is the one alone on its line, not the word in a comment
// ("% ICC profile stream"); a /Length that is a reference is replaced too.
func setStreamLength(obj string) string {
	kw := pdfRef17StreamKw.FindStringIndex(obj)
	if kw == nil {
		return obj
	}
	end := pdfRef17EndStream.FindStringIndex(obj[kw[1]:])
	if end == nil {
		return obj
	}
	dict := pdfRef17Length.ReplaceAllString(obj[:kw[0]], fmt.Sprintf("/Length %d", end[0]))
	return dict + obj[kw[0]:]
}

// pdfRef17CheckObject lexes text to its end (to its stream keyword, if it
// has one: stream data is not tokens), parses it — as an indirect object or a
// bare one — writes what it parsed and parses that again, and fails unless the
// two parses are equal and the parse took all of text.
func pdfRef17CheckObject(t *testing.T, what, text string, indirect bool) {
	t.Helper()
	tokens := text
	if kw := pdfRef17StreamKw.FindStringIndex(text); kw != nil {
		tokens = text[:kw[0]]
	}
	lexer := syntax.NewLexer([]byte(tokens))
	for {
		tok, err := lexer.NextToken()
		if err != nil {
			t.Errorf("%s: lexing: %v\n%s", what, err, text)
			return
		}
		if tok.Type == syntax.TokenEOF {
			break
		}
	}

	parse := func(data []byte) (object.Object, *syntax.Parser, error) {
		p := syntax.NewParser(data)
		if indirect {
			o, err := p.ParseIndirectObject()
			if err != nil {
				return nil, p, err
			}
			return o, p, nil
		}
		o, err := p.ParseObject()
		return o, p, err
	}
	obj, p, err := parse([]byte(text))
	if err != nil {
		t.Errorf("%s: parsing: %v\n%s", what, err, text)
		return
	}
	if rest := strings.TrimSpace(stripPDFComments(text[p.Offset():])); rest != "" {
		t.Errorf("%s: parsing stopped before the end, at %q\n%s", what, rest, text)
		return
	}

	var buf bytes.Buffer
	s := syntax.NewSerializer(&buf)
	if indirect {
		err = s.WriteIndirectObject(obj.(*object.IndirectObject))
	} else {
		err = s.WriteObject(obj)
	}
	if err != nil {
		t.Errorf("%s: writing: %v", what, err)
		return
	}
	again, _, err := parse(buf.Bytes())
	if err != nil {
		t.Errorf("%s: parsing what was written: %v\n%s", what, err, buf.String())
		return
	}
	if !object.Equal(obj, again) {
		t.Errorf("%s does not survive the round trip:\n  read:    %v\n  re-read: %v\nwritten as:\n%s", what, obj, again, buf.String())
	}
}

// stripPDFComments drops each % comment to the end of its line; it is for the
// tail after an object, which holds no strings.
func stripPDFComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.IndexByte(line, '%'); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// pdfRef17CheckXRef parses a cross-reference section and fails unless it
// reads at least one entry.
func pdfRef17CheckXRef(t *testing.T, text string) {
	t.Helper()
	data := []byte(text)
	pos := int64(len("xref"))
	for pos < int64(len(data)) && syntax.IsWhitespace(data[pos]) {
		pos++
	}
	table, err := ParseXRefTable(data, pos)
	if err != nil {
		t.Errorf("parsing the cross-reference section: %v\n%s", err, text)
		return
	}
	if len(table.Entries)+len(table.Free) == 0 {
		t.Errorf("the cross-reference section read as empty:\n%s", text)
	}
}

// TestPDFRef17ExamplesRegenerate holds the committed fixture to its
// extractor: run over the Reference, cmd/extract_spec_examples/pdfref17.py
// must write the fixture byte for byte, so that an edit to the extractor
// cannot leave a fixture behind that it would no longer make. It needs the
// Reference (placed by hand; PDF_REFERENCE_17 may name its directory),
// pdftohtml, pdftotext and python3, and skips without them.
func TestPDFRef17ExamplesRegenerate(t *testing.T) {
	pdf := testfiles.PDFReference17.File(t, "pdfreference1.7old.pdf")
	if _, err := exec.LookPath("pdftohtml"); err != nil {
		t.Skip("pdftohtml not installed")
	}
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	if testing.Short() {
		t.Skip("extracting the Reference takes about twenty seconds")
	}
	root := testfiles.Root(t)
	script := filepath.Join(root, "cmd", "extract_spec_examples", "pdfref17.py")
	got, err := exec.Command("python3", script, "--pdf", pdf).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("pdfref17.py: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("pdfref17.py: %v", err)
	}
	want, err := os.ReadFile(testfiles.Committed(t, "testdata/pdfref17_examples.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("testdata/pdfref17_examples.json is not what the extractor makes of %s: run it again\n"+
			"  python3 cmd/extract_spec_examples/pdfref17.py --pdf %q > testdata/pdfref17_examples.json\n"+
			"and look at the difference before committing it", pdf, pdf)
	}
}
