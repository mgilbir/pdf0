package lint

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// module is the type-checked source of every package in this module, loaded
// once for all the checks in this file.
type module struct {
	fset *token.FileSet
	pkgs []*checkedPackage
}

type checkedPackage struct {
	path  string
	files []*ast.File
	info  *types.Info
	types *types.Package
}

var (
	loadOnce sync.Once
	loaded   *module
	loadErr  error
)

// load type-checks every non-test package of the module. Dependencies,
// including the module's own packages as seen by each other, are read from
// the compiler's export data, which `go list -export` builds and names; only
// the package under inspection is type-checked from source, so its
// types.Info is complete.
func load(t *testing.T) *module {
	t.Helper()
	loadOnce.Do(func() { loaded, loadErr = loadModule() })
	if loadErr != nil {
		t.Fatalf("loading the module: %v", loadErr)
	}
	return loaded
}

func loadModule() (*module, error) {
	exports, err := goList("-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}", "github.com/mgilbir/pdf0/...")
	if err != nil {
		return nil, err
	}
	exportFile := map[string]string{}
	for _, line := range exports {
		path, file, _ := strings.Cut(line, "\t")
		if file != "" {
			exportFile[path] = file
		}
	}
	listed, err := goList("-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .GoFiles \",\"}}", "github.com/mgilbir/pdf0/...")
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exportFile[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(f)
	})
	m := &module{fset: fset}
	for _, line := range listed {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[2] == "" {
			continue // a directory with test files only
		}
		p := &checkedPackage{path: parts[0]}
		for _, name := range strings.Split(parts[2], ",") {
			f, err := parser.ParseFile(fset, filepath.Join(parts[1], name), nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			p.files = append(p.files, f)
		}
		p.info = &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
			Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		conf := types.Config{Importer: imp}
		p.types, err = conf.Check(p.path, fset, p.files, p.info)
		if err != nil {
			return nil, fmt.Errorf("type-checking %s: %w", p.path, err)
		}
		m.pkgs = append(m.pkgs, p)
	}
	if len(m.pkgs) == 0 {
		return nil, fmt.Errorf("go list found no packages")
	}
	return m, nil
}

func goList(args ...string) ([]string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if sc.Text() != "" {
			lines = append(lines, sc.Text())
		}
	}
	return lines, sc.Err()
}

// TestNoSlashBeforeANameVerb catches a PDF name formatted after a literal
// slash. object.Name's String method writes the name in PDF syntax, leading
// "/" included, so "/%s" applied to a Name prints "//Name". It shipped in the
// validated security handler's messages ("/Filter //Unknown1"), and the
// pattern is spread across dozens of messages in which "/%s" is correct —
// most format a plain string — so no search on spelling can tell the two
// apart. This check asks the type checker instead: for every call with a
// constant format string, each "/%s", "/%v" or "/%q" whose argument is an
// object.Name is a doubled slash.
//
// Any function is inspected, not only the fmt family, because the codebase
// wraps fmt in helpers (malformed, unsupported, violation builders) whose
// format arguments are forwarded unchanged.
func TestNoSlashBeforeANameVerb(t *testing.T) {
	m := load(t)
	var found []string
	for _, p := range m.pkgs {
		name := nameType(p.types)
		if name == nil {
			continue
		}
		for _, f := range p.files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				for i, arg := range call.Args {
					tv, ok := p.info.Types[arg]
					if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
						continue
					}
					for _, v := range slashVerbs(constant.StringVal(tv.Value)) {
						j := i + 1 + v
						if j >= len(call.Args) {
							continue
						}
						at, ok := p.info.Types[call.Args[j]]
						if ok && types.Identical(at.Type, name) {
							found = append(found, m.fset.Position(call.Args[j].Pos()).String())
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(found)
	for _, f := range found {
		t.Errorf("%s: an object.Name formatted after a literal \"/\"; Name's String already writes the slash", f)
	}
}

// nameType finds object.Name as p sees it: p itself if p is the object
// package, else through p's imports. Packages that cannot name the type
// cannot format one by its static type either, and are skipped.
func nameType(p *types.Package) types.Type {
	const objectPath = "github.com/mgilbir/pdf0/object"
	lookup := func(pkg *types.Package) types.Type {
		if obj := pkg.Scope().Lookup("Name"); obj != nil {
			return obj.Type()
		}
		return nil
	}
	if p.Path() == objectPath {
		return lookup(p)
	}
	for _, imp := range p.Imports() {
		if imp.Path() == objectPath {
			return lookup(imp)
		}
	}
	return nil
}

// slashVerbs returns, for each s, v or q verb in format that directly follows
// a literal "/", the verb's argument index (0 for the first argument after
// the format). Explicit argument indexes ("%[2]s") reset the counter as fmt
// does.
func slashVerbs(format string) []int {
	var out []int
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		start := i
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		// flags, width, precision, explicit index
		for i < len(format) && strings.IndexByte("+-# 0123456789.*[]", format[i]) >= 0 {
			if format[i] == '[' {
				end := strings.IndexByte(format[i:], ']')
				if end > 0 {
					var n int
					if _, err := fmt.Sscanf(format[i+1:i+end], "%d", &n); err == nil && n > 0 {
						arg = n - 1
					}
					i += end
				}
			} else if format[i] == '*' {
				arg++ // the width or precision consumes an argument
			}
			i++
		}
		if i >= len(format) {
			break
		}
		verb := format[i]
		if start > 0 && format[start-1] == '/' && (verb == 's' || verb == 'v' || verb == 'q') {
			out = append(out, arg)
		}
		arg++
	}
	return out
}

func TestSlashVerbs(t *testing.T) {
	for _, c := range []struct {
		format string
		want   []int
	}{
		{"/%s", []int{0}},
		{"%d /%s", []int{1}},
		{"/%% /%s", []int{0}},
		{"%s/%v and /%q", []int{1, 2}},
		{"/%[2]s /%[1]s", []int{1, 0}},
		{"/%*s", []int{1}},
		{"a/b %s", nil},
		{"/%d", nil},
	} {
		got := slashVerbs(c.format)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("slashVerbs(%q) = %v, want %v", c.format, got, c.want)
		}
	}
}
