package lint

import (
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestLimitsDocNamesEveryGuard fails when docs/limits.md does not name, in
// backticks, every guard identifier a "limit" finding can carry: the string
// value of each core.Guard* constant. The guard inventory there is how a
// caller who sees "(graph-nesting)" in a message finds out what it means, and
// it had fallen behind the code: no-source-file and graph-nesting were not in
// it, and it said the names were constants in limits_report.go, which held
// four of them (docs audit 2026-10-11 D54).
func TestLimitsDocNamesEveryGuard(t *testing.T) {
	guards := coreGuards(t)
	b, err := os.ReadFile(filepath.Join(testfiles.Root(t), "docs", "limits.md"))
	if err != nil {
		t.Fatal(err)
	}
	if missing := guardsMissingFrom(string(b), guards); len(missing) > 0 {
		t.Errorf("docs/limits.md does not name these guards; add each to its inventory:\n\t%s", strings.Join(missing, "\n\t"))
	}
}

// TestLimitsDocNamesEveryGuardDetects is the check's planted bug: a document
// that names a guard only outside backticks, or as part of a longer name, is
// missing it.
func TestLimitsDocNamesEveryGuardDetects(t *testing.T) {
	guards := map[string]string{"GuardWork": "work", "GuardWalkDepth": "walk-depth"}
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{"`work` and `walk-depth`", nil},
		{"`work` and walk-depth", []string{"GuardWalkDepth (walk-depth)"}},
		{"`walk-depth-total` and `work`", []string{"GuardWalkDepth (walk-depth)"}},
		{"", []string{"GuardWalkDepth (walk-depth)", "GuardWork (work)"}},
	} {
		got := guardsMissingFrom(tc.doc, guards)
		if strings.Join(got, ";") != strings.Join(tc.want, ";") {
			t.Errorf("guardsMissingFrom(%q) = %q, want %q", tc.doc, got, tc.want)
		}
	}
	if len(coreGuards(t)) < 20 {
		t.Fatal("found too few core.Guard* constants; the scan is looking in the wrong place")
	}
}

// coreGuards maps the name of each exported string constant of internal/core
// whose name begins "Guard" to its value.
func coreGuards(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range load(t).pkgs {
		if p.path != modulePath+"/internal/core" {
			continue
		}
		scope := p.types.Scope()
		for _, name := range scope.Names() {
			c, ok := scope.Lookup(name).(*types.Const)
			if !ok || !strings.HasPrefix(name, "Guard") || c.Val().Kind() != constant.String {
				continue
			}
			out[name] = constant.StringVal(c.Val())
		}
	}
	if len(out) == 0 {
		t.Fatal("found no core.Guard* constants")
	}
	return out
}

// guardsMissingFrom lists, sorted, each guard whose value doc does not name
// as a code span of its own.
func guardsMissingFrom(doc string, guards map[string]string) []string {
	var missing []string
	for name, val := range guards {
		if !strings.Contains(doc, "`"+val+"`") {
			missing = append(missing, name+" ("+val+")")
		}
	}
	sort.Strings(missing)
	return missing
}
