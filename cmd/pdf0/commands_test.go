package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/pdfa"
)

// writeTestPDF writes a minimal PDF/A document at the given level to dir and
// returns its path.
func writeTestPDF(t *testing.T, dir string, level pdfa.Level, mutate func(*pdf0.Document)) string {
	t.Helper()
	doc, err := pdf0.NewPDFADocument(level)
	if err != nil {
		t.Fatalf("building a %s skeleton: %v", level, err)
	}
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

// captureStdout runs fn with the command's stdout redirected and returns what
// it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var out bytes.Buffer
	old := stdout.w
	stdout.w = &out
	defer func() { stdout.w = old; stdout.err = nil }()
	runErr := fn()
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
	path := writeTestPDF(t, dir, pdfa.PDFA2b, func(doc *pdf0.Document) {
		orphan := &object.Dictionary{}
		orphan.Set("Type", object.Name("Page"))
		doc.Objects[50] = &object.IndirectObject{Number: 50, Value: orphan}
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
	path := writeTestPDF(t, dir, pdfa.PDFA2b, nil)
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
	path := writeTestPDF(t, dir, pdfa.PDFA2b, func(doc *pdf0.Document) {
		cat := doc.ResolveDict(doc.Trailer.Get("Root"))
		cat.Set("AA", &object.Dictionary{})
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
	clean := writeTestPDF(t, t.TempDir(), pdfa.PDFA2b, nil)
	got, err = captureStdout(t, func() error { return cmdRepair([]string{"-force", "-level", "2b", clean, out}) })
	if err != nil {
		t.Errorf("clean repair: %v", err)
	}
	if !strings.Contains(got, "0 fix(es) applied, 0 violation(s) remain") {
		t.Errorf("missing clean summary:\n%s", got)
	}
}

// TestCmdEncryptDecryptRoundTrip drives encrypt → decrypt → info through the
// command layer, with the passwords in the environment.
func TestCmdEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := writeTestPDF(t, dir, pdfa.PDFA2b, nil)
	enc := filepath.Join(dir, "enc.pdf")
	dec := filepath.Join(dir, "dec.pdf")
	useTerminal(t, &fakeTerminal{tty: false})

	t.Setenv("PDF0_USER_PASSWORD", "pw")
	if _, err := captureStdout(t, func() error { return cmdEncrypt([]string{in, enc}) }); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Wrong-password decrypt is an operational error, not usage.
	t.Setenv("PDF0_PASSWORD", "nope")
	_, err := captureStdout(t, func() error { return cmdDecrypt([]string{enc, dec}) })
	if exitCode(err) != 3 {
		t.Errorf("wrong password: got %T (%v), want operational (exit 3)", err, err)
	}
	t.Setenv("PDF0_PASSWORD", "pw")
	if _, err := captureStdout(t, func() error { return cmdDecrypt([]string{enc, dec}) }); err != nil {
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

// fakeTerminal is a scripted terminal: IsTerminal reports tty, and each
// ReadPassword returns the next answer.
type fakeTerminal struct {
	tty     bool
	answers []string
	prompts []string
}

func (f *fakeTerminal) IsTerminal() bool { return f.tty }

func (f *fakeTerminal) ReadPassword(prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)
	if len(f.answers) == 0 {
		return "", errors.New("fake terminal: no more answers")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func useTerminal(t *testing.T, f *fakeTerminal) {
	t.Helper()
	old := promptTerminal
	promptTerminal = f
	t.Cleanup(func() { promptTerminal = old })
}

func encryptedTestPDF(t *testing.T, dir, user string) string {
	t.Helper()
	doc, err := pdf0.NewPDFADocument(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.SetEncryption(user, "owner-"+user); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "enc.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestPromptOnTerminal: with no password supplied and a terminal on stdin, a
// command that needs the content prompts, and uses the answer (C113).
func TestPromptOnTerminal(t *testing.T) {
	dir := t.TempDir()
	enc := encryptedTestPDF(t, dir, "userpw")
	term := &fakeTerminal{tty: true, answers: []string{"userpw"}}
	useTerminal(t, term)
	dec := filepath.Join(dir, "dec.pdf")
	if _, err := captureStdout(t, func() error { return cmdDecrypt([]string{enc, dec}) }); err != nil {
		t.Fatalf("decrypt with a prompted password: %v", err)
	}
	if len(term.prompts) != 1 || !strings.Contains(term.prompts[0], "Password for") {
		t.Errorf("prompts = %q, want one password prompt", term.prompts)
	}

	// A wrong answer is a wrong password, not a missing one.
	useTerminal(t, &fakeTerminal{tty: true, answers: []string{"nope"}})
	_, err := captureStdout(t, func() error { return cmdExtract([]string{enc}) })
	if err == nil || !strings.Contains(err.Error(), "password is wrong") {
		t.Errorf("wrong prompted password: got %v", err)
	}

	// A supplied password is never second-guessed by a prompt.
	term = &fakeTerminal{tty: true}
	useTerminal(t, term)
	t.Setenv("PDF0_PASSWORD", "nope")
	if _, err := captureStdout(t, func() error { return cmdExtract([]string{enc}) }); err == nil {
		t.Error("wrong PDF0_PASSWORD accepted")
	}
	if len(term.prompts) != 0 {
		t.Errorf("prompted although a password was supplied: %q", term.prompts)
	}
}

// TestNoPromptWithoutTerminal: with stdin not a terminal nothing prompts; the
// command fails saying no password was supplied.
func TestNoPromptWithoutTerminal(t *testing.T) {
	dir := t.TempDir()
	enc := encryptedTestPDF(t, dir, "userpw")
	term := &fakeTerminal{tty: false, answers: []string{"userpw"}}
	useTerminal(t, term)
	_, err := captureStdout(t, func() error { return cmdValidate([]string{enc}) })
	if err == nil || !strings.Contains(err.Error(), "no password was supplied") {
		t.Errorf("got %v, want the missing-password error", err)
	}
	if len(term.prompts) != 0 {
		t.Errorf("prompted without a terminal: %q", term.prompts)
	}
}

// TestEncryptPromptConfirms: encrypt prompts twice for the user password and
// refuses, writing nothing, when the answers differ.
func TestEncryptPromptConfirms(t *testing.T) {
	dir := t.TempDir()
	in := writeTestPDF(t, dir, pdfa.PDFA2b, nil)
	out := filepath.Join(dir, "enc.pdf")
	useTerminal(t, &fakeTerminal{tty: true, answers: []string{"one", "two"}})
	_, err := captureStdout(t, func() error { return cmdEncrypt([]string{in, out}) })
	if exitCode(err) != 2 || !strings.Contains(err.Error(), "did not match") {
		t.Errorf("mismatched confirmation: got %v", err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("output written despite the mismatch")
	}
	useTerminal(t, &fakeTerminal{tty: true, answers: []string{"same", "same"}})
	if _, err := captureStdout(t, func() error { return cmdEncrypt([]string{in, out}) }); err != nil {
		t.Fatalf("encrypt with a confirmed prompt: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if doc, err := parseDoc(data, "same"); err != nil || doc.Locked() {
		t.Errorf("the prompted password does not open the output: %v", err)
	}
}

// TestLockedErrorUsesLockReason: the message follows Document.LockReason. A
// wrong password is "no password was supplied" or "the password is wrong"
// depending on whether one was; an unsupported or malformed /Encrypt is
// reported as itself either way, since no password would help.
func TestLockedErrorUsesLockReason(t *testing.T) {
	wrong := fmt.Errorf("%w: detail", pdf0.ErrWrongPassword)
	unsupported := fmt.Errorf("%w: /Filter /Adobe.PubSec", pdf0.ErrEncryptionUnsupported)
	malformed := fmt.Errorf("%w: /Length 7", pdf0.ErrEncryptionMalformed)
	cases := []struct {
		name     string
		reason   error
		supplied bool
		want     string
		wraps    error
	}{
		{"wrong, none supplied", wrong, false, "no password was supplied", nil},
		{"wrong, supplied", wrong, true, "the password is wrong", pdf0.ErrWrongPassword},
		{"unsupported, none supplied", unsupported, false, "/Adobe.PubSec", pdf0.ErrEncryptionUnsupported},
		{"unsupported, supplied", unsupported, true, "/Adobe.PubSec", pdf0.ErrEncryptionUnsupported},
		{"malformed, supplied", malformed, true, "/Length 7", pdf0.ErrEncryptionMalformed},
	}
	for _, c := range cases {
		got := lockedError("f.pdf", c.reason, c.supplied, nil)
		if !strings.Contains(got.Error(), c.want) {
			t.Errorf("%s: got %q, want it to say %q", c.name, got, c.want)
		}
		if c.wraps != nil && !errors.Is(got, c.wraps) {
			t.Errorf("%s: got %v, want it to wrap %v", c.name, got, c.wraps)
		}
		if c.reason != wrong && (strings.Contains(got.Error(), "no password was supplied") || strings.Contains(got.Error(), "password is wrong")) {
			t.Errorf("%s: blames the password for %v: %q", c.name, c.reason, got)
		}
	}
}

// TestRepairReadBackFailureIsAnError: when repair cannot re-read its own
// output, that is an operational error and nothing is written — never
// "0 violation(s) remain" (C155).
func TestRepairReadBackFailureIsAnError(t *testing.T) {
	dir := t.TempDir()
	in := writeTestPDF(t, dir, pdfa.PDFA2b, nil)
	out := filepath.Join(dir, "out.pdf")
	old := readBack
	readBack = func([]byte, string) (*pdf0.Document, error) { return nil, errors.New("planted read-back failure") }
	defer func() { readBack = old }()
	got, err := captureStdout(t, func() error { return cmdRepair([]string{in, out}) })
	if exitCode(err) != 3 || !strings.Contains(err.Error(), "planted read-back failure") {
		t.Errorf("read-back failure: got %v (exit %d), want an operational error", err, exitCode(err))
	}
	if strings.Contains(got, "violation(s) remain") {
		t.Errorf("reported a result despite the failed read-back:\n%s", got)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("output written although it does not read back")
	}
}

// TestOverwriteGuardsEachHold: the overwrite rule is enforced twice — by
// checkOutput before any work, and atomically by writeOutput when the file is
// put in place (which also covers a file appearing in between). Each must hold
// on its own.
func TestOverwriteGuardsEachHold(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.pdf")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkOutput(existing, false, nil); exitCode(err) != 2 {
		t.Errorf("checkOutput over an existing file: got %v, want a usage error", err)
	}
	if err := writeOutput(existing, false, []byte("new"), permOutput); exitCode(err) != 2 {
		t.Errorf("writeOutput over an existing file: got %v, want a usage error", err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "keep" {
		t.Errorf("existing file replaced: now %q", got)
	}
	if err := writeOutput(existing, true, []byte("new"), permOutput); err != nil {
		t.Fatalf("writeOutput with force: %v", err)
	}
	if got, _ := os.ReadFile(existing); string(got) != "new" {
		t.Errorf("forced write did not replace the file: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("files left in the directory: %v", entries)
	}
}

// TestReadLine: the line reader under the terminal prompt stops at the first
// newline, drops a CR, bounds the length and treats EOF with nothing typed as
// an error.
func TestReadLine(t *testing.T) {
	r := strings.NewReader("pw\r\nrest")
	got, err := readLine(r)
	if err != nil || got != "pw" {
		t.Errorf("readLine = %q, %v; want \"pw\"", got, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "rest" {
		t.Errorf("readLine consumed past the line: left %q", rest)
	}
	if got, err := readLine(strings.NewReader("no newline")); err != nil || got != "no newline" {
		t.Errorf("readLine at EOF = %q, %v", got, err)
	}
	if _, err := readLine(strings.NewReader("")); err == nil {
		t.Error("readLine of empty input: no error")
	}
	if _, err := readLine(strings.NewReader(strings.Repeat("x", maxPasswordLen+1) + "\n")); !errors.Is(err, errPasswordTooLong) {
		t.Errorf("overlong line: got %v", err)
	}
}

// undecodableContent gives doc a page whose content stream uses a filter pdf0
// does not decode (DCTDecode, an image filter), so every check that reads the
// page's content declines with a "limit" finding (unsupported-filter): a
// checker finding of the kind ordinary files produce.
func undecodableContent(t *testing.T, doc *pdf0.Document) {
	t.Helper()
	ref, err := doc.AddPage(pdf0.Page{Width: 612, Height: 792, Content: new(content.Builder).Save().Restore()})
	if err != nil {
		t.Fatal(err)
	}
	cs, ok := doc.Resolve(doc.ResolveDict(ref).Get("Contents")).(*object.Stream)
	if !ok {
		t.Fatal("the new page has no content stream")
	}
	cs.Dict.Set("Filter", object.Name("DCTDecode"))
	cs.Data = []byte("not a JPEG")
}

// writeUADoc writes a document the implemented PDF/UA-1 checks pass — PDF 1.7,
// tagged, a structure tree, a language, a displayed title and a pdfuaid:part 1
// identification — after mutate, and returns its path.
func writeUADoc(t *testing.T, mutate func(*pdf0.Document)) string {
	t.Helper()
	doc := pdf0.NewDocument()
	doc.Version = "1.7"
	cat := doc.ResolveDict(doc.Trailer.Get("Root"))
	cat.Set("Lang", object.String{Value: []byte("en-US")})
	cat.Set("MarkInfo", object.NewDictionary(object.Entry{Key: "Marked", Value: object.Boolean(true)}))
	cat.Set("ViewerPreferences", object.NewDictionary(object.Entry{Key: "DisplayDocTitle", Value: object.Boolean(true)}))
	root := &object.Dictionary{}
	root.Set("Type", object.Name("StructTreeRoot"))
	doc.Objects[99] = &object.IndirectObject{Number: 99, Value: root}
	cat.Set("StructTreeRoot", object.IndirectRef{Number: 99})
	md := &object.Dictionary{}
	md.Set("Type", object.Name("Metadata"))
	md.Set("Subtype", object.Name("XML"))
	doc.Objects[98] = &object.IndirectObject{Number: 98, Value: object.NewStream(md, []byte(`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description xmlns:pdfuaid="http://www.aiim.org/pdfua/ns/id/" pdfuaid:part="1"><dc:title xmlns:dc="http://purl.org/dc/elements/1.1/"><rdf:Alt><rdf:li xml:lang="x-default">Title</rdf:li></rdf:Alt></dc:title></rdf:Description></rdf:RDF></x:xmpmeta>`))}
	cat.Set("Metadata", object.IndirectRef{Number: 98})
	if mutate != nil {
		mutate(doc)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ua.pdf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFindingsExitCode pins the rule every validating command maps its
// findings by (follow-up to docs audit 2026-10-11: D64): no finding is
// success (0); any real violation is 1, checker findings beside it or not;
// findings that are all checker findings — the rules "limit" and "internal",
// pdf0.IsCheckerFinding — mean the run could not establish conformance either
// way, which is 3, the run failed. Before, a checker-only result exited 1, as
// if the file had been found to break a rule.
func TestFindingsExitCode(t *testing.T) {
	dir := t.TempDir()
	clean2b := writeTestPDF(t, dir, pdfa.PDFA2b, nil)
	limit2b := writeTestPDF(t, t.TempDir(), pdfa.PDFA2b, func(d *pdf0.Document) { undecodableContent(t, d) })
	undeclared := filepath.Join(t.TempDir(), "plain.pdf")
	{
		var buf bytes.Buffer
		if err := pdf0.NewDocument().Write(&buf); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(undeclared, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cleanUA := writeUADoc(t, nil)
	limitUA := writeUADoc(t, func(d *pdf0.Document) { undecodableContent(t, d) })
	noLang := func(d *pdf0.Document) { d.ResolveDict(d.Trailer.Get("Root")).Delete("Lang") }
	violationUA := writeUADoc(t, noLang)
	mixedUA := writeUADoc(t, func(d *pdf0.Document) { undecodableContent(t, d); noLang(d) })
	out := filepath.Join(t.TempDir(), "out.pdf")

	cases := []struct {
		name string
		cmd  func([]string) error
		args []string
		want int
		// lines: what stdout must say, for a run that prints its findings.
		lines []string
	}{
		{"validate, clean", cmdValidate, []string{clean2b}, 0, []string{"no violations found"}},
		{"validate, violations only", cmdValidate, []string{"-level", "1b", clean2b}, 1, []string{"6.7.11"}},
		{"validate, checker findings only", cmdValidate, []string{limit2b}, 3, []string{"limit] ", "unsupported-filter"}},
		{"validate, violations and checker findings", cmdValidate, []string{"-level", "1b", limit2b}, 1, []string{"6.7.11", "unsupported-filter"}},
		{"validate -level declared, nothing declared", cmdValidate, []string{"-level", "declared", undeclared}, 3, []string{"own declaration"}},
		{"ua, clean", cmdUA, []string{cleanUA}, 0, []string{"no PDF/UA violations found"}},
		{"ua, violations only", cmdUA, []string{violationUA}, 1, []string{"/Lang"}},
		{"ua, checker findings only", cmdUA, []string{limitUA}, 3, []string{"unsupported-filter"}},
		{"ua, violations and checker findings", cmdUA, []string{mixedUA}, 1, []string{"/Lang", "unsupported-filter"}},
		{"repair, clean", cmdRepair, []string{"-force", "-level", "2b", clean2b, out}, 0, []string{"0 violation(s) remain"}},
		{"repair, violations only", cmdRepair, []string{"-force", "-level", "1b", clean2b, out}, 1, []string{"violation(s) remain"}},
		{"repair, checker findings only", cmdRepair, []string{"-force", "-level", "2b", limit2b, out}, 3, []string{"0 violation(s) remain, 1 checker finding(s)"}},
		{"repair, violations and checker findings", cmdRepair, []string{"-force", "-level", "1b", limit2b, out}, 1, []string{"violation(s) remain, 1 checker finding(s)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := captureStdout(t, func() error { return c.cmd(c.args) })
			if code := exitCode(err); code != c.want {
				t.Errorf("exit %d (%v), want %d\n%s", code, err, c.want, got)
			}
			for _, l := range c.lines {
				if !strings.Contains(got, l) {
					t.Errorf("stdout does not say %q:\n%s", l, got)
				}
			}
			// repair writes its output whatever the findings say.
			if c.args[len(c.args)-1] == out {
				if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
					t.Errorf("repair wrote no output: %v", err)
				}
				os.Remove(out)
			}
		})
	}
}
