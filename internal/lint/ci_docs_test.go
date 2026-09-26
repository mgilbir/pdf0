package lint

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

var update = flag.Bool("update", false, "rewrite the generated documentation sections instead of comparing them")

// generatedSections are the parts of the documentation written by a program
// rather than by hand, because each restates facts that live somewhere else
// and drifted when restated by hand: both CONTRIBUTING.md and the testing
// guide still said "Go 1.25.x, five steps, no corpus" two months and two jobs
// later (audit 2026-09-22 C105), and the testing guide's table named a test
// that no longer existed as a data set's reader.
var generatedSections = []struct {
	name   string
	source string
	files  []string
	render func(t *testing.T, root string) string
}{
	{
		name:   "ci",
		source: ".github/workflows/ci.yml and internal/testfiles/datasets.go",
		files:  []string{"CONTRIBUTING.md", "docs/testing.md"},
		render: func(t *testing.T, root string) string {
			return renderCIDoc(parseWorkflow(t, root), parseDatasets(t, root))
		},
	},
	{
		name:   "options",
		source: "the With* functions in limits.go",
		files:  []string{"docs/architecture.md"},
		render: func(t *testing.T, root string) string {
			return renderOptionsDoc(t, root)
		},
	},
	{
		name:   "datasets",
		source: "internal/testfiles/datasets.go and the tests that read each data set",
		files:  []string{"docs/testing.md"},
		render: func(t *testing.T, root string) string {
			return renderDatasetDoc(parseDatasets(t, root), datasetReaders(t, root))
		},
	},
}

func sectionMarkers(name, source string) (begin, end string) {
	return fmt.Sprintf("<!-- BEGIN GENERATED: %s. From %s by TestGeneratedDocSections; regenerate with `go test ./internal/lint -run TestGeneratedDocSections -update`. -->", name, source),
		fmt.Sprintf("<!-- END GENERATED: %s -->", name)
}

// TestGeneratedDocSections checks each generated section against what its
// program produces now, or rewrites it under -update.
func TestGeneratedDocSections(t *testing.T) {
	root := testfiles.Root(t)
	for _, sec := range generatedSections {
		want := sec.render(t, root)
		begin, end := sectionMarkers(sec.name, sec.source)
		for _, rel := range sec.files {
			path := filepath.Join(root, filepath.FromSlash(rel))
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			src := string(b)
			i, j := strings.Index(src, begin), strings.Index(src, end)
			if i < 0 || j < i {
				t.Errorf("%s has no generated %s section; add the two marker lines\n%s\n%s", rel, sec.name, begin, end)
				continue
			}
			got := src[i+len(begin) : j]
			if got == want {
				continue
			}
			if *update {
				if err := os.WriteFile(path, []byte(src[:i+len(begin)]+want+src[j:]), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("rewrote the %s section of %s", sec.name, rel)
				continue
			}
			t.Errorf("%s: the generated %s section is out of date with %s; run\n\tgo test ./internal/lint -run TestGeneratedDocSections -update\n--- got\n%s\n--- want\n%s", rel, sec.name, sec.source, got, want)
		}
	}
}

// TestCIRunsEveryFetchableOracle fails when a data set has a make target but
// CI's corpus job does not fetch it, cache it, and count what arrived. The
// oracle behind the rule-coverage ratchet, the only coverage of CID-keyed CFF
// embedding and three decode oracles all had targets and never ran in CI
// (audit 2026-09-22 C157).
func TestCIRunsEveryFetchableOracle(t *testing.T) {
	root := testfiles.Root(t)
	wf := parseWorkflow(t, root)
	sets := parseDatasets(t, root)
	fetchable := 0
	for _, d := range sets {
		if d.target == "" {
			continue
		}
		fetchable++
		if !wf.fetches[d.target] {
			t.Errorf("CI does not fetch %s: add `%s` to the corpus job's fetch step", d.name, d.target)
		}
		if !wf.counted(d.dataDir()) {
			t.Errorf("CI does not count the files of %s: add a `check %s …` line to the corpus job's gate", d.name, d.dataDir())
		}
		if !wf.cached(d.dir) {
			t.Errorf("CI does not cache %s: add %s to the corpus job's cache paths", d.name, d.dir)
		}
	}
	if fetchable < 5 {
		t.Fatalf("found %d fetchable data sets; parsing internal/testfiles/datasets.go went wrong", fetchable)
	}
}

// TestFuzzTargetsAllRun fails when a fuzz target exists that `make fuzz`
// (which CI runs) does not fuzz. Under a plain go test a target only replays
// its seeds; three existed that nothing ever fuzzed.
func TestFuzzTargetsAllRun(t *testing.T) {
	root := testfiles.Root(t)
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^FUZZ_TARGETS\s*:?=\s*(.*)$`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("the Makefile has no FUZZ_TARGETS")
	}
	listed := strings.Fields(m[1])
	var found []string
	for _, rel := range goSourceFiles(t, true) {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Fuzz") {
				if strings.Contains(rel, "/") {
					t.Errorf("%s: %s is outside the root package, and `make fuzz` fuzzes only the root package", rel, fd.Name.Name)
				}
				found = append(found, fd.Name.Name)
			}
		}
	}
	slices.Sort(found)
	slices.Sort(listed)
	if !slices.Equal(found, listed) {
		t.Errorf("FUZZ_TARGETS in the Makefile is %v; the fuzz targets are %v", listed, found)
	}
	if !parseWorkflow(t, root).fetches["fuzz"] {
		t.Error("CI does not run `make fuzz`")
	}
}

type workflow struct {
	triggers []string
	jobs     []*ciJob
	fetches  map[string]bool // every make target any run: line names
	gates    []ciGate
	caches   []string
}

type ciJob struct {
	name, goVersion string
	steps           []*ciStep
}

type ciStep struct {
	name, uses, cond string
	run              []string
	paths            []string
}

type ciGate struct{ dir, glob, min string }

func (w *workflow) counted(dir string) bool {
	for _, g := range w.gates {
		if g.dir == dir {
			return true
		}
	}
	return false
}

func (w *workflow) cached(dir string) bool {
	for _, c := range w.caches {
		if c == dir || strings.HasPrefix(c, dir+"/") {
			return true
		}
	}
	return false
}

// parseWorkflow reads the subset of YAML ci.yml is written in: two-space
// indentation, a job per key under jobs:, a step per "- " item under steps:,
// and run: either inline or as a "|" block.
func parseWorkflow(t *testing.T, root string) *workflow {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	w := &workflow{fetches: map[string]bool{}}
	lines := strings.Split(string(b), "\n")
	indent := func(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }
	section := ""
	var job *ciJob
	var step *ciStep
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		tl := strings.TrimSpace(l)
		if tl == "" || strings.HasPrefix(tl, "#") {
			continue
		}
		ind := indent(l)
		if ind == 0 {
			section = strings.TrimSuffix(tl, ":")
			continue
		}
		switch section {
		case "on":
			if ind == 2 {
				w.triggers = append(w.triggers, strings.TrimSuffix(strings.SplitN(tl, ":", 2)[0], ":"))
			} else if len(w.triggers) > 0 {
				// A trigger's filter (branches: [main]) qualifies it.
				last := &w.triggers[len(w.triggers)-1]
				*last += " " + tl
			}
			continue
		case "jobs":
		default:
			continue
		}
		if ind == 2 {
			job = &ciJob{name: strings.TrimSuffix(tl, ":")}
			w.jobs = append(w.jobs, job)
			step = nil
			continue
		}
		if job == nil {
			continue
		}
		if ind == 6 && strings.HasPrefix(tl, "- ") {
			step = &ciStep{}
			job.steps = append(job.steps, step)
			tl = strings.TrimPrefix(tl, "- ")
			ind = 8
		}
		if step == nil {
			continue
		}
		key, val, _ := strings.Cut(tl, ":")
		val = strings.TrimSpace(val)
		block := func() []string {
			var out []string
			for i+1 < len(lines) && (strings.TrimSpace(lines[i+1]) == "" || indent(lines[i+1]) > ind) {
				i++
				if s := strings.TrimSpace(lines[i]); s != "" {
					out = append(out, s)
				}
			}
			return out
		}
		switch key {
		case "name":
			if ind == 8 {
				step.name = val
			}
		case "uses":
			step.uses = val
		case "if":
			step.cond = val
		case "go-version":
			job.goVersion = strings.Trim(val, `'"`)
		case "run":
			if val == "|" {
				step.run = block()
			} else {
				step.run = []string{val}
			}
		case "path":
			if val == "|" {
				step.paths = block()
			} else {
				step.paths = []string{val}
			}
			w.caches = append(w.caches, step.paths...)
		}
	}
	gate := regexp.MustCompile(`^check\s+(\S+)\s+'([^']+)'\s+"?([^"\s]+)"?`)
	makeCmd := regexp.MustCompile(`(?:^|[;&|]\s*)make\s+([^;&|#]*)`)
	for _, j := range w.jobs {
		for _, s := range j.steps {
			for _, r := range s.run {
				if m := gate.FindStringSubmatch(r); m != nil {
					w.gates = append(w.gates, ciGate{m[1], m[2], m[3]})
				}
				for _, m := range makeCmd.FindAllStringSubmatch(r, -1) {
					for _, a := range strings.Fields(m[1]) {
						if !strings.Contains(a, "=") {
							w.fetches[a] = true
						}
					}
				}
			}
		}
	}
	if len(w.jobs) == 0 || len(w.triggers) == 0 {
		t.Fatalf("parsed no jobs or triggers from ci.yml: the parser no longer matches the file")
	}
	return w
}

type dataset struct {
	varName, name, dir, sub, env, fetch, target string
}

func (d dataset) dataDir() string {
	if d.sub != "" {
		return d.dir + "/" + d.sub
	}
	return d.dir
}

// parseDatasets reads the Dataset literals in internal/testfiles/datasets.go,
// the one list of data sets the tests resolve; parsing it rather than
// listing the variables again here means a new data set cannot be missed.
func parseDatasets(t *testing.T, root string) []dataset {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, "internal", "testfiles", "datasets.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []dataset
	target := regexp.MustCompile("`make ([\\w-]+)`")
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			cl, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok || fmt.Sprint(cl.Type) != "Dataset" {
				continue
			}
			ds := dataset{varName: vs.Names[0].Name}
			for _, e := range cl.Elts {
				kv := e.(*ast.KeyValueExpr)
				lit, ok := kv.Value.(*ast.BasicLit)
				if !ok {
					continue
				}
				v, _ := strconv.Unquote(lit.Value)
				switch kv.Key.(*ast.Ident).Name {
				case "Name":
					ds.name = v
				case "Dir":
					ds.dir = v
				case "Sub":
					ds.sub = v
				case "Env":
					ds.env = v
				case "Fetch":
					ds.fetch = v
				}
			}
			if m := target.FindStringSubmatch(ds.fetch); m != nil {
				ds.target = m[1]
			}
			out = append(out, ds)
		}
	}
	if len(out) < 8 {
		t.Fatalf("found %d data sets in datasets.go; the parse is wrong", len(out))
	}
	return out
}

var ciCommand = regexp.MustCompile(`\bgo (?:run|test|build|vet)\b[^|;&>)]*|\bmake [\w =.-]*\w|\./scripts/[\w.-]+|\bgofmt -l [^)\s]+`)

func renderCIDoc(w *workflow, sets []dataset) string {
	var b strings.Builder
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	p("\n\nCI runs on every %s, as %d jobs.\n", strings.Join(quoteEach(w.triggers), " and every "), len(w.jobs))
	for _, j := range w.jobs {
		p("\n**`%s`** (Go `%s`):\n\n", j.name, j.goVersion)
		n := 0
		for _, s := range j.steps {
			if s.name == "" {
				continue // an unnamed step is setup: checkout, the toolchain
			}
			n++
			p("%d. %s", n, s.name)
			if s.cond != "" {
				p(" (only if `%s`)", s.cond)
			}
			var cmds []string
			for _, r := range s.run {
				for _, c := range ciCommand.FindAllString(r, -1) {
					cmds = append(cmds, strings.TrimSpace(c))
				}
			}
			if len(cmds) > 0 {
				p(": %s", joinCode(cmds))
			}
			if len(s.paths) > 0 {
				p(": %s", joinCode(s.paths))
			}
			p("\n")
		}
	}
	p("\nThe data sets the tests read, and whether CI has them. A data set CI does\n")
	p("not fetch is one whose tests skip there: those run only on a machine that\n")
	p("has the data.\n\n")
	p("| Data set | Location | How it arrives | In CI |\n|---|---|---|---|\n")
	for _, d := range sets {
		how := "`make " + d.target + "`"
		if d.target == "" {
			// The skip message's note on where it comes from, as a sentence.
			how = strings.TrimSuffix(d.fetch, " (see docs/testing.md)")
			how = strings.ToUpper(how[:1]) + how[1:]
		}
		ci := "**no**: local only"
		if d.target != "" && w.fetches[d.target] {
			ci = "fetched"
			for _, g := range w.gates {
				if g.dir != d.dataDir() {
					continue
				}
				if strings.HasPrefix(g.min, "$") {
					ci = fmt.Sprintf("fetched; fails below the manifest's count of `%s` files", g.glob)
				} else {
					ci = fmt.Sprintf("fetched; fails below %s `%s` files", g.min, g.glob)
				}
			}
		}
		p("| %s | `%s` | %s | %s |\n", d.name, d.dataDir(), how, ci)
	}
	p("\n")
	return b.String()
}

// quoteEach puts the first word of each trigger in code: "push branches:
// [main]" becomes "`push` (branches: [main])".
func quoteEach(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		name, rest, _ := strings.Cut(x, " ")
		out[i] = "`" + name + "`"
		if rest != "" {
			out[i] += " (" + rest + ")"
		}
	}
	return out
}

func joinCode(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = "`" + x + "`"
	}
	return strings.Join(out, ", ")
}

// datasetReaders maps each data set variable to the tests that read it,
// directly or through helpers in the same package (as "pkg.TestName" outside
// the root package).
func datasetReaders(t *testing.T, root string) map[string][]string {
	t.Helper()
	type fn struct {
		pkg   string
		uses  map[string]bool // data set variables named in the body
		calls map[string]bool // functions of the same package it calls
	}
	funcs := map[string]*fn{} // "dir\x00name"
	var tests []string
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
			if !ok || fd.Recv != nil || fd.Body == nil {
				continue
			}
			x := &fn{pkg: dir, uses: map[string]bool{}, calls: map[string]bool{}}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if id, ok := n.X.(*ast.Ident); ok && id.Name == "testfiles" {
						x.uses[n.Sel.Name] = true
					}
				case *ast.CallExpr:
					if id, ok := n.Fun.(*ast.Ident); ok {
						x.calls[id.Name] = true
					}
				}
				return true
			})
			key := dir + "\x00" + fd.Name.Name
			funcs[key] = x
			for _, p := range []string{"Test", "Fuzz", "Benchmark"} {
				if strings.HasPrefix(fd.Name.Name, p) {
					tests = append(tests, key)
				}
			}
		}
	}
	out := map[string][]string{}
	for _, key := range tests {
		dir, name, _ := strings.Cut(key, "\x00")
		seen := map[string]bool{key: true}
		queue := []string{key}
		used := map[string]bool{}
		for len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			f := funcs[k]
			for u := range f.uses {
				used[u] = true
			}
			for c := range f.calls {
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
		for u := range used {
			out[u] = append(out[u], label)
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
}

func renderDatasetDoc(sets []dataset, readers map[string][]string) string {
	var b strings.Builder
	b.WriteString("\n\n| Data set | Location | Override | How it arrives | Read by |\n|---|---|---|---|---|\n")
	for _, d := range sets {
		how := "`make " + d.target + "`"
		if d.target == "" {
			how = "by hand"
		}
		env := "—"
		if d.env != "" {
			env = "`" + d.env + "`"
		}
		rs := readers[d.varName]
		read := "nothing yet"
		if len(rs) > 0 {
			read = joinCode(rs)
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s |\n", d.name, d.dataDir(), env, how, read)
	}
	b.WriteString("\n")
	return b.String()
}

// renderOptionsDoc lists the With* options of limits.go from their own
// documentation: what each caps (its first sentence) and its default (the
// "(default …)" that sentence carries). The table in the architecture guide
// used to be written by hand, and a count of the options was quoted in five
// places; each went stale when an option was added.
func renderOptionsDoc(t *testing.T, root string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(root, "limits.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	def := regexp.MustCompile(`\s*\(default ([^,)]*)[^)]*\)`)
	var b strings.Builder
	b.WriteString("\n\n| Option | Caps | Default |\n|---|---|---|\n")
	n := 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "With") || !fd.Name.IsExported() || fd.Doc == nil {
			continue
		}
		text := strings.Join(strings.Fields(fd.Doc.Text()), " ")
		first := text
		if i := strings.Index(text, ". "); i >= 0 {
			first = text[:i]
		}
		first = strings.TrimSuffix(first, ".")
		m := def.FindStringSubmatch(first)
		if m == nil {
			// The table is the one place a caller compares the options, so
			// every option says its default where the table reads it.
			t.Errorf("limits.go: %s's first sentence does not state its default as \"(default …)\"", fd.Name.Name)
			continue
		}
		dflt := strings.TrimSpace(m[1])
		first = def.ReplaceAllString(first, "")
		what := strings.TrimSpace(strings.TrimPrefix(first, fd.Name.Name))
		what = strings.TrimPrefix(what, "caps ")
		what = strings.TrimSuffix(what, " —")
		what = strings.ReplaceAll(what, "|", "\\|")
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", fd.Name.Name, what, dflt)
		n++
	}
	if n == 0 {
		t.Fatal("found no With* options in limits.go")
	}
	b.WriteString("\n")
	return b.String()
}
