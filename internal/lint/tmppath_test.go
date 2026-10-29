package lint

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// TestNoCodeAssumesAPathUnderTmp: nothing committed that runs (Go, Python,
// shell, the Makefile, the workflows) names a path under /tmp. Such a path is
// one machine's leftover, not the repository's: a script that defaulted its
// input to /tmp/pdf_spec.txt read whatever was left there, or failed on a file
// nobody made. A test that wants a scratch directory takes t.TempDir; a
// program that wants a file takes it as an argument. Comments are code too
// here: one that names a /tmp path is how such a default gets copied back.
func TestNoCodeAssumesAPathUnderTmp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	root := testfiles.Root(t)
	cmd := exec.Command("git", "grep", "-n", "-I", "-e", "/tmp/", "--",
		"*.go", "*.py", "*.sh", "Makefile", ".github/*", ":!internal/lint/tmppath_test.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return // git grep found nothing
	}
	if err != nil {
		t.Fatalf("git grep: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		t.Errorf("a path under /tmp: %s", line)
	}
}
