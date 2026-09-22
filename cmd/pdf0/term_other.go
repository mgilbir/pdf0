//go:build !linux && !darwin

package main

import "errors"

// stdinTerminal never prompts on platforms where pdf0 has no no-echo
// implementation. A password is then given with a password-file flag or an
// environment variable, never by echoing it to the screen.
type stdinTerminal struct{}

func (stdinTerminal) IsTerminal() bool { return false }

// isTerminalFD is false where terminals cannot be detected, so a PDF written
// to "-" is not refused there even when stdout is a console.
func isTerminalFD(uintptr) bool { return false }

func (stdinTerminal) ReadPassword(string) (string, error) {
	return "", errors.New("password prompting is not supported on this platform")
}
