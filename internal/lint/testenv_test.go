package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// testEnvironment is what the tests use from outside the module: the
// environment variables they read and the programs they run. A test that
// finds a renderer, a reference validator or an interpreter missing skips,
// and a skip reads as a pass, so a contributor who has never heard of
// PDF0_VERAPDF runs the Type 3 fonts' veraPDF check never (docs audit
// 2026-10-11 D30). The testing guide's table is generated from this list, and
// TestTestEnvironmentIsDocumented fails when a test reads a variable or runs a
// program this list does not describe, or the list describes one nothing uses.
//
// The data sets' override variables (VERAPDF_CORPUS, …) are not here: the
// data set table documents them, from internal/testfiles/datasets.go.
var testEnvironment = []struct {
	name string
	tool bool   // a program, rather than an environment variable
	what string // what it is for, as the table says it
	// without is what happens when it is absent; for a variable, when it is
	// unset.
	without string
	// readBy is a file outside the Go tests that uses it, for a name no test
	// reads itself.
	readBy string
}{
	{name: "PDF0_VERAPDF", what: "the veraPDF command the Type 3 and CIDSet tests validate their documents with; [testdata/verapdf/verapdf-docker.sh](../testdata/verapdf/README.md) runs the official image (needs docker)", without: "those checks skip"},
	{name: "PDF0_SKIA_PYTHON", what: "a Python with the packages in `testdata/colrv1/requirements.txt`, which renders COLR glyphs with Skia ([testdata/colrv1/README.md](../testdata/colrv1/README.md))", without: "the Skia comparison skips"},
	{name: "PDF0_CLI_BIN", what: "a prebuilt `pdf0` command for the CLI's black-box tests, to point them at an older build and watch them fail", without: "the tests build the command from the checkout"},
	{name: "PDF0_LARGE_PDF", what: "a large real PDF to measure cancellation latency on", without: "the measurement skips"},
	{name: "PDF0_WORK_CORPUS", what: "a directory of further PDFs for the work-budget headroom measurement", without: "only the data sets present are measured"},
	{name: "PDF0_WORK_BIG", what: "any value: include files larger than the headroom measurement's size cap", without: "the largest files are left out"},
	{name: "PDF0_WORK_PROGRESS", what: "any value: name each file on stderr as the headroom measurement reaches it", without: "silent"},
	{name: "PDF0_WORK_CSV", what: "a file to write the headroom measurement's per-file samples to, as CSV", without: "no CSV is written"},
	{name: "PATH", what: "where the programs below are found; the CLI transcripts put the command they built first", without: "—"},
	{name: "CORPUSPROBE", readBy: "testdata/cc/sweep.sh", what: "the built `corpusprobe` the Common Crawl sweep runs; `make cc-sweep-limited` sets it", without: "the sweep runs `testdata/cc/run/corpusprobe`, which `make cc-sweep` builds"},
	{name: "go", tool: true, what: "the Go command: it builds what the CLI, example and developer-tool tests execute, and lists the packages the lints read", without: "those tests fail"},
	{name: "git", tool: true, what: "checks that no binary is committed, that `.gitignore` covers every command's output, and that THIRD_PARTY_NOTICES names every committed file that may be someone else's", without: "those tests fail"},
	{name: "bash", tool: true, what: "runs `scripts/check-no-binaries.sh` under its test", without: "that test fails"},
	{name: "/bin/sh", tool: true, what: "runs the CLI transcripts in `docs/cli.md` and the CLI tests that need a redirect or a limit", without: "those tests fail; the transcript test skips on Windows"},
	{name: "gs", tool: true, what: "Ghostscript: renders the pages the bitmap-font, colour-glyph and htmlpdf tests compare with what should be there", without: "the comparisons run with whichever of gs and pdftoppm is present, and skip with neither; htmlpdf's vertical-text ink check needs gs itself and skips"},
	{name: "pdftoppm", tool: true, what: "poppler's renderer, the second rasteriser beside Ghostscript", without: "as for gs"},
	{name: "pdftotext", tool: true, what: "poppler's text extractor: the spec-example pipeline, a table checked against ISO 32000-2's text, and htmlpdf's vertical text", without: "those tests skip"},
	{name: "pdfinfo", tool: true, what: "poppler's reader, as an outside opinion of the documents the encryption tests write", without: "those interop checks skip"},
	{name: "hb-view", tool: true, what: "HarfBuzz's renderer: a second opinion on how a COLRv1 emoji is painted", without: "that comparison skips"},
	{name: "python3", tool: true, what: "the spec-example extractors, the stringprep cross-check (Python's own tables) and, with PyMuPDF installed, MuPDF's opinion of an encrypted file's passwords", without: "those tests skip"},
}

// TestTestEnvironmentIsDocumented: every environment variable a test reads
// and every program a test runs is in testEnvironment, and everything there
// is used.
func TestTestEnvironmentIsDocumented(t *testing.T) {
	root := testfiles.Root(t)
	used := testEnvironmentReaders(t, root)
	listed := map[string]bool{}
	for _, e := range testEnvironment {
		listed[e.name] = true
		if e.what == "" || e.without == "" {
			t.Errorf("testEnvironment: %s does not say what it is for and what happens without it", e.name)
		}
		if _, ok := used[e.name]; ok {
			continue
		}
		if e.readBy == "" {
			t.Errorf("testEnvironment lists %s, which no test uses: remove it, or name the file that does in readBy", e.name)
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.readBy)))
		if err != nil || !strings.Contains(string(b), e.name) {
			t.Errorf("testEnvironment: %s is said to be read by %s, which does not mention it (%v)", e.name, e.readBy, err)
		}
	}
	for name, readers := range used {
		if !listed[name] {
			t.Errorf("%s, used by %s, is not in the testing guide: add it to testEnvironment (internal/lint/testenv_test.go) and regenerate", name, strings.Join(readers, ", "))
		}
	}
	if len(used) < 5 {
		t.Fatalf("found %d variables and programs used by tests; the scan is wrong", len(used))
	}
}

// testEnvironmentReaders maps each environment variable a test reads, and
// each program a test runs, to the tests that do, directly or through
// helpers in the same package (labelled as datasetReaders labels them). What
// counts: a string literal passed to os.Getenv, os.LookupEnv, exec.LookPath
// or as exec.Command's program, or passed to a helper of the same package
// that hands that parameter to one of them. A data set's override variable is
// left out; the data set table documents those.
func testEnvironmentReaders(t *testing.T, root string) map[string][]string {
	t.Helper()
	overrides := map[string]bool{}
	for _, d := range parseDatasets(t, root) {
		if d.env != "" {
			overrides[d.env] = true
		}
	}
	type fn struct {
		decl  *ast.FuncDecl
		names map[string]bool
		calls map[string]bool
	}
	funcs := map[string]*fn{} // "dir\x00name"
	var order []string
	// passes maps "dir\x00name" to the parameter positions a function hands
	// to a lookup.
	passes := map[string][]int{}
	for _, rel := range goSourceFiles(t, true) {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := dir + "\x00" + funcKey(fd)
			funcs[key] = &fn{decl: fd, names: map[string]bool{}, calls: map[string]bool{}}
			order = append(order, key)
			params := map[string]int{}
			i := 0
			for _, field := range fd.Type.Params.List {
				for _, n := range field.Names {
					params[n.Name] = i
					i++
				}
				if len(field.Names) == 0 {
					i++
				}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isLookup(call) && len(call.Args) > 0 {
					if id, ok := call.Args[0].(*ast.Ident); ok {
						if p, ok := params[id.Name]; ok {
							passes[key] = append(passes[key], p)
						}
					}
				}
				return true
			})
		}
	}
	for _, key := range order {
		dir, _, _ := strings.Cut(key, "\x00")
		x := funcs[key]
		ast.Inspect(x.decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isLookup(call) && len(call.Args) > 0 {
				if s, ok := stringLit(call.Args[0]); ok {
					x.names[s] = true
				}
			}
			if callee := calleeKey(call); callee != "" {
				x.calls[callee] = true
				for _, p := range passes[dir+"\x00"+callee] {
					if p < len(call.Args) {
						if s, ok := stringLit(call.Args[p]); ok {
							x.names[s] = true
						}
					}
				}
			}
			return true
		})
	}
	out := map[string][]string{}
	for _, key := range order {
		dir, name, _ := strings.Cut(key, "\x00")
		if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Fuzz") && !strings.HasPrefix(name, "Benchmark") {
			continue
		}
		seen := map[string]bool{key: true}
		queue := []string{key}
		names := map[string]bool{}
		for len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			for n := range funcs[k].names {
				names[n] = true
			}
			for c := range funcs[k].calls {
				ck := dir + "\x00" + c
				if _, ok := funcs[ck]; ok && !seen[ck] {
					seen[ck] = true
					queue = append(queue, ck)
				}
			}
		}
		label := name
		if dir != "." {
			label = dir + ": " + name
		}
		for n := range names {
			if !overrides[n] {
				out[n] = append(out[n], label)
			}
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
}

// funcKey names a function, or a method by its name alone (".name"): a call
// through a value names no type, so methods are matched by name.
func funcKey(fd *ast.FuncDecl) string {
	if fd.Recv != nil {
		return "." + fd.Name.Name
	}
	return fd.Name.Name
}

// calleeKey is the funcKey a call may reach: a plain call names a function,
// a call through a selector a method.
func calleeKey(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return "." + f.Sel.Name
	}
	return ""
}

// isLookup reports whether call is os.Getenv, os.LookupEnv, exec.LookPath or
// exec.Command, whose first argument names a variable or a program.
func isLookup(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	switch pkg.Name + "." + sel.Sel.Name {
	case "os.Getenv", "os.LookupEnv", "exec.LookPath", "exec.Command":
		return true
	}
	return false
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// readersCell names the tests that use an entry; past a handful, the
// packages they are in, since a row naming forty tests is not read.
func readersCell(rs []string) string {
	if len(rs) <= 8 {
		return joinCode(rs)
	}
	var pkgs []string
	for _, r := range rs {
		pkg := "."
		if dir, _, ok := strings.Cut(r, ": "); ok {
			pkg = dir
		}
		if !slices.Contains(pkgs, pkg) {
			pkgs = append(pkgs, pkg)
		}
	}
	slices.Sort(pkgs)
	for i, p := range pkgs {
		if p == "." {
			pkgs[i] = "the root package"
		} else {
			pkgs[i] = "`" + p + "`"
		}
	}
	return "tests in " + strings.Join(pkgs, ", ")
}

// renderTestEnvironmentDoc is the testing guide's table of testEnvironment,
// with the tests that use each entry.
func renderTestEnvironmentDoc(t *testing.T, root string) string {
	t.Helper()
	used := testEnvironmentReaders(t, root)
	var b strings.Builder
	for _, tools := range []bool{false, true} {
		if tools {
			b.WriteString("\n\n| Program | What for | Without it | Run by |\n|---|---|---|---|\n")
		} else {
			b.WriteString("\n\n| Variable | What it is | Unset | Read by |\n|---|---|---|---|\n")
		}
		for _, e := range testEnvironment {
			if e.tool != tools {
				continue
			}
			by := "`" + e.readBy + "`"
			if rs := used[e.name]; len(rs) > 0 {
				by = readersCell(rs)
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", e.name, e.what, e.without, by)
		}
	}
	b.WriteString("\n")
	return b.String()
}
