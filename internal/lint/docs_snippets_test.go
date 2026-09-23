package lint

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestDocSnippetsCompile type-checks every Go snippet a reader is shown: each
// ```go block in the Markdown documentation, and each Go code block in a doc
// comment, against the module as it is. The first example in the package
// documentation named a constant that had moved to another package, and
// nothing noticed, because nothing compiled it (audit 2026-09-22 C106; the
// signing guide's C158 was the same mistake).
//
// A snippet is usually a fragment, so it is completed before checking:
//
//   - A snippet with a package clause is checked as the file it is.
//   - A snippet of declarations is checked as a file of them.
//   - Anything else is a function body, of a function returning error.
//
// Imports are added for each package the snippet names (the module's public
// packages and the standard library ones in stdImports). The variables the
// surrounding prose establishes — doc, data, r, size, w, ctx and a few more,
// see snippetPreamble — are declared at package level, so a snippet that
// declares its own shadows them. An HTML comment directly above a fence
// adjusts this:
//
//	<!-- snippet
//	var req *http.Request
//	-->                                 declarations to add (imports allowed)
//	<!-- snippet in <import path> -->   check inside that package, as a rule
//	                                    recipe written in the package would be
//	<!-- snippet skip: <reason> -->     not Go; the reason is required
//	<!-- snippet verbatim <file> -->    a quotation: its lines must appear, in
//	                                    order and together, in that file
//
// Unused variables and imports are not reported: a fragment that reads a
// result into err and stops there is still an accurate fragment. Everything
// else the type checker reports fails the test, at the snippet's own line.
func TestDocSnippetsCompile(t *testing.T) {
	env := loadSnippetEnv(t)
	snips := collectSnippets(t)
	if len(snips) < 20 {
		t.Fatalf("found %d Go snippets; the scan is looking in the wrong place", len(snips))
	}
	checked := 0
	for _, s := range snips {
		if s.skip != "" {
			continue
		}
		checked++
		if s.verbatim != "" {
			if msg := checkVerbatim(t, s); msg != "" {
				t.Errorf("%s: %s", s.where, msg)
			}
			continue
		}
		for _, e := range env.check(s) {
			t.Errorf("%s", e)
		}
	}
	t.Logf("type-checked %d snippets (%d skipped with a reason)", checked, len(snips)-checked)
}

// snippetPreamble declares the names the documentation's prose sets up
// before a fragment uses them.
const snippetPreamble = `
var (
	doc       *pdf0.Document
	data      []byte
	r         io.ReaderAt
	size      int64
	w         io.Writer
	ctx       context.Context
	in        htmlpdf.Input
	opts      htmlpdf.Options
	err       error
	myProfile []byte
	buildBomb func(testing.TB) []byte
)
`

// stdImports are the standard library packages a snippet may name without
// importing them.
var stdImports = map[string]string{
	"bytes": "bytes", "context": "context", "errors": "errors", "fmt": "fmt",
	"io": "io", "iter": "iter", "log": "log", "math": "math", "os": "os",
	"strings": "strings", "time": "time", "testing": "testing", "slices": "slices",
	"sort": "sort", "strconv": "strconv", "sync": "sync", "filepath": "path/filepath",
	"x509": "crypto/x509", "crypto": "crypto", "ecdsa": "crypto/ecdsa", "rsa": "crypto/rsa",
	"elliptic": "crypto/elliptic", "rand": "crypto/rand", "sha256": "crypto/sha256",
	"image": "image", "png": "image/png", "jpeg": "image/jpeg", "color": "image/color",
	"http": "net/http", "bufio": "bufio", "flag": "flag", "unicode": "unicode",
	"utf8": "unicode/utf8", "hex": "encoding/hex", "json": "encoding/json", "xml": "encoding/xml",
}

type snippet struct {
	where     string // file:line, for messages
	file      string // slash path relative to the root
	line      int
	body      string
	skip      string // why it is not checked
	in        string // import path to check it inside, or ""
	verbatim  string // file the snippet quotes, or ""
	decls     string // extra declarations from the directive
	internal  bool   // may name the module's internal packages
	isComment bool
}

func collectSnippets(t *testing.T) []snippet {
	t.Helper()
	var out []snippet
	for _, rel := range markdownFiles(t) {
		if isHistorical(rel) {
			continue
		}
		for _, f := range readMarkdown(t, rel).fences {
			if f.lang != "go" {
				continue
			}
			s := snippet{where: fmt.Sprintf("%s:%d", f.file, f.line), file: f.file, line: f.line, body: f.body}
			if f.hasDir {
				switch d := f.directive; {
				case strings.HasPrefix(d, "skip"):
					reason := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(d, "skip"), ":"))
					if reason == "" {
						t.Errorf("%s: a skipped snippet must say why", s.where)
					}
					s.skip = reason
				case strings.HasPrefix(d, "in "):
					s.in = strings.TrimSpace(strings.TrimPrefix(d, "in "))
				case strings.HasPrefix(d, "verbatim "):
					s.verbatim = strings.TrimSpace(strings.TrimPrefix(d, "verbatim "))
				default:
					s.decls = d
				}
			}
			out = append(out, s)
		}
	}
	for _, b := range docCommentBlocks(t) {
		if isCommandBlock(b.text) || !namesModulePackage(b.text) {
			continue
		}
		out = append(out, snippet{
			where: fmt.Sprintf("%s:%d (doc comment)", b.file, b.line), file: b.file, line: b.line,
			body: b.text, internal: strings.HasPrefix(b.file, "internal/"), isComment: true,
		})
	}
	return out
}

// namesModulePackage reports whether a comment code block uses one of the
// module's packages — what tells a Go example from a block of PDF syntax or a
// diagram, which a doc comment may also indent.
func namesModulePackage(text string) bool {
	for name := range modulePackageNames {
		if strings.Contains(text, name+".") {
			return true
		}
	}
	return false
}

// modulePackageNames maps the name of each of the module's library packages
// to its import path. The internal ones are marked, since only a snippet
// inside internal/ may use them.
var modulePackageNames = map[string]struct {
	path     string
	internal bool
}{}

type snippetEnv struct {
	exports map[string]string   // import path -> export data file
	pkgs    map[string][]string // import path -> its non-test Go files
}

var (
	snippetEnvOnce sync.Once
	snippetEnvVal  *snippetEnv
	snippetEnvErr  error
)

func loadSnippetEnv(t *testing.T) *snippetEnv {
	t.Helper()
	snippetEnvOnce.Do(func() { snippetEnvVal, snippetEnvErr = buildSnippetEnv() })
	if snippetEnvErr != nil {
		t.Fatal(snippetEnvErr)
	}
	return snippetEnvVal
}

func loadModulePackageNames() error {
	if len(modulePackageNames) > 0 {
		return nil
	}
	lines, err := goList("-f", "{{.Name}}\t{{.ImportPath}}", "github.com/mgilbir/pdf0/...")
	if err != nil {
		return err
	}
	for _, l := range lines {
		name, path, _ := strings.Cut(l, "\t")
		if name == "main" || name == "lint" || name == "scripts" {
			continue
		}
		modulePackageNames[name] = struct {
			path     string
			internal bool
		}{path, strings.Contains(path, "/internal/")}
	}
	return nil
}

func buildSnippetEnv() (*snippetEnv, error) {
	if err := loadModulePackageNames(); err != nil {
		return nil, err
	}
	args := []string{"-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", "github.com/mgilbir/pdf0/..."}
	for _, p := range stdImports {
		args = append(args, p)
	}
	lines, err := goList(args...)
	if err != nil {
		return nil, err
	}
	env := &snippetEnv{exports: map[string]string{}, pkgs: map[string][]string{}}
	for _, l := range lines {
		path, file, _ := strings.Cut(l, "\t")
		if file != "" {
			env.exports[path] = file
		}
	}
	listed, err := goList("-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \",\"}}", "github.com/mgilbir/pdf0/...")
	if err != nil {
		return nil, err
	}
	for _, l := range listed {
		parts := strings.Split(l, "\t")
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		for _, f := range strings.Split(parts[2], ",") {
			env.pkgs[parts[0]] = append(env.pkgs[parts[0]], filepath.Join(parts[1], f))
		}
	}
	return env, nil
}

// check type-checks one snippet and returns what is wrong with it.
func (env *snippetEnv) check(s snippet) []string {
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := env.exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	var problems []string
	conf := types.Config{Importer: imp, Error: func(err error) {
		te, ok := err.(types.Error)
		if ok && te.Soft && (strings.Contains(te.Msg, "declared and not used") || strings.Contains(te.Msg, "imported and not used")) {
			return
		}
		problems = append(problems, err.Error())
	}}
	lineDirective := fmt.Sprintf("//line %s:%d\n", s.file, s.line)
	if s.isComment {
		// A comment's block has no line of its own to point at; name the
		// comment and let the column-free position say which line of the
		// block it is.
		lineDirective = fmt.Sprintf("//line %s(doc comment at line %d):1\n", s.file, s.line)
	}

	parse := func(name, src string) (*ast.File, error) {
		return parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	}

	// Inside a package: the snippet is a file of declarations added to it.
	if s.in != "" {
		files := env.pkgs[s.in]
		if len(files) == 0 {
			return []string{fmt.Sprintf("%s: snippet directive names %q, which is not a package of this module", s.where, s.in)}
		}
		var pkgFiles []*ast.File
		for _, f := range files {
			af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
			if err != nil {
				return []string{err.Error()}
			}
			pkgFiles = append(pkgFiles, af)
		}
		name := pkgFiles[0].Name.Name
		imports := map[string]string{}
		for _, af := range pkgFiles {
			for _, is := range af.Imports {
				p, _ := strconv.Unquote(is.Path.Value)
				n := p[strings.LastIndex(p, "/")+1:]
				if is.Name != nil {
					n = is.Name.Name
				}
				imports[n] = p
			}
		}
		body := s.body
		src := "package " + name + "\n" + importBlock(body, imports, true) + lineDirective + body + "\n"
		af, err := parse("snippet.go", src)
		if err != nil {
			return []string{fmt.Sprintf("%s: does not parse as declarations in package %s: %v", s.where, name, err)}
		}
		if _, err := conf.Check(s.in, fset, append(pkgFiles, af), nil); err != nil && len(problems) == 0 {
			problems = append(problems, err.Error())
		}
		return problems
	}

	// A whole file.
	if strings.HasPrefix(strings.TrimSpace(s.body), "package ") {
		af, err := parse("snippet.go", lineDirective+s.body)
		if err != nil {
			return []string{fmt.Sprintf("%s: %v", s.where, err)}
		}
		conf.Check("snippet", fset, []*ast.File{af}, nil)
		return problems
	}

	// Declarations, or a function body.
	extraImports, extraDecls := splitImports(s.decls)
	var af *ast.File
	declMode := false
	src := "package snippet\n" + importBlock(s.body, nil, s.internal) + extraImports + lineDirective + s.body + "\n"
	if f, err := parse("snippet.go", src); err == nil {
		af, declMode = f, true
	} else {
		src = "package snippet\n" + importBlock(s.body, nil, s.internal) + extraImports +
			"func _() error {\n" + lineDirective + s.body + "\nreturn nil\n}\n"
		f, err := parse("snippet.go", src)
		if err != nil {
			return []string{fmt.Sprintf("%s: parses neither as declarations nor as statements: %v", s.where, err)}
		}
		af = f
	}

	// The preamble, less any name the snippet (in declaration mode) or its
	// directive declares at package level.
	declared := map[string]bool{}
	if declMode {
		for n := range topLevelNames(af) {
			declared[n] = true
		}
	}
	if extraDecls != "" {
		df, err := parse("directive.go", "package snippet\n"+extraImports+extraDecls)
		if err != nil {
			return []string{fmt.Sprintf("%s: the snippet directive's declarations do not parse: %v", s.where, err)}
		}
		for n := range topLevelNames(df) {
			declared[n] = true
		}
	}
	var pre strings.Builder
	for _, l := range strings.Split(snippetPreamble, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] != "var" && !declared[f[0]] {
			pre.WriteString(l + "\n")
		}
	}
	preSrc := "var (\n" + pre.String() + ")\n" + extraDecls + "\n"
	pf, err := parse("preamble.go", "package snippet\n"+importBlock(preSrc, nil, true)+extraImports+preSrc)
	if err != nil {
		return []string{fmt.Sprintf("%s: internal error building the preamble: %v", s.where, err)}
	}
	conf.Check("snippet", fset, []*ast.File{af, pf}, nil)
	return problems
}

// importBlock returns an import declaration for every package src names
// through a selector: the module's packages, the standard library ones in
// stdImports, and the extra names given.
func importBlock(src string, extra map[string]string, internal bool) string {
	var paths []string
	seen := map[string]bool{}
	add := func(name, path string) {
		if !seen[name] {
			seen[name] = true
			paths = append(paths, fmt.Sprintf("%s %q", name, path))
		}
	}
	for i := 0; i < len(src); i++ {
		if !isIdentStart(src[i]) || (i > 0 && (isIdentPart(src[i-1]) || src[i-1] == '.')) {
			continue
		}
		j := i
		for j < len(src) && isIdentPart(src[j]) {
			j++
		}
		if j < len(src) && src[j] == '.' {
			name := src[i:j]
			if p, ok := extra[name]; ok {
				add(name, p)
			} else if m, ok := modulePackageNames[name]; ok && (internal || !m.internal) {
				add(name, m.path)
			} else if p, ok := stdImports[name]; ok {
				add(name, p)
			}
		}
		i = j
	}
	if len(paths) == 0 {
		return ""
	}
	slices.Sort(paths)
	return "import (\n\t" + strings.Join(paths, "\n\t") + "\n)\n"
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }

// splitImports separates import lines in a directive from its declarations.
func splitImports(decls string) (imports, rest string) {
	var im, re []string
	for _, l := range strings.Split(decls, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "import ") {
			im = append(im, l)
		} else {
			re = append(re, l)
		}
	}
	if len(im) > 0 {
		imports = strings.Join(im, "\n") + "\n"
	}
	return imports, strings.Join(re, "\n")
}

func topLevelNames(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				out[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					out[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						out[n.Name] = true
					}
				}
			}
		}
	}
	return out
}

// checkVerbatim reports whether a snippet that says it quotes a file does:
// its lines, trailing white space aside, must be consecutive lines of it.
func checkVerbatim(t *testing.T, s snippet) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testfiles.Root(t), filepath.FromSlash(s.verbatim)))
	if err != nil {
		return fmt.Sprintf("quotes %s, which cannot be read: %v", s.verbatim, err)
	}
	trim := func(src string) []string {
		lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t\r")
		}
		return lines
	}
	want, have := trim(s.body), trim(string(b))
	for i := 0; i+len(want) <= len(have); i++ {
		if slices.Equal(have[i:i+len(want)], want) {
			return ""
		}
	}
	return fmt.Sprintf("says it quotes %s verbatim, and the file no longer contains it", s.verbatim)
}
