package main

// Black-box tests: they build the pdf0 command into a temporary directory and
// execute it, so what they check is the behaviour a user gets — exit codes,
// what reaches stdout and stderr, and what is (and is not) left on disk.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/content"
	"github.com/mgilbir/pdf0/pdfa"
)

// cliBinary is the built command, set up by TestMain.
var cliBinary string

func TestMain(m *testing.M) {
	code, err := buildAndRun(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// buildAndRun builds the command into a fresh temporary directory (never the
// repository) and runs the tests. PDF0_CLI_BIN names a prebuilt binary
// instead, which is how the black-box tests are pointed at an older build to
// see them fail.
func buildAndRun(m *testing.M) (int, error) {
	if bin := os.Getenv("PDF0_CLI_BIN"); bin != "" {
		cliBinary = bin
		return m.Run(), nil
	}
	dir, err := os.MkdirTemp("", "pdf0-cli-test-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		if goTool, err = exec.LookPath("go"); err != nil {
			return 0, fmt.Errorf("no go tool to build the command with: %v", err)
		}
	}
	cliBinary = filepath.Join(dir, "pdf0")
	build := exec.Command(goTool, "build", "-o", cliBinary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("building the pdf0 command: %v\n%s", err, out)
	}
	return m.Run(), nil
}

// cliRun describes one invocation.
type cliRun struct {
	args  []string
	env   []string // added to a clean environment
	stdin []byte   // nil: stdin is /dev/null
	// stdinFile and stdoutFile replace stdin/stdout with open files.
	stdinFile, stdoutFile *os.File
	// shell is a prefix run by /bin/sh before exec'ing the command, e.g.
	// "umask 022; ulimit -f 1".
	shell string
}

type cliResult struct {
	stdout, stderr string
	code           int
}

// cleanEnv is the test process's environment without any PDF0_* variable, so
// a developer's own PDF0_PASSWORD never leaks into a test.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PDF0_") {
			env = append(env, kv)
		}
	}
	return env
}

func runCLI(t *testing.T, r cliRun) cliResult {
	t.Helper()
	var cmd *exec.Cmd
	if r.shell != "" {
		cmd = exec.Command("/bin/sh", append([]string{"-c", r.shell + `; exec "$0" "$@"`, cliBinary}, r.args...)...)
	} else {
		cmd = exec.Command(cliBinary, r.args...)
	}
	cmd.Env = append(cleanEnv(), r.env...)
	// Run in a scratch directory, so a relative path the command writes by
	// mistake (an old build took "-" as a file name) never lands in the repository.
	cmd.Dir = t.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if r.stdoutFile != nil {
		cmd.Stdout = r.stdoutFile
	}
	switch {
	case r.stdinFile != nil:
		cmd.Stdin = r.stdinFile
	case r.stdin != nil:
		cmd.Stdin = bytes.NewReader(r.stdin)
	}
	err := cmd.Run()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running pdf0 %v: %v", r.args, err)
		}
		code = ee.ExitCode()
	}
	return cliResult{stdout.String(), stderr.String(), code}
}

func (res cliResult) String() string {
	return fmt.Sprintf("exit %d\nstdout: %q\nstderr: %q", res.code, res.stdout, res.stderr)
}

// onePageDoc builds a conforming PDF/A-2b document with one empty page.
func onePageDoc(t *testing.T) *pdf0.Document {
	t.Helper()
	doc, err := pdf0.NewPDFADocument(pdfa.PDFA2b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.AddPage(pdf0.Page{Width: 612, Height: 792, Content: new(content.Builder).Save().Restore()}); err != nil {
		t.Fatal(err)
	}
	return doc
}

// pdfBytes builds a small conforming PDF/A-2b document with one page.
func pdfBytes(t *testing.T, mutate func(*pdf0.Document)) []byte {
	t.Helper()
	doc := onePageDoc(t)
	if mutate != nil {
		mutate(doc)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// encryptedBytes is pdfBytes encrypted with the given passwords.
func encryptedBytes(t *testing.T, user, owner string) []byte {
	t.Helper()
	doc := onePageDoc(t)
	if err := doc.SetEncryption(user, owner); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// openDoc parses a PDF the command wrote.
func openDoc(t *testing.T, data []byte, password string) *pdf0.Document {
	t.Helper()
	doc, err := parseDoc(data, password)
	if err != nil {
		t.Fatalf("the written PDF does not parse: %v", err)
	}
	return doc
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s was written; it should not exist", path)
	}
}

// assertNoTempFiles checks that no temp file was left behind in dir.
func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// TestCLIRefusesPasswordsOnArgv: every flag that used to take a password on
// the command line is refused with exit 2, nothing is written, and the value
// is never echoed back (C113).
func TestCLIRefusesPasswordsOnArgv(t *testing.T) {
	dir := t.TempDir()
	const secret = "s3cret-on-argv"
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, secret, "ownerpw"))
	out := filepath.Join(dir, "out.pdf")
	cases := [][]string{
		{"info", "-password", secret, enc},
		{"validate", "-password", secret, enc},
		{"decrypt", "-password", secret, enc, out},
		{"extract", "-password", secret, enc},
		{"repair", "-password", secret, enc, out},
		{"ua", "-password", secret, enc},
		{"encrypt", "-user", secret, plain, out},
		{"encrypt", "-owner", secret, plain, out},
	}
	for _, args := range cases {
		res := runCLI(t, cliRun{args: args})
		if res.code != 2 {
			t.Errorf("pdf0 %s: want exit 2 (refused), got %v", args[0], res)
		}
		if !strings.Contains(res.stderr, "visible to every user") {
			t.Errorf("pdf0 %s: the refusal does not say why:\n%v", args[0], res)
		}
		if strings.Contains(res.stdout+res.stderr, secret) {
			t.Errorf("pdf0 %s: the password was echoed back:\n%v", args[0], res)
		}
		assertNotExist(t, out)
	}
}

// TestCLIPasswordSources: a password is taken from its file flag (one trailing
// newline removed), from stdin through "-", or from the environment; the file
// flag wins over the environment (C113).
func TestCLIPasswordSources(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	good := writeFile(t, filepath.Join(dir, "good"), []byte("userpw\n"))
	goodCRLF := writeFile(t, filepath.Join(dir, "goodcrlf"), []byte("ownerpw\r\n"))
	cases := []struct {
		name string
		run  cliRun
	}{
		{"file", cliRun{args: []string{"extract", "-password-file", good, enc}}},
		{"file with CRLF, owner password", cliRun{args: []string{"extract", "-password-file", goodCRLF, enc}}},
		{"stdin", cliRun{args: []string{"extract", "-password-file", "-", enc}, stdin: []byte("userpw\n")}},
		{"environment", cliRun{args: []string{"extract", enc}, env: []string{"PDF0_PASSWORD=userpw"}}},
		{"file beats environment", cliRun{args: []string{"extract", "-password-file", good, enc}, env: []string{"PDF0_PASSWORD=wrong"}}},
	}
	for _, c := range cases {
		res := runCLI(t, c.run)
		if res.code != 0 {
			t.Errorf("%s: want exit 0, got %v", c.name, res)
		}
	}
	// And the environment really is consulted: a wrong one fails.
	res := runCLI(t, cliRun{args: []string{"extract", enc}, env: []string{"PDF0_PASSWORD=wrong"}})
	if res.code != 3 {
		t.Errorf("wrong PDF0_PASSWORD: want exit 3, got %v", res)
	}
	// stdin cannot be both the input and the password.
	res = runCLI(t, cliRun{args: []string{"extract", "-password-file", "-", "-"}, stdin: readFile(t, enc)})
	if res.code != 2 || !strings.Contains(res.stderr, "stdin cannot be used for both") {
		t.Errorf("stdin as password and input: want exit 2, got %v", res)
	}
}

// TestCLIWrongPasswordIsNotMissingPassword: "no password was given" and "the
// password given is wrong" are different failures with different messages,
// both exit 3 (C155).
func TestCLIWrongPasswordIsNotMissingPassword(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	wrong := writeFile(t, filepath.Join(dir, "wrong"), []byte("nope"))
	for _, cmd := range []string{"validate", "extract", "ua", "decrypt", "repair"} {
		args := []string{cmd, enc}
		if cmd == "decrypt" || cmd == "repair" {
			args = append(args, filepath.Join(dir, cmd+"-out.pdf"))
		}
		missing := runCLI(t, cliRun{args: args})
		if missing.code != 3 || !strings.Contains(missing.stderr, "no password was supplied") {
			t.Errorf("%s without a password: want exit 3 naming the missing password, got %v", cmd, missing)
		}
		withWrong := runCLI(t, cliRun{args: append([]string{cmd, "-password-file", wrong}, args[1:]...)})
		if withWrong.code != 3 || !strings.Contains(withWrong.stderr, "password is wrong") {
			t.Errorf("%s with a wrong password: want exit 3 naming the wrong password, got %v", cmd, withWrong)
		}
		if strings.Contains(withWrong.stderr, "no password was supplied") {
			t.Errorf("%s with a wrong password claims none was supplied:\n%v", cmd, withWrong)
		}
		if cmd == "decrypt" || cmd == "repair" {
			assertNotExist(t, args[len(args)-1])
		}
	}
}

// TestCLIUnsupportedEncryptionIsNotAPasswordError: a file whose security
// handler pdf0 does not implement is reported as such, with or without a
// password; blaming the password would send the user looking for another one.
func TestCLIUnsupportedEncryptionIsNotAPasswordError(t *testing.T) {
	dir := t.TempDir()
	data := encryptedBytes(t, "userpw", "ownerpw")
	// Same length, so every xref offset stays valid.
	if bytes.Count(data, []byte("/Standard")) != 1 {
		t.Fatalf("expected exactly one /Standard in the encrypted file")
	}
	data = bytes.Replace(data, []byte("/Standard"), []byte("/Unknown1"), 1)
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), data)
	pw := writeFile(t, filepath.Join(dir, "pw"), []byte("userpw"))
	for _, args := range [][]string{{"validate", enc}, {"validate", "-password-file", pw, enc}} {
		res := runCLI(t, cliRun{args: args})
		if res.code != 3 || !strings.Contains(res.stderr, "Unknown1") {
			t.Errorf("%v: want exit 3 naming the unsupported handler, got %v", args, res)
		}
		if strings.Contains(res.stderr, "no password was supplied") || strings.Contains(res.stderr, "password is wrong") {
			t.Errorf("%v: blames the password for an unsupported handler: %v", args, res)
		}
	}
}

// TestCLIDecryptWritesPrivateFile:decrypted output is created 0600 whatever
// the umask, and ordinary output follows the umask (C155).
func TestCLIDecryptWritesPrivateFile(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	pw := writeFile(t, filepath.Join(dir, "pw"), []byte("userpw\n"))
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	dec := filepath.Join(dir, "dec.pdf")
	rep := filepath.Join(dir, "rep.pdf")
	reenc := filepath.Join(dir, "reenc.pdf")

	res := runCLI(t, cliRun{shell: "umask 022", args: []string{"decrypt", "-password-file", pw, enc, dec}})
	if res.code != 0 {
		t.Fatalf("decrypt: %v", res)
	}
	res = runCLI(t, cliRun{shell: "umask 022", args: []string{"repair", "-password-file", pw, enc, rep}})
	if res.code != 0 && res.code != 1 {
		t.Fatalf("repair: %v", res)
	}
	res = runCLI(t, cliRun{shell: "umask 022", args: []string{"encrypt", "-user-password-file", pw, plain, reenc}})
	if res.code != 0 {
		t.Fatalf("encrypt: %v", res)
	}
	for path, want := range map[string]fs.FileMode{dec: 0o600, rep: 0o600, reenc: 0o644} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %v, want %v", filepath.Base(path), got, want)
		}
	}
	if doc := openDoc(t, readFile(t, dec), ""); doc.Encrypted {
		t.Error("decrypted output is still encrypted")
	}
	if doc := openDoc(t, readFile(t, reenc), "userpw"); !doc.Encrypted || doc.Locked() {
		t.Errorf("encrypted output: Encrypted=%v Locked=%v, want encrypted and opened by the user password", doc.Encrypted, doc.Locked())
	}
}

// TestCLIEncryptPasswords: encrypt takes the user password from its file or
// the environment and the owner password separately; with neither and no
// terminal it refuses, and an empty user password is refused (C113).
func TestCLIEncryptPasswords(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	ownerFile := writeFile(t, filepath.Join(dir, "owner"), []byte("theowner\n"))
	empty := writeFile(t, filepath.Join(dir, "empty"), nil)

	out := filepath.Join(dir, "a.pdf")
	res := runCLI(t, cliRun{args: []string{"encrypt", "-owner-password-file", ownerFile, plain, out},
		env: []string{"PDF0_USER_PASSWORD=theuser"}})
	if res.code != 0 {
		t.Fatalf("encrypt: %v", res)
	}
	data := readFile(t, out)
	for _, pw := range []string{"theuser", "theowner"} {
		if openDoc(t, data, pw).Locked() {
			t.Errorf("the %q password does not open the encrypted output", pw)
		}
	}
	if !openDoc(t, data, "").Locked() {
		t.Error("the encrypted output opens with no password")
	}

	out = filepath.Join(dir, "b.pdf")
	res = runCLI(t, cliRun{args: []string{"encrypt", plain, out}})
	if res.code != 2 || !strings.Contains(res.stderr, "needs a user password") {
		t.Errorf("encrypt with no password and no terminal: want exit 2, got %v", res)
	}
	res = runCLI(t, cliRun{args: []string{"encrypt", "-user-password-file", empty, plain, out}})
	if res.code != 2 || !strings.Contains(res.stderr, "must not be empty") {
		t.Errorf("encrypt with an empty user password: want exit 2, got %v", res)
	}
	res = runCLI(t, cliRun{args: []string{"encrypt", "-owner-password-file", ownerFile, plain, out}})
	if res.code != 2 {
		t.Errorf("encrypt with only an owner password: want exit 2, got %v", res)
	}
	assertNotExist(t, out)
}

// TestCLIRefusesOutputThatIsAnInput: no command that writes may replace one of
// its inputs, however the output is spelled — the same path, another spelling,
// a symlink, or a hard link — and -force does not change that (C155).
func TestCLIRefusesOutputThatIsAnInput(t *testing.T) {
	dir := t.TempDir()
	plainData := pdfBytes(t, nil)
	encData := encryptedBytes(t, "userpw", "ownerpw")
	pw := writeFile(t, filepath.Join(dir, "pw"), []byte("userpw"))
	other := writeFile(t, filepath.Join(dir, "other.pdf"), plainData)

	spellings := map[string]func(in string) string{
		"same path": func(in string) string { return in },
		"other spelling": func(in string) string {
			return filepath.Join(filepath.Dir(in), ".", "sub", "..", filepath.Base(in))
		},
		"symlink": func(in string) string {
			link := in + ".link"
			if err := os.Symlink(in, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
		"hard link": func(in string) string {
			link := in + ".hard"
			if err := os.Link(in, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	commands := map[string]struct {
		data []byte
		args func(in, out string) []string
	}{
		"decrypt": {encData, func(in, out string) []string { return []string{"decrypt", "-force", "-password-file", pw, in, out} }},
		"encrypt": {plainData, func(in, out string) []string {
			return []string{"encrypt", "-force", "-user-password-file", pw, in, out}
		}},
		"repair": {plainData, func(in, out string) []string { return []string{"repair", "-force", in, out} }},
		"merge first": {plainData, func(in, out string) []string {
			return []string{"merge", "-force", out, in, other}
		}},
		"merge last": {plainData, func(in, out string) []string {
			return []string{"merge", "-force", out, other, in}
		}},
	}
	i := 0
	for cname, c := range commands {
		for sname, spell := range spellings {
			i++
			in := writeFile(t, filepath.Join(dir, fmt.Sprintf("in%d.pdf", i)), c.data)
			out := spell(in)
			res := runCLI(t, cliRun{args: c.args(in, out)})
			if res.code != 2 || !strings.Contains(res.stderr, "is the input") {
				t.Errorf("%s, output as %s of the input: want exit 2 refusing, got %v", cname, sname, res)
			}
			if !bytes.Equal(readFile(t, in), c.data) {
				t.Errorf("%s, output as %s of the input: the input was modified", cname, sname)
			}
		}
	}
	assertNoTempFiles(t, dir)
}

// TestCLIMergeNeedsTwoInputs: "merge a.pdf b.pdf" used to replace a.pdf with a
// copy of b.pdf; one input is now a usage error and nothing is written (C155).
func TestCLIMergeNeedsTwoInputs(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, filepath.Join(dir, "a.pdf"), pdfBytes(t, nil))
	orig := readFile(t, a)
	b := writeFile(t, filepath.Join(dir, "b.pdf"), pdfBytes(t, nil))
	// Without -force the overwrite guard would refuse anyway; with it, only
	// the two-input rule stands between the command and a.pdf.
	for _, args := range [][]string{{"merge", a, b}, {"merge", "-force", a, b}} {
		res := runCLI(t, cliRun{args: args})
		if res.code != 2 || !strings.Contains(res.stderr, "usage: pdf0 merge") {
			t.Errorf("%v: want exit 2 with the usage line, got %v", args, res)
		}
	}
	if !bytes.Equal(readFile(t, a), orig) {
		t.Error("merge with one input modified its first operand")
	}
}

// TestCLIRefusesExistingOutput: an existing output is replaced only with
// -force, for every command that writes (C155).
func TestCLIRefusesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	plain2 := writeFile(t, filepath.Join(dir, "plain2.pdf"), pdfBytes(t, nil))
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	pw := writeFile(t, filepath.Join(dir, "pw"), []byte("userpw"))
	precious := []byte("precious, not a PDF")
	commands := map[string]func(force bool, out string) []string{
		"decrypt": func(force bool, out string) []string {
			return withForce(force, "decrypt", "-password-file", pw, enc, out)
		},
		"encrypt": func(force bool, out string) []string {
			return withForce(force, "encrypt", "-user-password-file", pw, plain, out)
		},
		"repair": func(force bool, out string) []string { return withForce(force, "repair", plain, out) },
		"merge":  func(force bool, out string) []string { return withForce(force, "merge", out, plain, plain2) },
	}
	for name, args := range commands {
		out := writeFile(t, filepath.Join(dir, name+"-out.pdf"), precious)
		res := runCLI(t, cliRun{args: args(false, out)})
		if res.code != 2 || !strings.Contains(res.stderr, "-force") {
			t.Errorf("%s over an existing file without -force: want exit 2 naming -force, got %v", name, res)
		}
		if !bytes.Equal(readFile(t, out), precious) {
			t.Errorf("%s replaced an existing file without -force", name)
		}
		res = runCLI(t, cliRun{args: args(true, out)})
		if res.code != 0 {
			t.Errorf("%s -force: want exit 0, got %v", name, res)
		}
		if bytes.Equal(readFile(t, out), precious) {
			t.Errorf("%s -force did not replace the existing file", name)
		}
	}
	assertNoTempFiles(t, dir)
}

func withForce(force bool, cmd string, rest ...string) []string {
	args := []string{cmd}
	if force {
		args = append(args, "-force")
	}
	return append(args, rest...)
}

// TestCLIForceWritesThroughSymlink: -force over a symlinked output replaces the
// file it points to and leaves the link a link, as a shell redirect would.
func TestCLIForceWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	target := writeFile(t, filepath.Join(dir, "target.pdf"), []byte("old"))
	link := filepath.Join(dir, "link.pdf")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	res := runCLI(t, cliRun{args: []string{"repair", "-force", plain, link}})
	if res.code != 0 {
		t.Fatalf("repair -force through a symlink: %v", res)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced by a regular file (%v)", err)
	}
	openDoc(t, readFile(t, target), "")
}

// TestCLIFailedWriteLeavesOutputIntact: output is written to a temp file and
// renamed into place, so a write that fails part-way (here: the file-size
// limit) leaves the existing output as it was and no temp file behind (C155).
func TestCLIFailedWriteLeavesOutputIntact(t *testing.T) {
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	plain2 := writeFile(t, filepath.Join(dir, "plain2.pdf"), pdfBytes(t, nil))
	old := pdfBytes(t, nil)
	out := writeFile(t, filepath.Join(dir, "out.pdf"), old)
	// ulimit -f counts 512- or 1024-byte blocks depending on the shell; one
	// block is far below the size of the merged document either way.
	res := runCLI(t, cliRun{shell: "ulimit -f 1", args: []string{"merge", "-force", out, plain, plain2}})
	if res.code != 3 {
		t.Errorf("merge past the file-size limit: want exit 3, got %v", res)
	}
	if !bytes.Equal(readFile(t, out), old) {
		t.Error("a failed write damaged the existing output")
	}
	assertNoTempFiles(t, dir)

	fresh := filepath.Join(dir, "fresh.pdf")
	res = runCLI(t, cliRun{shell: "ulimit -f 1", args: []string{"merge", fresh, plain, plain2}})
	if res.code != 3 {
		t.Errorf("merge to a new file past the file-size limit: want exit 3, got %v", res)
	}
	assertNotExist(t, fresh)
	assertNoTempFiles(t, dir)
}

// TestCLIStdinStdout: "-" is stdin as an input and stdout as an output.
func TestCLIStdinStdout(t *testing.T) {
	dir := t.TempDir()
	plainData := pdfBytes(t, nil)
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), plainData)
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))

	res := runCLI(t, cliRun{args: []string{"info", "-"}, stdin: plainData})
	if res.code != 0 || !strings.Contains(res.stdout, "pages:     1") {
		t.Errorf("info -: %v", res)
	}
	res = runCLI(t, cliRun{args: []string{"validate", "-"}, stdin: plainData})
	if res.code != 0 {
		t.Errorf("validate -: %v", res)
	}

	res = runCLI(t, cliRun{args: []string{"decrypt", enc, "-"}, env: []string{"PDF0_PASSWORD=userpw"}})
	if res.code != 0 {
		t.Fatalf("decrypt to stdout: %v", res)
	}
	if doc := openDoc(t, []byte(res.stdout), ""); doc.Encrypted {
		t.Error("decrypt to stdout wrote an encrypted document")
	}

	res = runCLI(t, cliRun{args: []string{"merge", "-", "-", plain}, stdin: plainData})
	if res.code != 0 {
		t.Fatalf("merge from stdin to stdout: %v", res)
	}
	if n := openDoc(t, []byte(res.stdout), "").PageCount(); n != 2 {
		t.Errorf("merged %d pages, want 2", n)
	}
	res = runCLI(t, cliRun{args: []string{"merge", "-", "-", "-"}, stdin: plainData})
	if res.code != 2 {
		t.Errorf("two inputs from stdin: want exit 2, got %v", res)
	}

	// repair to stdout: the PDF is the only thing on stdout; the report moves
	// to stderr.
	res = runCLI(t, cliRun{args: []string{"repair", plain, "-"}})
	if res.code != 0 {
		t.Fatalf("repair to stdout: %v", res)
	}
	if !strings.HasPrefix(res.stdout, "%PDF-") || !strings.Contains(res.stderr, "0 violation(s) remain") {
		t.Errorf("repair to stdout mixed the report into the PDF: %v", res)
	}
	openDoc(t, []byte(res.stdout), "")
}

// TestCLIStdoutRedirectedToInput: `pdf0 decrypt in.pdf - > in.pdf` has lost
// in.pdf before pdf0 runs; pdf0 says so instead of a bare parse error.
func TestCLIStdoutRedirectedToInput(t *testing.T) {
	dir := t.TempDir()
	in := writeFile(t, filepath.Join(dir, "in.pdf"), pdfBytes(t, nil))
	f, err := os.OpenFile(in, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res := runCLI(t, cliRun{args: []string{"repair", in, "-"}, stdoutFile: f})
	if res.code != 2 || !strings.Contains(res.stderr, "already truncated") {
		t.Errorf("stdout redirected onto the input: want exit 2 explaining it, got %v", res)
	}
}

// TestCLIStdoutWriteFailure: a result that cannot be written (stdout on a full
// device) is an operational error, not a silent exit 0.
func TestCLIStdoutWriteFailure(t *testing.T) {
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no /dev/full on this system: %v", err)
	}
	defer full.Close()
	dir := t.TempDir()
	plain := writeFile(t, filepath.Join(dir, "plain.pdf"), pdfBytes(t, nil))
	for _, args := range [][]string{{"info", plain}, {"validate", plain}, {"extract", plain}, {"repair", plain, "-"}} {
		res := runCLI(t, cliRun{args: args, stdoutFile: full})
		if res.code != 3 || !strings.Contains(res.stderr, "writing to stdout") {
			t.Errorf("pdf0 %s with stdout on /dev/full: want exit 3, got %v", args[0], res)
		}
	}
}

// TestCLIRepairRefusesLockedFile: repair on a file it cannot decrypt used to
// write the ciphertext back and report "0 fixes"; it now refuses (C155).
func TestCLIRepairRefusesLockedFile(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	out := filepath.Join(dir, "out.pdf")
	res := runCLI(t, cliRun{args: []string{"repair", enc, out}})
	if res.code != 3 || !strings.Contains(res.stderr, "no password was supplied") {
		t.Errorf("repair of a locked file: want exit 3, got %v", res)
	}
	assertNotExist(t, out)
}

// TestCLIInfoReportsLocked: info reports whether the supplied password (if
// any) opened the file.
func TestCLIInfoReportsLocked(t *testing.T) {
	dir := t.TempDir()
	enc := writeFile(t, filepath.Join(dir, "enc.pdf"), encryptedBytes(t, "userpw", "ownerpw"))
	res := runCLI(t, cliRun{args: []string{"info", enc}})
	if res.code != 0 || !strings.Contains(res.stdout, "locked:    true") {
		t.Errorf("info without a password: %v", res)
	}
	res = runCLI(t, cliRun{args: []string{"info", enc}, env: []string{"PDF0_PASSWORD=userpw"}})
	if res.code != 0 || !strings.Contains(res.stdout, "locked:    false") {
		t.Errorf("info with the password: %v", res)
	}
}
