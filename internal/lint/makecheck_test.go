package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestMakeTargetsAreWhatCIRuns fails when the make targets a contributor is
// told to run before pushing stop running what CI runs. The pre-push lists in
// the README and CONTRIBUTING.md named four of CI's ten build steps, and the
// advice "touched a validator, run make test-corpus" ran -run TestCorpus,
// which skips TestRuleCoverage and every other corpus test whose name does
// not start that way — green locally, red in CI (docs audit 2026-10-11 D13,
// D31). So:
//
//   - `make check` runs the commands of CI's build-test job, in its order;
//   - `make test-corpus` runs the command of CI's corpus job's test step, with
//     the corpus named as CI names it.
func TestMakeTargetsAreWhatCIRuns(t *testing.T) {
	root := testfiles.Root(t)
	wf := parseWorkflow(t, root)
	b, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makefile := string(b)

	job := func(name string) *ciJob {
		for _, j := range wf.jobs {
			if j.name == name {
				return j
			}
		}
		t.Fatalf("ci.yml has no %s job", name)
		return nil
	}
	stepCommands := func(steps []*ciStep) []string {
		var out []string
		for _, s := range steps {
			for _, r := range s.run {
				out = append(out, commandsIn(r)...)
			}
		}
		return out
	}

	ci := stepCommands(job("build-test").steps)
	check := recipeCommands(t, makefile, "check")
	if len(ci) < 5 {
		t.Fatalf("found %d commands in CI's build-test job; the parse is wrong", len(ci))
	}
	if !slices.Equal(check, ci) {
		t.Errorf("make check runs\n\t%s\nCI's build-test job runs\n\t%s\nmake them the same, in the same order", strings.Join(check, "\n\t"), strings.Join(ci, "\n\t"))
	}

	var ratchet []string
	for _, s := range job("corpus").steps {
		for _, r := range s.run {
			for _, c := range commandsIn(r) {
				if strings.HasPrefix(c, "go test") {
					ratchet = append(ratchet, c)
				}
			}
		}
	}
	if len(ratchet) == 0 {
		t.Fatal("found no go test in CI's corpus job; the parse is wrong")
	}
	corpus := recipeCommands(t, makefile, "test-corpus")
	for _, c := range ratchet {
		if !slices.Contains(corpus, c) {
			t.Errorf("make test-corpus does not run CI's corpus test step, `%s`; it runs %q", c, corpus)
		}
	}
	if !strings.Contains(recipe(t, makefile, "test-corpus"), "VERAPDF_CORPUS=$(CORPUS_DIR) go test ./...") {
		t.Error("make test-corpus does not run go test with VERAPDF_CORPUS set, as CI's corpus job does")
	}
}

// commandsIn picks the commands out of one line of shell, as the generated
// CI section does.
func commandsIn(line string) []string {
	var out []string
	for _, c := range ciCommand.FindAllString(line, -1) {
		out = append(out, strings.TrimSpace(c))
	}
	return out
}

// recipe returns the recipe of a Makefile target: its tab-indented lines,
// continuations joined, with make's own spellings ($(MAKE), $$) written as
// the shell sees them.
func recipe(t *testing.T, makefile, target string) string {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	head := regexp.MustCompile(`^` + regexp.QuoteMeta(target) + `\s*:([^=]|$)`)
	for i, l := range lines {
		if !head.MatchString(l) {
			continue
		}
		var body []string
		for _, r := range lines[i+1:] {
			if !strings.HasPrefix(r, "\t") {
				break
			}
			body = append(body, strings.TrimPrefix(r, "\t"))
		}
		s := strings.Join(body, "\n")
		s = strings.ReplaceAll(s, "\\\n", " ")
		s = strings.ReplaceAll(s, "$(MAKE)", "make")
		s = strings.ReplaceAll(s, "$$", "$")
		return s
	}
	t.Fatalf("the Makefile has no %s target", target)
	return ""
}

func recipeCommands(t *testing.T, makefile, target string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(recipe(t, makefile, target), "\n") {
		out = append(out, commandsIn(l)...)
	}
	return out
}
