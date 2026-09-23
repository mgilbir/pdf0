package lint

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestGitignoreCoversEveryCommand fails when `go build ./<dir>` run from the
// repository root would drop an executable that git does not ignore. Three
// such binaries were committed and shipped in two releases, 19.5 MB of ELF in
// a 24 MB module, and the .gitignore list that was meant to stop the next one
// still missed five of the examples (audit 2026-09-22 C166). Every main
// package of the module — the development tools behind their build tag
// included — is checked by name against the real .gitignore.
func TestGitignoreCoversEveryCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to check .gitignore: %v", err)
	}
	root := testfiles.Root(t)
	dirs, err := goList("-tags", "devtools", "-f", "{{if eq .Name \"main\"}}{{.Dir}}{{end}}", "github.com/mgilbir/pdf0/...")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 5 {
		t.Fatalf("found %d commands; go list is looking in the wrong place", len(dirs))
	}
	for _, d := range dirs {
		name := filepath.Base(d)
		cmd := exec.Command("git", "check-ignore", "-q", "--no-index", "--", name)
		cmd.Dir = root
		err := cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
			// ignored
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			rel, _ := filepath.Rel(root, d)
			t.Errorf("go build ./%s from the repository root writes ./%s, which .gitignore does not cover; add /%s to it",
				filepath.ToSlash(rel), name, strings.TrimPrefix(name, "/"))
		default:
			t.Fatalf("git check-ignore %s: %v", name, err)
		}
	}
}
