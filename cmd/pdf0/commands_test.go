package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0"
)

// writeTestPDF writes a minimal PDF/A document at the given level to dir and
// returns its path.
func writeTestPDF(t *testing.T, dir string, level pdf0.PDFALevel, mutate func(*pdf0.Document)) string {
	t.Helper()
	doc := pdf0.NewPDFADocument(level)
	if mutate != nil {
		mutate(doc)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "in.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return out.String(), runErr
}

// TestExitCodes pins the C47 exit-code contract: violations (1), usage (2),
// and operational errors (3) are distinguishable.
func TestExitCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0},
		{"violations", violationsf("3 violation(s)"), 1},
		{"usage", usagef("usage: ..."), 2},
		{"operational", os.ErrNotExist, 3},
	}
	for _, c := range cases {
		if got := exitCode(c.err); got != c.want {
			t.Errorf("%s: exitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestCommandUsageErrors: every subcommand reports a wrong argument count as a
// usage error (exit 2), not an operational one.
func TestCommandUsageErrors(t *testing.T) {
	cases := map[string]func([]string) error{
		"info":     cmdInfo,
		"validate": cmdValidate,
		"decrypt":  cmdDecrypt,
		"encrypt":  cmdEncrypt,
		"extract":  cmdExtract,
		"repair":   cmdRepair,
		"merge":    cmdMerge,
		"ua":       cmdUA,
	}
	for name, cmd := range cases {
		err := cmd(nil)
		if _, ok := err.(usageError); !ok {
			t.Errorf("%s with no args: got %T (%v), want usageError", name, err, err)
		}
	}
}

// TestCmdInfoPageCount: info counts pages via the page tree, so an orphan
// /Type /Page object outside the tree does not inflate the count (C47).
func TestCmdInfoPageCount(t *testing.T) {
	dir := t.TempDir()
	path := writeTestPDF(t, dir, pdf0.PDFA2b, func(doc *pdf0.Document) {
		orphan := &pdf0.Dictionary{}
		orphan.Set("Type", pdf0.Name("Page"))
		doc.Objects[50] = &pdf0.IndirectObject{Number: 50, Value: orphan}
	})
	out, err := captureStdout(t, func() error { return cmdInfo([]string{path}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pages:     0") {
		t.Errorf("orphan page object inflated the page count:\n%s", out)
	}
}

// TestCmdValidate: a conforming file exits 0; the same file at the wrong level
// reports violations with exit code 1.
func TestCmdValidate(t *testing.T) {
	dir := t.TempDir()
	path := writeTestPDF(t, dir, pdf0.PDFA2b, nil)
	if _, err := captureStdout(t, func() error { return cmdValidate([]string{path}) }); err != nil {
		t.Errorf("conforming 2b file: %v", err)
	}
	_, err := captureStdout(t, func() error { return cmdValidate([]string{"-level", "1b", path}) })
	if exitCode(err) != 1 {
		t.Errorf("2b file validated at 1b: got %T (%v), want violations (exit 1)", err, err)
	}
}

// TestCmdRepair: repair prints what it fixed and a summary of what remains,
// and reports remaining violations through the exit code instead of silently
// exiting 0 (C47).
func TestCmdRepair(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.pdf")

	// A repairable defect (catalog /AA) and an unfixable target (a 2b file
	// repaired toward 1b keeps its pdfaid:part 2 metadata).
	path := writeTestPDF(t, dir, pdf0.PDFA2b, func(doc *pdf0.Document) {
		cat := doc.ResolveDict(doc.Trailer.Get("Root"))
		cat.Set("AA", &pdf0.Dictionary{})
	})
	got, err := captureStdout(t, func() error { return cmdRepair([]string{"-level", "1b", path, out}) })
	if !strings.Contains(got, "fixed: removed catalog additional-actions (/AA)") {
		t.Errorf("missing fix line:\n%s", got)
	}
	if !strings.Contains(got, "violation(s) remain") {
		t.Errorf("missing summary line:\n%s", got)
	}
	if exitCode(err) != 1 {
		t.Errorf("remaining violations: got %T (%v), want violations (exit 1)", err, err)
	}
	if fi, statErr := os.Stat(out); statErr != nil || fi.Size() == 0 {
		t.Errorf("repaired output not written: %v", statErr)
	}

	// Repairing a conforming file at its own level: nothing fixed, nothing
	// remains, exit 0 — but still a summary.
	clean := writeTestPDF(t, t.TempDir(), pdf0.PDFA2b, nil)
	got, err = captureStdout(t, func() error { return cmdRepair([]string{"-level", "2b", clean, out}) })
	if err != nil {
		t.Errorf("clean repair: %v", err)
	}
	if !strings.Contains(got, "0 fix(es) applied, 0 violation(s) remain") {
		t.Errorf("missing clean summary:\n%s", got)
	}
}

// TestCmdEncryptDecryptRoundTrip drives encrypt → decrypt → info through the
// command layer.
func TestCmdEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := writeTestPDF(t, dir, pdf0.PDFA2b, nil)
	enc := filepath.Join(dir, "enc.pdf")
	dec := filepath.Join(dir, "dec.pdf")

	if _, err := captureStdout(t, func() error { return cmdEncrypt([]string{"-user", "pw", in, enc}) }); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Wrong-password decrypt is an operational error, not usage.
	_, err := captureStdout(t, func() error { return cmdDecrypt([]string{"-password", "nope", enc, dec}) })
	if exitCode(err) != 3 {
		t.Errorf("wrong password: got %T (%v), want operational (exit 3)", err, err)
	}
	if _, err := captureStdout(t, func() error { return cmdDecrypt([]string{"-password", "pw", enc, dec}) }); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	out, err := captureStdout(t, func() error { return cmdInfo([]string{dec}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "encrypted: false") {
		t.Errorf("decrypted file still reports encrypted:\n%s", out)
	}
}
