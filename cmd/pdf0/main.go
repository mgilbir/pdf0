// Command pdf0 is a small command-line front end to the pdf0 library:
// inspect, validate, repair, merge, decrypt, and encrypt PDF files.
//
// Exit codes: 0 — success (no violations found); 1 — the requested checks
// reported violations; 2 — usage error, including a refusal to overwrite a
// file; 3 — the run failed: an operational error (I/O, parse, encryption), or
// checks that reported nothing but checker findings, so conformance is unknown
// (findingsError).
//
// Passwords are never taken from argv; see secret.go. Outputs never replace an
// input and are written atomically; see files.go.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mgilbir/pdf0"
)

// usageError marks a command-line usage mistake (exit code 2).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// usagef builds a usageError.
func usagef(format string, a ...any) error {
	return usageError{msg: fmt.Sprintf(format, a...)}
}

// violationsError marks a run whose checks found violations (exit code 1) —
// distinct from an operational failure (audit C47).
type violationsError struct{ msg string }

func (e violationsError) Error() string { return e.msg }

// violationsf builds a violationsError.
func violationsf(format string, a ...any) error {
	return violationsError{msg: fmt.Sprintf(format, a...)}
}

// tally splits a validator's findings into real violations and checker
// findings (pdf0.IsCheckerFinding: the rules "limit" and "internal", a check
// that could not be completed).
func tally[V pdf0.Violation](findings []V) (violations, checker int) {
	for _, f := range findings {
		if pdf0.IsCheckerFinding(f) {
			checker++
		} else {
			violations++
		}
	}
	return violations, checker
}

// findingsError is the result of a validating command whose checks found
// violations and checker findings in the numbers given, and the one place the
// rule is written: no finding is success (exit 0); any violation is exit 1,
// whatever checker findings come with it, because the file is known to break
// a rule; checker findings alone are exit 3, the run failed, because the run
// could not establish conformance either way, and a script asking "is this
// file conformant?" must read neither yes nor "it breaks a rule" (docs audit
// 2026-10-11 D64). msg is the command's own words for the violations.
func findingsError(violations, checker int, msg string) error {
	switch {
	case violations > 0 && checker > 0:
		return violationsf("%s, and %d checker finding(s)", msg, checker)
	case violations > 0:
		return violationsError{msg: msg}
	case checker > 0:
		return fmt.Errorf("%d checker finding(s) and no violation: the checks could not be completed, so conformance is unknown", checker)
	}
	return nil
}

// exitCode maps a command's error to the process exit code.
func exitCode(err error) int {
	var u usageError
	var v violationsError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &u):
		return 2
	case errors.As(err, &v):
		return 1
	}
	return 3
}

// resultWriter is stdout for command results. It remembers the first write
// error, so a report that could not be written (a full disk behind a
// redirect) fails the run instead of exiting 0 with the output lost.
type resultWriter struct {
	w   io.Writer
	err error
}

func (r *resultWriter) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	n, err := r.w.Write(p)
	if err != nil {
		r.err = err
	}
	return n, err
}

// stdout is where every command writes its results and, for "-", its PDF.
var stdout = &resultWriter{w: os.Stdout}

var commands = map[string]func([]string) error{
	"info":     cmdInfo,
	"validate": cmdValidate,
	"decrypt":  cmdDecrypt,
	"encrypt":  cmdEncrypt,
	"extract":  cmdExtract,
	"repair":   cmdRepair,
	"merge":    cmdMerge,
	"ua":       cmdUA,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

// run executes one command line and returns the exit code.
func run(args []string) int {
	if len(args) < 1 {
		usage()
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		return 2
	}
	err := cmd(args[1:])
	if stdout.err != nil {
		// Whatever the command concluded, its result did not reach the
		// reader; that is the failure to report.
		err = fmt.Errorf("writing to stdout: %w", stdout.err)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	return exitCode(err)
}

// usage prints the top-level help. The -level names come from pdfa.Levels(),
// as parseLevel reads them: a hand-kept copy here once offered four of the
// eleven.
func usage() {
	fmt.Fprintf(os.Stderr, `pdf0 — inspect, validate, repair, merge, and (de)encrypt PDF files

usage:
  pdf0 info     [-password-file F] <file>
  pdf0 validate [-level LEVEL|declared] [-password-file F] <file>
  pdf0 decrypt  [-force] [-password-file F] <in> <out>
  pdf0 encrypt  [-force] [-user-password-file F] [-owner-password-file F] <in> <out>
  pdf0 extract  [-password-file F] <file>
  pdf0 repair   [-force] [-level LEVEL] [-password-file F] <in> <out>
  pdf0 merge    [-force] <out> <in1> <in2> [in3 ...]
  pdf0 ua       [-password-file F] <file>

LEVEL is a PDF/A level (default %s):
  %s
validate also takes "declared": the level the file's own metadata claims.

"-" is stdin as an input and stdout as an output. An existing output is
replaced only with -force, and never when it is one of the inputs.

passwords are never taken on the command line. Each comes from, in order:
its file flag ("-" reads stdin), the environment (PDF0_PASSWORD,
PDF0_USER_PASSWORD, PDF0_OWNER_PASSWORD), or a no-echo prompt when stdin
is a terminal.

exit codes: 0 success, 1 violations reported, 2 usage error or refused
overwrite, 3 read/write, parse, or encryption error, or checks that could
not be completed (only "limit" or "internal" findings)
`, defaultLevel, levelNames)
}

// parseDoc parses a PDF from memory, with a password when one is given.
func parseDoc(data []byte, password string) (*pdf0.Document, error) {
	if password != "" {
		return pdf0.ReadWithPassword(bytes.NewReader(data), int64(len(data)), password)
	}
	return pdf0.Read(bytes.NewReader(data), int64(len(data)))
}
