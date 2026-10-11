package main

// The command lines and output shown in docs/cli.md are this file's output.
// They were pasted in by hand once, and went stale while still claiming to be
// "real captured output": `info` counted objects the document no longer had,
// `ua` printed its findings in another order, and `merge` "printed nothing"
// where it printed two lines (docs audit 2026-10-11 D35). The synopsis claimed
// to be `pdf0 -h`, and the binary's own help had fallen behind it (D14).
//
// So each transcript is a list of shell command lines, run here, in order, in
// one scratch directory, by /bin/sh exactly as written, and the document's
// generated sections must match what they print. The fixtures are made the
// same way, from recipes a reader can run in a checkout, so every file a
// transcript names can be rebuilt.

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
)

var update = flag.Bool("update", false, "rewrite the generated sections of docs/cli.md instead of comparing them")

// cliDocFixtures are the files the transcripts read, each with the command
// line that makes it, run in a checkout of this repository. `go run
// ./examples/<name>` and `go run ./cmd/pdf0/testdata/<name>` are run as
// written: the test builds that program and runs it in the scratch directory,
// which is what `go run` does.
var cliDocFixtures = []string{
	"go run ./examples/simple_pdf > simple.pdf",
	"go run ./examples/simple_pdf17 > simple17.pdf",
	"go run ./examples/simple_pdfa > a4.pdf",
	"go run ./examples/flavored -o fl > /dev/null && cp fl/flavored-PDFA-2b.pdf clean2b.pdf",
	"PDF0_USER_PASSWORD=secret pdf0 encrypt simple.pdf enc.pdf",
	// The same file with its security handler renamed, in place and at the
	// same length, so that every offset in the file still holds.
	"LC_ALL=C sed 's|/Filter /Standard|/Filter /Bespoke_|' enc.pdf > unk.pdf",
	// Signed with a throwaway self-signed key.
	"go run ./cmd/pdf0/testdata/signpdf < simple.pdf > signed.pdf",
}

// cliDocSections are the generated sections of docs/cli.md, in the order
// they run. Later sections may read what earlier ones wrote.
var cliDocSections = []struct {
	name string
	runs []string
}{
	{"info", []string{"pdf0 info simple.pdf"}},
	{"validate", []string{
		"pdf0 validate clean2b.pdf",
		"pdf0 validate -level 1b clean2b.pdf",
		"pdf0 validate -level declared clean2b.pdf",
		"pdf0 validate -level declared simple.pdf",
		"pdf0 validate -level 5b clean2b.pdf",
	}},
	{"passwords", []string{
		"pdf0 validate enc.pdf",
		"PDF0_PASSWORD=wrong pdf0 validate enc.pdf",
		"pdf0 validate unk.pdf",
	}},
	{"ua", []string{"pdf0 ua simple.pdf"}},
	{"decrypt", []string{
		"PDF0_PASSWORD=secret pdf0 decrypt enc.pdf dec.pdf",
		"pdf0 info dec.pdf",
		"pdf0 decrypt simple.pdf out.pdf",
		"PDF0_PASSWORD=wrong pdf0 decrypt enc.pdf out.pdf",
		"PDF0_PASSWORD=secret pdf0 decrypt enc.pdf dec.pdf",
		"PDF0_PASSWORD=secret pdf0 decrypt enc.pdf /nonexistentdir/out.pdf",
	}},
	{"encrypt", []string{
		"PDF0_USER_PASSWORD=secret pdf0 encrypt clean2b.pdf enc2b.pdf",
		"PDF0_USER_PASSWORD=secret pdf0 encrypt enc.pdf enc2.pdf",
		"pdf0 encrypt simple.pdf enc3.pdf",
	}},
	{"extract", []string{"pdf0 extract simple.pdf"}},
	{"repair", []string{
		"pdf0 repair -level 2b simple.pdf rep.pdf",
		"PDF0_PASSWORD=secret pdf0 repair enc2b.pdf rep2b.pdf",
		"pdf0 repair -level 9z simple.pdf rep9.pdf",
	}},
	{"merge", []string{
		"pdf0 merge merged3.pdf simple17.pdf a4.pdf simple.pdf",
		"pdf0 info merged3.pdf",
	}},
	{"merge-encrypted", []string{"pdf0 merge m2.pdf simple.pdf enc.pdf"}},
	{"signed", []string{
		"PDF0_USER_PASSWORD=secret pdf0 encrypt signed.pdf encsigned.pdf",
		"pdf0 merge msigned.pdf simple17.pdf signed.pdf",
		"PDF0_USER_PASSWORD=secret pdf0 encrypt -invalidate-signatures signed.pdf encsigned.pdf",
	}},
	// Last, because they destroy what they redirect onto (a copy).
	{"redirect", []string{
		"cp simple17.pdf same.pdf",
		"pdf0 repair same.pdf - >> same.pdf",
		"pdf0 repair same.pdf - > same.pdf",
	}},
}

// goRunProgram finds the programs a fixture recipe runs with `go run`: an
// example, or a fixture maker under cmd/pdf0/testdata.
var goRunProgram = regexp.MustCompile(`go run \./((?:examples|cmd/pdf0/testdata)/\w+)`)

// tempName is the random part of an output's temporary file, which differs
// on every run.
var tempName = regexp.MustCompile(`\.pdf0-[0-9a-f]+\.tmp`)

// TestCLIDocTranscripts runs every generated section of docs/cli.md and
// compares it with the document, or rewrites the document under -update.
func TestCLIDocTranscripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the transcripts are shell command lines")
	}
	root := testfiles.Root(t)
	dir := t.TempDir()
	env := docEnv(t, root, dir)

	for _, f := range cliDocFixtures {
		if res := shellRun(t, dir, env, f); res.code != 0 {
			t.Fatalf("fixture %q: %v", f, res)
		}
	}

	want := map[string]string{"help": renderHelp(t, dir, env)}
	order := []string{"help"}
	var fixtures strings.Builder
	fixtures.WriteString("\n\n```\n")
	for _, f := range cliDocFixtures {
		fixtures.WriteString(f + "\n")
	}
	fixtures.WriteString("```\n\n")
	want["fixtures"] = fixtures.String()
	order = append(order, "fixtures")
	for _, s := range cliDocSections {
		var b strings.Builder
		b.WriteString("\n\n```\n")
		for _, line := range s.runs {
			b.WriteString(renderRun(line, shellRun(t, dir, env, line)))
		}
		b.WriteString("```\n\n")
		want[s.name] = b.String()
		order = append(order, s.name)
	}

	path := filepath.Join(root, "docs", "cli.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	changed := false
	for _, name := range order {
		begin, end := cliDocMarkers(name)
		i, j := strings.Index(doc, begin), strings.Index(doc, end)
		if i < 0 || j < i {
			t.Errorf("docs/cli.md has no generated %q section; add the two marker lines\n%s\n%s", name, begin, end)
			continue
		}
		got := doc[i+len(begin) : j]
		if got == want[name] {
			continue
		}
		if *update {
			doc = doc[:i+len(begin)] + want[name] + doc[j:]
			changed = true
			continue
		}
		t.Errorf("docs/cli.md: the %q section is not what the command prints; run\n\tgo test ./cmd/pdf0 -run TestCLIDocTranscripts -update\n--- document\n%s\n--- command\n%s", name, got, want[name])
	}
	if changed {
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("rewrote docs/cli.md")
	}
}

// TestHelpNamesEveryLevel: the help's level list is pdfa.Levels(), not a copy
// of it. A hand-kept list said "1b|2b|3b|4" while -level took eleven names.
func TestHelpNamesEveryLevel(t *testing.T) {
	res := runCLI(t, cliRun{args: []string{"-h"}})
	if res.code != 0 || res.stdout != "" {
		t.Fatalf("pdf0 -h: want exit 0 and the help on stderr only, got %v", res)
	}
	for _, l := range pdfa.Levels() {
		name := levelName(l)
		if !regexp.MustCompile(`(^|[\s,|])` + regexp.QuoteMeta(name) + `([\s,|.]|$)`).MatchString(res.stderr) {
			t.Errorf("pdf0 -h does not name the level %s:\n%s", name, res.stderr)
		}
		if _, ok := parseLevel(name, false); !ok {
			t.Errorf("-level %s, which the help offers, is refused", name)
		}
	}
	if !strings.Contains(res.stderr, "declared") {
		t.Errorf("pdf0 -h does not mention -level declared:\n%s", res.stderr)
	}
}

func cliDocMarkers(name string) (begin, end string) {
	return fmt.Sprintf("<!-- BEGIN GENERATED: cli %s. From the pdf0 command by TestCLIDocTranscripts; regenerate with `go test ./cmd/pdf0 -run TestCLIDocTranscripts -update`. -->", name),
		fmt.Sprintf("<!-- END GENERATED: cli %s -->", name)
}

// renderHelp is `pdf0 -h` as the synopsis shows it: the text alone.
func renderHelp(t *testing.T, dir string, env []string) string {
	t.Helper()
	res := shellRun(t, dir, env, "pdf0 -h")
	if res.code != 0 || res.stdout != "" {
		t.Fatalf("pdf0 -h: want exit 0 and the help on stderr only, got %v", res)
	}
	return "\n\n```\n" + res.stderr + "```\n\n"
}

// renderRun shows one command line and what it printed: stdout, then stderr
// with each line marked, then the exit status.
func renderRun(line string, res cliResult) string {
	var b strings.Builder
	b.WriteString("$ " + line + "\n")
	b.WriteString(res.stdout)
	if res.stdout != "" && !strings.HasSuffix(res.stdout, "\n") {
		// extract writes no trailing newline; the transcript needs one.
		b.WriteString("\n")
	}
	for _, l := range strings.Split(strings.TrimSuffix(res.stderr, "\n"), "\n") {
		if l != "" {
			b.WriteString(tempName.ReplaceAllString(l, ".pdf0-….tmp") + "   # stderr\n")
		}
	}
	fmt.Fprintf(&b, "# exit %d\n", res.code)
	return b.String()
}

// docEnv is the environment the transcripts run in: no PDF0_* variable of
// the developer's, the built command first on PATH, and `go run
// ./examples/<name>` or `go run ./cmd/pdf0/testdata/<name>` answered by the
// built program.
func docEnv(t *testing.T, root, dir string) []string {
	t.Helper()
	bin := filepath.Join(dir, ".bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cliBinary, filepath.Join(bin, "pdf0")); err != nil {
		t.Fatal(err)
	}
	goTool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		if goTool, err = exec.LookPath("go"); err != nil {
			t.Fatalf("no go tool to build the examples with: %v", err)
		}
	}
	built := map[string]bool{}
	for _, f := range cliDocFixtures {
		for _, m := range goRunProgram.FindAllStringSubmatch(f, -1) {
			if built[m[1]] {
				continue
			}
			built[m[1]] = true
			cmd := exec.Command(goTool, "build", "-o", filepath.Join(bin, "prog-"+strings.ReplaceAll(m[1], "/", "-")), "./"+m[1])
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("building %s: %v\n%s", m[1], err, out)
			}
		}
	}
	// A script in place of the go command: it runs only the programs
	// built above, so a recipe that needs anything else fails here.
	goFunc := filepath.Join(bin, "go")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = run ] || { echo \"the transcripts' go runs the built programs only: $*\" >&2; exit 97; }\n" +
		"shift\n" +
		"name=$(echo \"${1#./}\" | tr / -)\n" +
		"shift\n" +
		"[ -x \"" + bin + "/prog-$name\" ] || { echo \"the transcripts' go did not build $name\" >&2; exit 97; }\n" +
		"exec \"" + bin + "/prog-$name\" \"$@\"\n"
	if err := os.WriteFile(goFunc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return append(cleanEnv(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// shellRun runs one command line with /bin/sh in dir. Stdin is /dev/null,
// as in a script: no prompt can appear.
func shellRun(t *testing.T, dir string, env []string, line string) cliResult {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running %q: %v", line, err)
		}
		code = ee.ExitCode()
	}
	return cliResult{stdout.String(), stderr.String(), code}
}
