package lint

// declIndex: every name declared in the module's source and in the source of
// the modules it is documented alongside, built by parsing (not
// type-checking), so that it sees every file whatever its build tags.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// indexedModules are the dependencies whose source the documentation may
// point into: the shaping and layout engine, the invoice rules, the colour
// engine and the JPEG 2000 codec all moved out of this repository, and the
// docs say where they went.
var indexedModules = []string{
	"github.com/mgilbir/forme",
	"github.com/mgilbir/formalis",
	"github.com/mgilbir/golittlecms",
	"github.com/mgilbir/gopenjpeg",
}

type pkgDecls struct {
	name    string
	dir     string                     // absolute
	top     map[string]bool            // package-level names
	members map[string]map[string]bool // type name -> field and method names
	embeds  map[string][]string        // type name -> embedded type names (unqualified)
	funcs   map[string]bool            // package-level func names, test files included
}

type declIndex struct {
	byName map[string][]*pkgDecls // package name -> packages of that name
	types  map[string][]*pkgDecls // type name -> packages declaring it
	all    map[string]bool        // every declared name, locals included
	// literal holds every identifier-shaped word of every string literal:
	// the PDF names, operators and keys the code handles ("MediaBox",
	// "Identity-H", "BDC"), which the docs put in backticks as often as
	// they do Go names.
	literal map[string]bool
	// makeVars are the Makefile's variables (FUZZTIME, CORPUS_DIR), which
	// the docs name as settings.
	makeVars map[string]bool
	roots    []string // module root first, then the indexed modules
	modRoot  string
}

var (
	indexOnce sync.Once
	indexVal  *declIndex
	indexErr  error
)

func loadDeclIndex(t *testing.T) *declIndex {
	t.Helper()
	root := testfiles.Root(t)
	indexOnce.Do(func() { indexVal, indexErr = buildDeclIndex(root) })
	if indexErr != nil {
		t.Fatal(indexErr)
	}
	return indexVal
}

func buildDeclIndex(root string) (*declIndex, error) {
	idx := &declIndex{
		byName: map[string][]*pkgDecls{}, types: map[string][]*pkgDecls{},
		all: map[string]bool{}, literal: map[string]bool{}, makeVars: map[string]bool{},
		roots: []string{root}, modRoot: root,
	}
	dirs, err := goList(append([]string{"-m", "-f", "{{.Dir}}"}, indexedModules...)...)
	if err != nil {
		return nil, err
	}
	if len(dirs) != len(indexedModules) {
		return nil, fmt.Errorf("go list -m found %d of the %d indexed modules", len(dirs), len(indexedModules))
	}
	idx.roots = append(idx.roots, dirs...)
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return nil, err
	}
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)\s*[?:]?=`).FindAllStringSubmatch(string(mk), -1) {
		idx.makeVars[m[1]] = true
	}
	for _, r := range idx.roots {
		if err := idx.addTree(r); err != nil {
			return nil, err
		}
	}
	goroot, err := goEnv("GOROOT")
	if err != nil {
		return nil, err
	}
	for _, p := range stdImports {
		if err := idx.addDir(filepath.Join(goroot, "src", filepath.FromSlash(p)), false); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func goEnv(key string) (string, error) {
	out, err := goToolOutput("env", key)
	return strings.TrimSpace(out), err
}

func (idx *declIndex) addTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (skippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		return idx.addDir(p, true)
	})
}

// addDir indexes the Go files in one directory; tests says whether test
// files count (they do for the module and its siblings, whose test names the
// docs cite, and not for the standard library).
func (idx *declIndex) addDir(dir string, tests bool) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	pkgs := map[string]*pkgDecls{}
	fset := token.NewFileSet()
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || (!tests && strings.HasSuffix(n, "_test.go")) {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			continue // a file that does not parse declares nothing a doc can cite
		}
		name := strings.TrimSuffix(f.Name.Name, "_test")
		pd := pkgs[name]
		if pd == nil {
			pd = &pkgDecls{name: name, dir: dir, top: map[string]bool{}, members: map[string]map[string]bool{},
				embeds: map[string][]string{}, funcs: map[string]bool{}}
			pkgs[name] = pd
		}
		// The documentation checks' own test data plants stale names as
		// string literals to see them caught; those must not count as a
		// string the code uses.
		idx.addFile(pd, f, filepath.Base(dir) != "lint" || filepath.Base(filepath.Dir(dir)) != "internal")
	}
	for name, pd := range pkgs {
		idx.byName[name] = append(idx.byName[name], pd)
		for tn := range pd.members {
			idx.types[tn] = append(idx.types[tn], pd)
		}
	}
	return nil
}

var literalWord = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func (idx *declIndex) addFile(pd *pkgDecls, f *ast.File, literals bool) {
	// Every name the file declares counts, not only the package-level ones: a
	// doc explaining a function may name what it computes, which is often a
	// local, and a table's fields are often an anonymous struct's. So do
	// parameters and results, := and var declarations, range variables, local
	// types and constants — and every identifier-shaped word of every string
	// literal, kept apart as literal.
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BasicLit:
			if literals && n.Kind == token.STRING {
				for _, w := range literalWord.FindAllString(n.Value, -1) {
					idx.literal[w] = true
				}
			}
		case *ast.Field:
			for _, id := range n.Names {
				idx.all[id.Name] = true
			}
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, e := range n.Lhs {
					if id, ok := e.(*ast.Ident); ok {
						idx.all[id.Name] = true
					}
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						idx.all[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				idx.all[id.Name] = true
			}
		case *ast.TypeSpec:
			idx.all[n.Name.Name] = true
		}
		return true
	})
	member := func(typ, name string) {
		if pd.members[typ] == nil {
			pd.members[typ] = map[string]bool{}
		}
		pd.members[typ][name] = true
		idx.all[name] = true
	}
	addType := func(s *ast.TypeSpec) {
		pd.top[s.Name.Name] = true
		idx.all[s.Name.Name] = true
		if pd.members[s.Name.Name] == nil {
			pd.members[s.Name.Name] = map[string]bool{}
		}
		switch tt := s.Type.(type) {
		case *ast.StructType:
			for _, fl := range tt.Fields.List {
				if len(fl.Names) == 0 {
					if n := baseTypeName(fl.Type); n != "" {
						pd.embeds[s.Name.Name] = append(pd.embeds[s.Name.Name], n)
						member(s.Name.Name, n)
					}
				}
				for _, n := range fl.Names {
					member(s.Name.Name, n.Name)
				}
			}
		case *ast.InterfaceType:
			for _, fl := range tt.Methods.List {
				for _, n := range fl.Names {
					member(s.Name.Name, n.Name)
				}
				if len(fl.Names) == 0 {
					if n := baseTypeName(fl.Type); n != "" {
						pd.embeds[s.Name.Name] = append(pd.embeds[s.Name.Name], n)
					}
				}
			}
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			idx.all[d.Name.Name] = true
			if d.Recv == nil {
				pd.top[d.Name.Name] = true
				pd.funcs[d.Name.Name] = true
			} else if len(d.Recv.List) == 1 {
				if n := baseTypeName(d.Recv.List[0].Type); n != "" {
					member(n, d.Name.Name)
				}
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					addType(s)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						pd.top[n.Name] = true
						idx.all[n.Name] = true
					}
				}
			}
		}
	}
}

// baseTypeName is the unqualified name of a receiver or embedded type:
// T, *T, T[P], pkg.T.
func baseTypeName(e ast.Expr) string {
	for {
		switch x := e.(type) {
		case *ast.StarExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.SelectorExpr:
			return x.Sel.Name
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

// hasMember reports whether any type named typ, in any package of pkgs (or
// any package at all when pkgs is nil), has a field or method name, directly
// or through embedding.
func (idx *declIndex) hasMember(pkgs []*pkgDecls, typ, name string) bool {
	if pkgs == nil {
		pkgs = idx.types[typ]
	}
	for _, pd := range pkgs {
		if idx.memberIn(pd, typ, name, 0) {
			return true
		}
	}
	return false
}

func (idx *declIndex) memberIn(pd *pkgDecls, typ, name string, depth int) bool {
	if depth > 8 {
		return false
	}
	if pd.members[typ][name] {
		return true
	}
	for _, e := range pd.embeds[typ] {
		// The embedded type may be declared in this package or another.
		if _, ok := pd.members[e]; ok {
			if idx.memberIn(pd, e, name, depth+1) {
				return true
			}
			continue
		}
		for _, other := range idx.types[e] {
			if idx.memberIn(other, e, name, depth+1) {
				return true
			}
		}
	}
	return false
}
