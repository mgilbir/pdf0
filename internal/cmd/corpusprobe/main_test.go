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
	bin := filepath.Join(t.TempDir(), "corpusprobe")
	out, err := exec.Command("go", "build", "-tags", "devtools", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("building corpusprobe: %v\n%s", err, out)
	}
	return bin
}

// run executes the probe with its failure log directed into tmp.
func run(t *testing.T, bin, tmp string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
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

// TestCorpusProbeArgs: a worker count that is not a positive integer in range,
// and a directory that is not one, are usage errors (exit 2) — "-1" used to
// panic and "0" to probe nothing and exit 0 (C156).
func TestCorpusProbeArgs(t *testing.T) {
	bin := buildTool(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "f.pdf")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := [][]string{
		{},
		{dir, "-1"},
		{dir, "0"},
		{dir, "abc"},
		{dir, "4x"},
		{dir, "100000"},
		{dir, "2", "extra"},
		{filepath.Join(dir, "nosuch")},
		{file},
	}
	for _, args := range cases {
		out, code := run(t, bin, t.TempDir(), args...)
		if code != 2 || !strings.Contains(out, "usage:") {
			t.Errorf("corpusprobe %q: exit %d, want 2 with usage\n%s", args, code, out)
		}
		if strings.Contains(out, "panic") || strings.Contains(out, "probing") {
			t.Errorf("corpusprobe %q started or crashed:\n%s", args, out)
		}
	}
}

// TestCorpusProbeRun: a directory with nothing to probe is a failure, not an
// empty success; a real run probes every file, logs every non-ok outcome, and
// exits 0 when nothing panicked or hung.
func TestCorpusProbeRun(t *testing.T) {
	bin := buildTool(t)

	out, code := run(t, bin, t.TempDir(), t.TempDir())
	if code != 1 || !strings.Contains(out, "no .pdf files") {
		t.Errorf("empty directory: exit %d, want 1\n%s", code, out)
	}

	dir := t.TempDir()
	doc, err := pdf0.NewPDFADocument(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.pdf"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.pdf"), []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	out, code = run(t, bin, tmp, dir, "2")
	if code != 0 {
		t.Errorf("probe run: exit %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"probing 2 PDFs with 2 workers", "  ok        1", "  error     1", "=== PANICS (0)"} {
		if !strings.Contains(out, want) {
			t.Errorf("probe report lacks %q:\n%s", want, out)
		}
	}
	log, err := os.ReadFile(filepath.Join(tmp, "corpusprobe-failures.tsv"))
	if err != nil {
		t.Fatalf("failure log: %v", err)
	}
	if !strings.Contains(string(log), "bad.pdf\terror\t") || strings.Contains(string(log), "good.pdf") {
		t.Errorf("failure log should list bad.pdf only:\n%s", log)
	}
}
