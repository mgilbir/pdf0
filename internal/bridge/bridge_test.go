package bridge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

func mustPanic(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("no panic; want one containing %q", want)
		}
		if s, _ := r.(string); !strings.Contains(s, want) {
			t.Fatalf("panic %v; want one containing %q", r, want)
		}
	}()
	f()
}

func TestResolveChecksTheType(t *testing.T) {
	e := &Entry{name: "pkg.fn"}
	e.Install(func(n int) string { return strings.Repeat("x", n) })
	if got := Resolve[func(int) string](e)(3); got != "xxx" {
		t.Fatalf("resolved function returned %q", got)
	}
	mustPanic(t, "pkg.fn is func(int) string, and its caller expects func(int) int", func() {
		Resolve[func(int) int](e)
	})
}

func TestResolveOfAnUninstalledEntryPanics(t *testing.T) {
	mustPanic(t, "pkg.missing is <nil>", func() { Resolve[func()](&Entry{name: "pkg.missing"}) })
}

func TestInstallIsOnceAndBeforeResolve(t *testing.T) {
	e := &Entry{name: "pkg.fn"}
	e.Install(func() {})
	mustPanic(t, "installed twice", func() { e.Install(func() {}) })

	late := &Entry{name: "pkg.late"}
	mustPanic(t, "is <nil>", func() { Resolve[func()](late) })
	mustPanic(t, "installed after it was resolved", func() { late.Install(func() {}) })

	mustPanic(t, "installed as nil", func() { (&Entry{name: "pkg.nil"}).Install(nil) })
}

// TestEntriesListsEveryEntry keeps Entries in step with the variables, so the
// root package's test that every entry is installed and resolved covers all
// of them.
func TestEntriesListsEveryEntry(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "bridge.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, s := range g.Specs {
			for _, n := range s.(*ast.ValueSpec).Names {
				declared = append(declared, n.Name)
			}
		}
	}
	names := map[*Entry]string{}
	for _, e := range Entries() {
		names[e] = e.name
	}
	if len(names) != len(Entries()) {
		t.Errorf("Entries lists an entry twice")
	}
	if len(declared) != len(names) {
		t.Errorf("bridge.go declares %d entries %v, Entries lists %d", len(declared), declared, len(names))
	}
	var listed []string
	for _, n := range names {
		listed = append(listed, n)
	}
	sort.Strings(listed)
	for i := 1; i < len(listed); i++ {
		if listed[i] == listed[i-1] {
			t.Errorf("two entries are named %s", listed[i])
		}
	}
}
