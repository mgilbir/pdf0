package lint

// Shared scanning for the documentation checks (docs_*_test.go): finding the
// Markdown files, splitting them into fenced blocks and inline code spans, and
// finding the code blocks in Go doc comments.

import (
	"go/ast"
	"go/doc/comment"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// historicalDocs are documents that describe the tree as it was when they were
// written. An audit report that names a file which has since moved is correct:
// that is where the file was. The checks that hold every other document to the
// tree as it is skip these.
var historicalDocs = []string{
	"docs/audits/",
	// Design records: each opens by saying it records what was intended and
	// has not been rewritten to match the outcome.
	"docs/proposals/",
}

// isHistorical reports whether a document describes the past: one under
// historicalDocs, or an architecture decision record that has been
// superseded (docs/adr/README.md: "A record is superseded, never edited to
// say something different").
func isHistorical(rel string) bool {
	for _, p := range historicalDocs {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return supersededADR[rel]
}

var supersededADR = map[string]bool{}

// loadSupersededADRs marks each ADR whose status line says it is superseded.
func loadSupersededADRs(t *testing.T) {
	t.Helper()
	root := testfiles.Root(t)
	m, err := filepath.Glob(filepath.Join(root, "docs", "adr", "[0-9]*.md"))
	if err != nil || len(m) == 0 {
		t.Fatalf("no ADRs found under docs/adr (%v)", err)
	}
	for _, p := range m {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range strings.SplitN(string(b), "\n", 8) {
			if strings.HasPrefix(l, "**Status:** superseded") {
				rel, _ := filepath.Rel(root, p)
				supersededADR[filepath.ToSlash(rel)] = true
			}
		}
	}
}

// skippedDirs are never scanned for documents or Go source: fetched or
// hand-placed data, tool state, and other checkouts.
var skippedDirs = map[string]bool{
	".git": true, "testdata": true, "spec": true, ".claude": true, "node_modules": true,
}

// markdownFiles returns every Markdown file in the repository as slash paths
// relative to the root, sorted.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	root := testfiles.Root(t)
	loadSupersededADRs(t)
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("found no Markdown files: the walk is looking in the wrong place")
	}
	slices.Sort(out)
	return out
}

// fence is one fenced code block.
type fence struct {
	file string // slash path relative to the root
	line int    // 1-based line of the first line of content
	lang string
	body string
	// directive is the body of a "<!-- snippet ... -->" comment on the
	// line(s) directly above the fence, without the comment markers and the
	// word "snippet", or "".
	directive string
	hasDir    bool
	// messages marks a fence preceded by "<!-- messages -->": every line is
	// a message the code emits (TestDocQuotedMessagesExist).
	messages bool
}

// span is one inline code span outside any fence.
type span struct {
	file string
	line int
	text string
}

type markdown struct {
	rel    string
	fences []fence
	spans  []span
}

var (
	fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([A-Za-z0-9_+-]*)")
	codeSpan  = regexp.MustCompile("`([^`\n]+)`")
)

func readMarkdown(t *testing.T, rel string) markdown {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testfiles.Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return parseMarkdown(rel, string(b))
}

func parseMarkdown(rel, src string) markdown {
	md := markdown{rel: rel}
	lines := strings.Split(src, "\n")
	inComment := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := fenceOpen.FindStringSubmatch(line); m != nil && !inComment {
			marker := m[1]
			f := fence{file: rel, line: i + 2, lang: strings.ToLower(m[2])}
			if d, ok := commentAbove(lines, i); ok {
				if body, isSnippet := strings.CutPrefix(d, "snippet"); isSnippet {
					f.directive, f.hasDir = strings.TrimSpace(body), true
				} else if d == "messages" {
					f.messages = true
				}
			}
			var body []string
			for i++; i < len(lines); i++ {
				tl := strings.TrimSpace(lines[i])
				if strings.HasPrefix(tl, marker) && strings.Trim(tl, marker[:1]) == "" {
					break
				}
				body = append(body, lines[i])
			}
			f.body = strings.Join(body, "\n")
			md.fences = append(md.fences, f)
			continue
		}
		// HTML comments hold directives and notes, not prose. A "<!--"
		// inside a code span is prose about comments, not one.
		var text strings.Builder
		inSpan := false
		for j := 0; j < len(line); j++ {
			switch {
			case inComment:
				if strings.HasPrefix(line[j:], "-->") {
					inComment = false
					j += 2
				}
			case line[j] == '`':
				inSpan = !inSpan
				text.WriteByte('`')
			case !inSpan && strings.HasPrefix(line[j:], "<!--"):
				inComment = true
				j += 3
			default:
				text.WriteByte(line[j])
			}
		}
		for _, m := range codeSpan.FindAllStringSubmatch(text.String(), -1) {
			md.spans = append(md.spans, span{file: rel, line: i + 1, text: m[1]})
		}
	}
	return md
}

// commentAbove returns the body of an HTML comment that ends on the nearest
// non-blank line above line i, without its markers.
func commentAbove(lines []string, i int) (string, bool) {
	j := i - 1
	for j >= 0 && strings.TrimSpace(lines[j]) == "" {
		j--
	}
	if j < 0 || !strings.HasSuffix(strings.TrimSpace(lines[j]), "-->") {
		return "", false
	}
	end := j
	for j >= 0 && !strings.Contains(lines[j], "<!--") {
		j--
	}
	if j < 0 {
		return "", false
	}
	text := strings.Join(lines[j:end+1], "\n")
	text = text[strings.Index(text, "<!--")+4:]
	text = strings.TrimSuffix(strings.TrimSpace(text), "-->")
	return strings.TrimSpace(text), true
}

// commentBlock is one code block in a Go doc comment.
type commentBlock struct {
	file string // slash path relative to the root
	line int    // the line of the comment the block sits in
	text string
}

// goSourceFiles returns the module's Go files (test files included when
// tests is set), as slash paths relative to the root.
func goSourceFiles(t *testing.T, tests bool) []string {
	t.Helper()
	root := testfiles.Root(t)
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || (!tests && strings.HasSuffix(p, "_test.go")) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// docCommentBlocks returns the code blocks in the doc comments a reader of
// the API sees: package comments, exported functions and methods, and every
// declaration group.
func docCommentBlocks(t *testing.T) []commentBlock {
	t.Helper()
	root := testfiles.Root(t)
	var out []commentBlock
	var p comment.Parser
	for _, rel := range goSourceFiles(t, false) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		groups := []*ast.CommentGroup{f.Doc}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					groups = append(groups, d.Doc)
				}
			case *ast.GenDecl:
				groups = append(groups, d.Doc)
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						groups = append(groups, s.Doc)
					case *ast.ValueSpec:
						groups = append(groups, s.Doc)
					}
				}
			}
		}
		for _, g := range groups {
			if g == nil {
				continue
			}
			for _, b := range p.Parse(g.Text()).Content {
				if c, ok := b.(*comment.Code); ok {
					out = append(out, commentBlock{file: rel, line: fset.Position(g.Pos()).Line, text: c.Text})
				}
			}
		}
	}
	return out
}

// isCommandBlock reports whether a comment code block is shell commands
// rather than Go.
func isCommandBlock(text string) bool {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	first = strings.TrimPrefix(first, "$ ")
	return strings.HasPrefix(first, "go ") || strings.HasPrefix(first, "make ") || strings.HasPrefix(first, "./")
}
