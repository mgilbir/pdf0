package htmlpdf

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/forme/layout"
)

// TestEveryDrawOpFieldIsAccountedFor holds drawnFields to forme's display
// list: every operation layout declares is listed, and every field of each
// is, so a field or an operation added upstream fails here until the backend
// says what it does with it (audit 2026-09-22 C27, C114: DrawText's
// Sideways, Anticlockwise and Upright were read by nothing).
func TestEveryDrawOpFieldIsAccountedFor(t *testing.T) {
	ops := map[string]reflect.Type{
		"FillRect":  reflect.TypeOf(layout.FillRect{}),
		"DrawText":  reflect.TypeOf(layout.DrawText{}),
		"DrawImage": reflect.TypeOf(layout.DrawImage{}),
		"TileImage": reflect.TypeOf(layout.TileImage{}),
	}
	// The operation set itself, from the source: Op's method is unexported,
	// so the types that implement it are exactly the ones layout declares a
	// method isOp on, and reflection cannot list them.
	declared := declaredOps(t)
	if !reflect.DeepEqual(declared, sortedKeys(ops)) {
		t.Errorf("layout declares the operations %v; this backend knows %v", declared, sortedKeys(ops))
	}
	if !reflect.DeepEqual(declared, sortedKeys(drawnFields)) {
		t.Errorf("layout declares the operations %v; drawnFields accounts for %v", declared, sortedKeys(drawnFields))
	}
	for name, typ := range ops {
		var fields []string
		for i := 0; i < typ.NumField(); i++ {
			if f := typ.Field(i); f.IsExported() {
				fields = append(fields, f.Name)
			}
		}
		sort.Strings(fields)
		if listed := sortedKeys(drawnFields[name]); !reflect.DeepEqual(fields, listed) {
			t.Errorf("layout.%s has the fields %v; drawnFields says what happens to %v",
				name, fields, listed)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// declaredOps parses forme's layout package, as this build resolves it, for
// the receiver types of isOp.
func declaredOps(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}", "github.com/mgilbir/forme/layout").Output()
	if err != nil {
		t.Fatalf("locating forme's layout package: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var ops []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "isOp" || len(fn.Recv.List) != 1 {
				continue
			}
			switch r := fn.Recv.List[0].Type.(type) {
			case *ast.Ident:
				ops = append(ops, r.Name)
			case *ast.StarExpr:
				if id, ok := r.X.(*ast.Ident); ok {
					ops = append(ops, id.Name)
				}
			}
		}
	}
	sort.Strings(ops)
	if len(ops) == 0 {
		t.Fatal("found no isOp methods in forme's layout package; the scan is broken")
	}
	return ops
}
