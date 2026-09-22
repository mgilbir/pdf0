// Command pdf0 is a small command-line front end to the pdf0 library:
// inspect, validate, decrypt, and encrypt PDF files.
//
// Exit codes: 0 — success (no violations found); 1 — the requested checks
// reported violations; 2 — usage error, including a refusal to overwrite a
// file; 3 — operational error (I/O, parse, encryption).
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

func usage() {
	fmt.Fprint(os.Stderr, `pdf0 — inspect, validate, and (de)encrypt PDF files

usage:
  pdf0 info     [-password-file F] <file>
  pdf0 validate [-level 1b|2b|3b|4] [-password-file F] <file>
  pdf0 decrypt  [-force] [-password-file F] <in> <out>
  pdf0 encrypt  [-force] [-user-password-file F] [-owner-password-file F] <in> <out>
  pdf0 extract  [-password-file F] <file>
  pdf0 repair   [-force] [-level 1b|2b|3b|4] [-password-file F] <in> <out>
  pdf0 merge    [-force] <out> <in1> <in2> [in3 ...]
  pdf0 ua       [-password-file F] <file>

"-" is stdin as an input and stdout as an output. An existing output is
replaced only with -force, and never when it is one of the inputs.

passwords are never taken on the command line. Each comes from, in order:
its file flag ("-" reads stdin), the environment (PDF0_PASSWORD,
PDF0_USER_PASSWORD, PDF0_OWNER_PASSWORD), or a no-echo prompt when stdin
is a terminal.

exit codes: 0 success, 1 violations reported, 2 usage error or refused
overwrite, 3 read/write, parse, or encryption error
`)
}

// parseDoc parses a PDF from memory, with a password when one is given.
func parseDoc(data []byte, password string) (*pdf0.Document, error) {
	if password != "" {
		return pdf0.ReadWithPassword(bytes.NewReader(data), int64(len(data)), password)
	}
	return pdf0.Read(bytes.NewReader(data), int64(len(data)))
}
