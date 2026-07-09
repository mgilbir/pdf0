// Command pdf0 is a small command-line front end to the pdf0 library:
// inspect, validate, decrypt, and encrypt PDF files.
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/mgilbir/pdf0"
)

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
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `pdf0 — inspect, validate, and (de)encrypt PDF files

usage:
  pdf0 info    <file>
  pdf0 validate [-level 1b|2b|3b|4] <file>
  pdf0 decrypt [-password PW] <in> <out>
  pdf0 encrypt -user PW [-owner PW] <in> <out>
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
