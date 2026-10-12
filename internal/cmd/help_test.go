package cmd

// Every developer tool under internal/cmd answers -h with its usage and exit
// status 0, and does nothing else. corpusprobe and corpustime used to take -h
// for the directory or file to work on, and genicc and gensaslprep ignored it
// and regenerated their files — gensaslprep after fetching the RFC (docs audit
// 2026-10-11 D68). The tools carry the devtools tag, so they are built with it
// and executed; this file carries no tag so that a plain `go test ./...` runs
// it, and it finds the tools by listing the directory, so a new one is held to
// the same.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEveryToolAnswersHelp(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n++
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			exe := filepath.Join(bin, name)
			if out, err := exec.Command("go", "build", "-tags", "devtools", "-o", exe, "./"+name).CombinedOutput(); err != nil {
				t.Fatalf("building %s: %v\n%s", name, err, out)
			}
			for _, h := range []string{"-h", "-help", "--help"} {
				// In an empty directory, so a tool that took -h for an
				// instruction to run would find nothing to read and leave
				// what it wrote where the test can see it.
				dir := t.TempDir()
				cmd := exec.Command(exe, h)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir())
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Errorf("%s %s: %v, want exit 0\n%s", name, h, err, out)
				}
				if !strings.Contains(string(out), "usage: "+name) {
					t.Errorf("%s %s does not print its usage line (\"usage: %s …\"):\n%s", name, h, name, out)
				}
				if left, _ := os.ReadDir(dir); len(left) != 0 {
					t.Errorf("%s %s wrote %d entries into its working directory", name, h, len(left))
				}
			}
		})
	}
	if n == 0 {
		t.Fatal("found no tools under internal/cmd")
	}
}
