// Command pdf0 is a small command-line front end to the pdf0 library:
// inspect, validate, decrypt, and encrypt PDF files.
//
// Exit codes: 0 — success (no violations found); 1 — the requested checks
// reported violations; 2 — usage error; 3 — operational error (I/O, parse,
// encryption).
package main

import (
	"bytes"
	"fmt"
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
	switch err.(type) {
	case nil:
		return 0
	case usageError:
		return 2
	case violationsError:
		return 1
	}
	return 3
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "info":
		err = cmdInfo(os.Args[2:])
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "decrypt":
		err = cmdDecrypt(os.Args[2:])
	case "encrypt":
		err = cmdEncrypt(os.Args[2:])
	case "extract":
		err = cmdExtract(os.Args[2:])
	case "repair":
		err = cmdRepair(os.Args[2:])
	case "merge":
		err = cmdMerge(os.Args[2:])
	case "ua":
		err = cmdUA(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pdf0 — inspect, validate, and (de)encrypt PDF files

usage:
  pdf0 info     [-password PW] <file>
  pdf0 validate [-level 1b|2b|3b|4] [-password PW] <file>
  pdf0 decrypt  [-password PW] <in> <out>
  pdf0 encrypt  -user PW [-owner PW] <in> <out>
  pdf0 extract  [-password PW] <file>
  pdf0 repair   [-level 1b|2b|3b|4] [-password PW] <in> <out>
  pdf0 merge    <out> <in1> <in2> [in3 ...]
  pdf0 ua       [-password PW] <file>

exit codes: 0 success, 1 violations reported, 2 usage error,
3 read/write, parse, or encryption error
`)
}

func readDoc(path, password string) (*pdf0.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if password != "" {
		return pdf0.ReadWithPassword(bytes.NewReader(data), int64(len(data)), password)
	}
	return pdf0.Read(bytes.NewReader(data), int64(len(data)))
}
