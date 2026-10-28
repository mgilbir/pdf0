package lint

import (
	"errors"
	"os"
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

// TestGitignoreCoversASymlinkedDataSet: every data set internal/testfiles knows
// is ignored when it is not a directory, which is what a data set symlinked to
// another checkout's copy is to git. A pattern with a trailing slash ("dir/")
// matches only a directory, so a symlink it names shows as untracked, and a
// `git add -A` in that checkout commits it.
//
// git decides whether a path is a directory by looking at it, and in a checkout
// with the data the path is one, so the question is put to a scratch repository
// holding only .gitignore, where no data set path exists and each is judged as
// a file. A data set whose directory also holds committed files (a manifest, a
// downloader) is skipped: it is ignored by file patterns, and its directory
// cannot be a symlink without replacing what is committed.
func TestGitignoreCoversASymlinkedDataSet(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to check .gitignore: %v", err)
	}
	root := testfiles.Root(t)
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", scratch).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(scratch, ".gitignore"), ignore, 0o644); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, d := range parseDatasets(t, root) {
		tracked := exec.Command("git", "ls-files", "--", d.dir)
		tracked.Dir = root
		out, err := tracked.Output()
		if err != nil {
			t.Fatalf("git ls-files %s: %v", d.dir, err)
		}
		if len(strings.TrimSpace(string(out))) > 0 {
			continue
		}
		checked++
		cmd := exec.Command("git", "check-ignore", "-q", "--no-index", "--", d.dir)
		cmd.Dir = scratch
		err = cmd.Run()
		var exit *exec.ExitError
		switch {
		case err == nil:
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			t.Errorf("%s (%s) is not ignored unless it is a directory: name it in .gitignore without a trailing slash",
				d.dir, d.name)
		default:
			t.Fatalf("git check-ignore %s: %v", d.dir, err)
		}
	}
	if checked < 5 {
		t.Fatalf("checked %d data sets; parseDatasets is reading the wrong file", checked)
	}
}
