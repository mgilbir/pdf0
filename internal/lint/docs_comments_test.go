package lint

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestCommentsNameWhatExists is TestDocCodeSpansResolve for the comments in
// the Go source. A camelCase word in a comment is an identifier — English has
// none — and when the identifier is renamed or removed the comment goes on
// naming it: the core's comments named the scanners the content lexer had
// replaced, facturx documented a helper nobody declared, and a doc comment sat
// on the wrong function after the one it described was deleted (audit
// 2026-09-22 C167). Every such word must be declared somewhere in the
// module or the modules it points into, or be a string the code uses.
func TestCommentsNameWhatExists(t *testing.T) {
	idx := loadDeclIndex(t)
	root := testfiles.Root(t)
	camel := regexp.MustCompile(`\b[a-z][a-z0-9]*[A-Z][A-Za-z0-9]*\b`)
	checked := 0
	for _, rel := range goSourceFiles(t, true) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				text := c.Text
				if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "//line ") {
					continue
				}
				// An indented line is a code block, an example: its names
				// are the example's own, and TestDocSnippetsCompile checks
				// it against the module.
				if strings.HasPrefix(text, "//\t") {
					continue
				}
				for _, loc := range camel.FindAllStringIndex(text, -1) {
					w := text[loc[0]:loc[1]]
					// A word glued to a path or a URL is part of it.
					if loc[0] > 0 && strings.ContainsRune("/.-_", rune(text[loc[0]-1])) {
						continue
					}
					if loc[1] < len(text) && strings.ContainsRune("_", rune(text[loc[1]])) {
						continue
					}
					checked++
					if idx.all[w] || idx.literal[w] {
						continue
					}
					if _, ok := notGoNames[w]; ok {
						continue
					}
					t.Errorf("%s:%d: the comment names %s, which nothing declares", rel, fset.Position(c.Pos()).Line, w)
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("checked %d names in comments; the scan is looking in the wrong place", checked)
	}
}
