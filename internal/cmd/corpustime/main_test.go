package main

// The command is built with -tags devtools and executed; this file carries no
// tag so that a plain `go test ./...` runs it.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
)

func buildTool(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "corpustime")
	out, err := exec.Command("go", "build", "-tags", "devtools", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building corpustime: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		return string(out), ee.ExitCode()
	}
	return string(out), 0
}

// TestCorpusTime: a missing file is reported, not a nil dereference; a file
// that does not parse is reported with the parser's error; either makes the
// exit status 1, and a good file still gets every stage timed (C156).
func TestCorpusTime(t *testing.T) {
	bin := buildTool(t)
	dir := t.TempDir()

	doc, err := pdf0.NewPDFADocument(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "good.pdf")
	bad := filepath.Join(dir, "bad.pdf")
	missing := filepath.Join(dir, "missing.pdf")
	if err := os.WriteFile(good, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := run(t, bin, good)
	if code != 0 {
		t.Errorf("good file: exit %d\n%s", code, out)
	}
	for _, stage := range []string{"Read", "PageCount", "Write", "ValidatePDFUA"} {
		if !strings.Contains(out, stage) {
			t.Errorf("good file: no %s stage in\n%s", stage, out)
		}
	}

	out, code = run(t, bin, missing, bad, good)
	if code != 1 {
		t.Errorf("missing and bad files: exit %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "panic") || strings.Contains(out, "nil pointer") {
		t.Errorf("crashed:\n%s", out)
	}
	if !strings.Contains(out, "read fail") || !strings.Contains(out, "Read error") {
		t.Errorf("the missing and the unparseable file are not both reported:\n%s", out)
	}
	if !strings.Contains(out, "ValidatePDFUA") {
		t.Errorf("the good file after the bad ones was not timed:\n%s", out)
	}
}
