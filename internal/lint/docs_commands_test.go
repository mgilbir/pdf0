package lint

import (
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestDocCommandsRun checks the commands the repository tells people to run:
// in the Markdown docs (fenced shell blocks and inline code), in doc
// comments, in the CI workflow, and in the comments of .gitignore and the
// Makefile. Every `make` target must exist; every `go run`, `go build`,
// `go test` or `go vet` of a directory must name one that exists and must
// carry `-tags devtools` when everything in it is behind that tag; every
// `-fuzz` pattern must name a fuzz target in the package being tested.
//
// Each was once wrong without anyone noticing (audit 2026-09-22 C164, C165):
// the testing guide ran `go run ./internal/cmd/rulecoverage`, which the tag
// excludes entirely; CI's comment pointed at a `make fuzz` that did not exist,
// and so did .gitignore at three EN 16931 targets that had moved to formalis;
// and `go test -fuzz=FuzzCmapSubtable .` prints "no fuzz tests to fuzz" and
// exits 0, because that target moved to forme.
func TestDocCommandsRun(t *testing.T) {
	root := testfiles.Root(t)
	targets := makeTargets(t, root)
	elsewhere := resolveOtherRepoTargets(t)
	var cmds []docCommand
	for _, rel := range markdownFiles(t) {
		if isHistorical(rel) {
			continue
		}
		md := readMarkdown(t, rel)
		for _, f := range md.fences {
			switch f.lang {
			case "", "sh", "bash", "shell", "console":
				for i, l := range strings.Split(f.body, "\n") {
					cmds = append(cmds, docCommand{fmt.Sprintf("%s:%d", rel, f.line+i), l})
				}
			}
		}
		for _, s := range md.spans {
			cmds = append(cmds, docCommand{fmt.Sprintf("%s:%d", s.file, s.line), s.text})
		}
	}
	for _, b := range docCommentBlocks(t) {
		if isCommandBlock(b.text) {
			for _, l := range strings.Split(b.text, "\n") {
				cmds = append(cmds, docCommand{fmt.Sprintf("%s:%d (doc comment)", b.file, b.line), l})
			}
		}
	}
	for _, rel := range []string{".github/workflows/ci.yml", ".gitignore", "Makefile"} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			where := fmt.Sprintf("%s:%d", rel, i+1)
			for _, m := range codeSpan.FindAllStringSubmatch(l, -1) {
				cmds = append(cmds, docCommand{where, m[1]})
			}
			if rel == ".github/workflows/ci.yml" {
				// A run: line, or a line of a run: | block.
				l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
				l = strings.TrimSpace(strings.TrimPrefix(l, "run:"))
				cmds = append(cmds, docCommand{where, l})
			}
		}
	}

	checked := 0
	for _, c := range cmds {
		for _, part := range splitShell(c.text) {
			msg, applies := checkCommand(root, targets, elsewhere, part)
			if !applies {
				continue
			}
			checked++
			if msg != "" {
				t.Errorf("%s: `%s`: %s", c.where, part, msg)
			}
		}
	}
	if checked < 40 {
		t.Fatalf("checked only %d commands; the scan is looking in the wrong place", checked)
	}
	t.Logf("checked %d commands", checked)
}

type docCommand struct{ where, text string }

// otherRepoTargets are make targets that moved to another module's Makefile
// along with the code they fetch data for, and that this repository's
// comments name as living there now. Each maps to that module, and the test
// checks its Makefile (at the version go.mod requires) still has the target.
var otherRepoTargets = map[string]string{
	"test-bidi":         "github.com/mgilbir/forme",
	"test-grapheme":     "github.com/mgilbir/forme",
	"en16931-artefacts": "github.com/mgilbir/formalis",
	"en16931-codelists": "github.com/mgilbir/formalis",
	"cius-oracles":      "github.com/mgilbir/formalis",
}

// resolveOtherRepoTargets checks each of otherRepoTargets against its
// module's Makefile and returns the ones that resolve.
func resolveOtherRepoTargets(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for target, mod := range otherRepoTargets {
		dirs, err := goList("-m", "-f", "{{.Dir}}", mod)
		if err != nil || len(dirs) != 1 {
			t.Fatalf("locating %s: %v", mod, err)
		}
		if makeTargets(t, dirs[0])[target] {
			out[target] = true
		} else {
			t.Errorf("otherRepoTargets says %s's Makefile has %q, and it does not", mod, target)
		}
	}
	return out
}

// splitShell splits a line into its commands at ;, &&, || and |, and drops a
// prompt and a trailing comment.
func splitShell(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "$ ")
	if i := strings.Index(line, " #"); i >= 0 {
		line = line[:i]
	}
	var out []string
	for _, p := range regexp.MustCompile(`&&|\|\||;|\|`).Split(line, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// checkCommand checks one command. applies is false for anything that is not
// a make or go invocation.
func checkCommand(root string, targets, elsewhere map[string]bool, cmd string) (msg string, applies bool) {
	f := strings.Fields(cmd)
	// Leading environment assignments: VAR=x make y.
	for len(f) > 0 && strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-") {
		f = f[1:]
	}
	if len(f) == 0 {
		return "", false
	}
	switch f[0] {
	case "make":
		var bad []string
		for _, a := range f[1:] {
			if strings.HasPrefix(a, "-") || strings.Contains(a, "=") || strings.ContainsAny(a, "<>…$") {
				continue
			}
			if !targets[a] && !elsewhere[a] {
				bad = append(bad, a)
			}
		}
		if len(bad) > 0 {
			return fmt.Sprintf("the Makefile has no target %s", strings.Join(bad, ", ")), true
		}
		return "", true
	case "go":
		if len(f) < 2 {
			return "", false
		}
		switch f[1] {
		case "run", "build", "test", "vet", "install":
		default:
			return "", false
		}
		return checkGoCommand(root, f[1], f[2:]), true
	}
	return "", false
}

// goValueFlags are the go command flags that take a separate value.
var goValueFlags = map[string]bool{
	"-tags": true, "-o": true, "-run": true, "-fuzz": true, "-fuzztime": true, "-count": true,
	"-timeout": true, "-bench": true, "-skip": true, "-cpu": true, "-p": true, "-coverprofile": true,
	"-cpuprofile": true, "-memprofile": true, "-benchtime": true, "-parallel": true, "-fuzzminimizetime": true,
	"-exec": true, "-mod": true, "-C": true,
}

func checkGoCommand(root, verb string, args []string) string {
	var tags []string
	var fuzz string
	var pkgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			name, val, hasVal := strings.Cut(a, "=")
			name = "-" + strings.TrimLeft(name, "-")
			if !hasVal && goValueFlags[name] && i+1 < len(args) {
				i++
				val = args[i]
			}
			switch name {
			case "-tags":
				tags = append(tags, strings.Split(strings.Trim(val, `'"`), ",")...)
			case "-fuzz":
				fuzz = strings.Trim(val, `'"`)
			}
			continue
		}
		if strings.HasPrefix(a, ">") || strings.HasPrefix(a, "2>") {
			break // a redirection ends the go command's arguments
		}
		pkgs = append(pkgs, a)
		if verb == "run" {
			break // what follows is the program's own arguments
		}
	}
	var problems []string
	for _, p := range pkgs {
		if !strings.HasPrefix(p, ".") {
			continue // an import path or a file list; not a directory here
		}
		if strings.HasSuffix(p, ".go") {
			continue
		}
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(strings.TrimSuffix(p, "..."), "/")))
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			problems = append(problems, fmt.Sprintf("%s is not a directory of this repository", p))
			continue
		}
		if strings.HasSuffix(p, "...") {
			continue
		}
		if tag := requiredTag(dir); tag != "" && !contains(tags, tag) {
			problems = append(problems, fmt.Sprintf("every file in %s is behind the %q build tag, so this fails with \"build constraints exclude all Go files\"; add -tags %s", p, tag, tag))
		}
		if fuzz != "" && verb == "test" {
			if msg := checkFuzzTarget(dir, p, fuzz); msg != "" {
				problems = append(problems, msg)
			}
		}
	}
	if fuzz != "" && verb == "test" && len(pkgs) == 0 {
		if msg := checkFuzzTarget(root, ".", fuzz); msg != "" {
			problems = append(problems, msg)
		}
	}
	return strings.Join(problems, "; ")
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// requiredTag returns the build tag every non-test Go file in dir requires,
// or "" if some file builds without one.
func requiredTag(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	tag := ""
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return ""
		}
		var expr constraint.Expr
		for _, l := range strings.Split(string(b), "\n") {
			if constraint.IsGoBuild(l) {
				expr, _ = constraint.Parse(l)
				break
			}
			if strings.HasPrefix(l, "package ") {
				break
			}
		}
		if expr == nil || expr.Eval(func(string) bool { return false }) {
			return "" // this file builds with no tags
		}
		if tg, ok := expr.(*constraint.TagExpr); ok {
			tag = tg.Tag
		}
	}
	return tag
}

// checkFuzzTarget checks that a -fuzz pattern names a fuzz target in dir.
func checkFuzzTarget(dir, pkg, pattern string) string {
	name := strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$")
	name = strings.TrimSuffix(name, `\`)
	if !regexp.MustCompile(`^Fuzz\w+$`).MatchString(name) {
		return "" // a placeholder or a real regexp; nothing to resolve
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && strings.Contains(string(b), "func "+name+"(") {
			return ""
		}
	}
	return fmt.Sprintf("there is no fuzz target %s in %s, so go test prints \"no fuzz tests to fuzz\" and exits 0", name, pkg)
}

// makeTargets returns the Makefile's explicit targets.
func makeTargets(t *testing.T, root string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	rule := regexp.MustCompile(`^([A-Za-z0-9_./-][A-Za-z0-9_./ -]*):([^=]|$)`)
	for _, l := range strings.Split(string(b), "\n") {
		if m := rule.FindStringSubmatch(l); m != nil {
			for _, n := range strings.Fields(m[1]) {
				out[n] = true
			}
		}
	}
	if len(out) < 10 {
		t.Fatalf("found %d Makefile targets; the parse is wrong", len(out))
	}
	return out
}
