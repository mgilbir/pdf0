// Package scripts holds tests for the repository's shell scripts; it has no
// Go code of its own.
package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// tempRepo is a throwaway git repository with one small committed file, isolated
// from the user's and the system's git configuration (hooks, signing, …).
type tempRepo struct {
	t   *testing.T
	dir string
	env []string
}

func newTempRepo(t *testing.T) *tempRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required to test the repository's git scripts: %v", err)
	}
	r := &tempRepo{t: t, dir: t.TempDir()}
	r.env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	r.git("init", "-q")
	r.write("README", []byte("hello\n"))
	r.git("add", "README")
	r.git("commit", "-q", "-m", "init")
	return r
}

func (r *tempRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = r.dir, r.env
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *tempRepo) write(name string, data []byte) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// check runs the script in the repository (from a subdirectory when sub is
// set) and returns its output and exit code.
func (r *tempRepo) check(sub string) (string, int) {
	r.t.Helper()
	script := testfiles.Committed(r.t, "scripts/check-no-binaries.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir, cmd.Env = filepath.Join(r.dir, sub), r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			r.t.Fatal(err)
		}
		return string(out), ee.ExitCode()
	}
	return string(out), 0
}

var elf = append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0}, 64)...)

// TestCheckNoBinariesReadsTheIndex: the script judges what is staged, not HEAD
// and not the working tree (audit 2026-09-22 C156).
func TestCheckNoBinariesReadsTheIndex(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		r := newTempRepo(t)
		if out, code := r.check(""); code != 0 {
			t.Errorf("clean repository: exit %d\n%s", code, out)
		}
	})

	t.Run("staged oversize file", func(t *testing.T) {
		r := newTempRepo(t)
		r.write("big file.json", bytes.Repeat([]byte("a"), 300*1024))
		r.git("add", "big file.json")
		out, code := r.check("")
		if code != 1 || !strings.Contains(out, "oversized file in the index: big file.json (307200 bytes") {
			t.Errorf("staged 300 KB file: exit %d, want 1 naming it\n%s", code, out)
		}
	})

	t.Run("staged ELF, working copy changed", func(t *testing.T) {
		r := newTempRepo(t)
		r.write("tool", elf)
		r.git("add", "tool")
		// What is staged is what gets committed; the working copy is
		// irrelevant, whether rewritten or gone.
		r.write("tool", []byte("#!/bin/sh\n"))
		out, code := r.check("")
		if code != 1 || !strings.Contains(out, "binary in the index: tool") {
			t.Errorf("staged ELF under a rewritten working copy: exit %d, want 1\n%s", code, out)
		}
		if err := os.Remove(filepath.Join(r.dir, "tool")); err != nil {
			t.Fatal(err)
		}
		out, code = r.check("")
		if code != 1 || !strings.Contains(out, "binary in the index: tool") {
			t.Errorf("staged ELF with its working copy deleted: exit %d, want 1\n%s", code, out)
		}
	})

	t.Run("run from a subdirectory", func(t *testing.T) {
		r := newTempRepo(t)
		r.write("sub/x.txt", []byte("x"))
		r.write("tool", elf)
		r.git("add", ".")
		out, code := r.check("sub")
		if code != 1 || !strings.Contains(out, "binary in the index: tool") {
			t.Errorf("run from sub/: exit %d, want the whole index checked\n%s", code, out)
		}
	})

	t.Run("oversize file removed from the index", func(t *testing.T) {
		r := newTempRepo(t)
		r.write("big.bin", bytes.Repeat([]byte("b"), 300*1024))
		r.git("add", "big.bin")
		r.git("commit", "-q", "-m", "oops")
		r.git("rm", "-q", "--cached", "big.bin")
		// Committed at HEAD, but the removal is staged: the next commit
		// does not contain it.
		if out, code := r.check(""); code != 0 {
			t.Errorf("oversize file staged for removal: exit %d, want 0\n%s", code, out)
		}
	})

	t.Run("empty index checks nothing and says so", func(t *testing.T) {
		r := newTempRepo(t)
		r.git("rm", "-q", "--cached", "README")
		if out, code := r.check(""); code != 2 || !strings.Contains(out, "nothing was checked") {
			t.Errorf("empty index: exit %d, want 2\n%s", code, out)
		}
	})

	t.Run("symlink is not read as content", func(t *testing.T) {
		r := newTempRepo(t)
		big := filepath.Join(r.dir, "outside")
		if err := os.WriteFile(big, elf, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(big, filepath.Join(r.dir, "link")); err != nil {
			t.Fatal(err)
		}
		r.git("add", "link")
		if out, code := r.check(""); code != 0 {
			t.Errorf("staged symlink: exit %d, want 0\n%s", code, out)
		}
	})
}
