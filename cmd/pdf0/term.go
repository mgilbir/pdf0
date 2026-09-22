package main

import (
	"errors"
	"fmt"
	"io"
)

// terminal is where an interactive password is read from. The real one is the
// process's stdin (term_unix.go); tests substitute a fake so the prompting
// logic can be exercised without a pseudo-terminal.
type terminal interface {
	// IsTerminal reports whether prompting is possible at all: stdin is an
	// interactive terminal whose echo can be switched off.
	IsTerminal() bool
	// ReadPassword writes prompt to stderr and reads one line from the
	// terminal with echo off. The returned line has its terminator removed.
	ReadPassword(prompt string) (string, error)
}

// promptTerminal is the terminal the commands prompt on. It is a variable only
// so that tests can replace it.
var promptTerminal terminal = stdinTerminal{}

// maxPasswordLen bounds a password read from a terminal or a file. ISO 32000-2
// uses at most the first 127 bytes of a password (32 for revisions 2–4), so
// nothing legitimate comes close; the bound only stops a mistaken
// `-password-file /dev/zero` or a huge file from being read into memory.
const maxPasswordLen = 4096

var errPasswordTooLong = fmt.Errorf("password is longer than %d bytes", maxPasswordLen)

// readLine reads bytes from r up to the first '\n' (or EOF), one byte at a
// time so nothing past the line is consumed, and strips a trailing "\r".
func readLine(r io.Reader) (string, error) {
	var line []byte
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			if len(line) >= maxPasswordLen {
				return "", errPasswordTooLong
			}
			line = append(line, b[0])
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(line) == 0 {
					return "", errors.New("no password entered (end of input)")
				}
				break
			}
			return "", err
		}
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return string(line), nil
}
