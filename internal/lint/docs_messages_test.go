package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestDocQuotedMessagesExist checks what the docs present as the code's own
// words. The troubleshooting guide opens by promising that every message it
// quotes is a real string the library or the CLI emits, and quoted the CLI's
// password errors from before they were rewritten, and a Write refusal in words
// the code had stopped using. Two forms are checked:
//
//   - a fenced block preceded by <!-- messages -->: each line is a message,
//     with N, M, FILE, LEVEL, … and <placeholders> standing for what varies.
//     Every stretch of fixed text between placeholders must occur in a string
//     literal of the module;
//   - a block quote introduced by "From the godoc on `X`:" or "From `X`'s
//     godoc:": its text must occur in the doc comment of a declaration named X.
func TestDocQuotedMessagesExist(t *testing.T) {
	root := testfiles.Root(t)
	lits, docs := sourceTexts(t, root)
	placeholder := regexp.MustCompile(`\b(N|M|FILE|LEVEL|CLAUSE)\b|…|<[^>]*>`)
	checkedMsgs, checkedQuotes := 0, 0
	for _, rel := range markdownFiles(t) {
		if isHistorical(rel) {
			continue
		}
		md := readMarkdown(t, rel)
		for _, f := range md.fences {
			if !f.messages {
				continue
			}
			for i, line := range strings.Split(f.body, "\n") {
				if k := strings.Index(line, "  #"); k >= 0 {
					line = line[:k]
				}
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				checkedMsgs++
				for _, frag := range placeholder.Split(line, -1) {
					frag = strings.TrimSpace(frag)
					if len(frag) < 6 {
						continue
					}
					if !containsIn(lits, frag) {
						t.Errorf("%s:%d: the quoted message %q contains %q, which no string literal in the module does", rel, f.line+i, line, frag)
					}
				}
			}
		}
		for _, q := range godocQuotes(t, rel) {
			checkedQuotes++
			doc, ok := docs[q.name]
			if !ok {
				t.Errorf("%s:%d: quotes the godoc of %s, which nothing declares", rel, q.line, q.name)
				continue
			}
			if !strings.Contains(doc, q.text) {
				t.Errorf("%s:%d: the quotation of %s's godoc is not in it any more:\n%s", rel, q.line, q.name, q.text)
			}
		}
	}
	if checkedMsgs < 10 || checkedQuotes < 2 {
		t.Fatalf("checked %d messages and %d godoc quotes; the scan is looking in the wrong place", checkedMsgs, checkedQuotes)
	}
}

func containsIn(set []string, frag string) bool {
	for _, s := range set {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

type godocQuote struct {
	name, text string
	line       int
}

var godocIntro = regexp.MustCompile("From (?:the godoc on `([A-Za-z_.]+)`|`([A-Za-z_.]+)`'s godoc):\\s*$")

// godocQuotes finds the block quotes a document attributes to a godoc.
func godocQuotes(t *testing.T, rel string) []godocQuote {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testfiles.Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var out []godocQuote
	for i, l := range lines {
		m := godocIntro.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		name := m[1] + m[2]
		if k := strings.LastIndex(name, "."); k >= 0 {
			name = name[k+1:]
		}
		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
			j++
		}
		var parts []string
		for ; j < len(lines) && strings.HasPrefix(lines[j], ">"); j++ {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(lines[j], ">")))
		}
		if len(parts) == 0 {
			t.Errorf("%s:%d: announces a godoc quotation and has no block quote after it", rel, i+1)
			continue
		}
		// A paragraph break inside the quote ("> " alone) is a gap in the
		// quotation; each paragraph is checked on its own.
		for _, para := range strings.Split(strings.Join(parts, "\n"), "\n\n") {
			if text := strings.Join(strings.Fields(para), " "); text != "" {
				out = append(out, godocQuote{name: name, text: text, line: i + 1})
			}
		}
	}
	return out
}

// sourceTexts returns every string literal of the module's non-test source,
// unquoted, and every declaration's doc comment by name, with white space
// collapsed.
func sourceTexts(t *testing.T, root string) (lits []string, docs map[string]string) {
	t.Helper()
	docs = map[string]string{}
	for _, rel := range goSourceFiles(t, false) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					if v, err := strconv.Unquote(n.Value); err == nil {
						lits = append(lits, v)
					}
				}
			case *ast.FuncDecl:
				if n.Doc != nil {
					docs[n.Name.Name] += " " + strings.Join(strings.Fields(n.Doc.Text()), " ")
				}
			case *ast.TypeSpec:
				if n.Doc != nil {
					docs[n.Name.Name] += " " + strings.Join(strings.Fields(n.Doc.Text()), " ")
				}
			case *ast.Field:
				if n.Doc != nil {
					for _, id := range n.Names {
						docs[id.Name] += " " + strings.Join(strings.Fields(n.Doc.Text()), " ")
					}
				}
			}
			return true
		})
	}
	return lits, docs
}
