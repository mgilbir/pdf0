package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// Passwords never ride on the command line (audit 2026-09-22 C113). Anything
// in argv is readable by every user on the machine through ps and /proc, and
// lands in shell history. Each password a command takes has exactly three
// sources, tried in this order:
//
//  1. a file flag (-password-file, -user-password-file, -owner-password-file)
//     naming a file whose contents are the password, with one trailing newline
//     removed. "-" means stdin, when stdin is not already the PDF input.
//  2. an environment variable (PDF0_PASSWORD, PDF0_USER_PASSWORD,
//     PDF0_OWNER_PASSWORD). An empty variable counts as unset.
//  3. a prompt with echo off, only when stdin is a terminal and not the PDF
//     input. With stdin not a terminal there is no prompt: a script gets a
//     clear error instead of a hang.
//
// The old argv flags (-password, -user, -owner) are still recognised, but only
// so that using one is refused with a message naming the replacements; the
// value given is never stored or used.

// secretRole names one password a command takes and where it may come from.
type secretRole struct {
	what     string // "password", "user password", "owner password"
	fileFlag string // e.g. "password-file"
	env      string // e.g. "PDF0_PASSWORD"
	argvFlag string // the refused legacy flag, e.g. "password"
}

var (
	readPassword  = secretRole{"password", "password-file", "PDF0_PASSWORD", "password"}
	userPassword  = secretRole{"user password", "user-password-file", "PDF0_USER_PASSWORD", "user"}
	ownerPassword = secretRole{"owner password", "owner-password-file", "PDF0_OWNER_PASSWORD", "owner"}
)

// refusedFlag is a flag that exists only to be refused: it records that it was
// given and discards the value, so a password passed on argv is never kept in
// memory, printed or used.
type refusedFlag struct{ set bool }

func (f *refusedFlag) String() string { return "" }
func (f *refusedFlag) Set(string) error {
	f.set = true
	return nil
}

// secretFlags are the flags for one role, registered on a command's FlagSet.
type secretFlags struct {
	role secretRole
	file string
	argv refusedFlag
}

func addSecretFlags(fs *flag.FlagSet, role secretRole) *secretFlags {
	s := &secretFlags{role: role}
	fs.StringVar(&s.file, role.fileFlag, "", "read the "+role.what+" from `FILE` (\"-\" for stdin); or set "+role.env)
	fs.Var(&s.argv, role.argvFlag, "refused: a "+role.what+" on the command line is visible to other users; use -"+role.fileFlag+" or "+role.env)
	return s
}

// hint says how to supply this password, for error messages.
func (s *secretFlags) hint() string {
	return fmt.Sprintf("-%s FILE, %s, or run from a terminal to be prompted", s.role.fileFlag, s.role.env)
}

// resolve returns the password from its file flag or its environment variable.
// ok is false when neither supplied one; the caller then decides whether to
// prompt. stdin tracks whether stdin has already been claimed, by the PDF
// input or by another password.
func (s *secretFlags) resolve(stdin *stdinClaim) (pw string, ok bool, err error) {
	if s.argv.set {
		return "", false, usagef("-%s is no longer accepted: a %s on the command line is visible to every user through ps and is saved in shell history; use %s",
			s.role.argvFlag, s.role.what, s.hint())
	}
	if s.file != "" {
		pw, err := readPasswordFile(s.file, stdin, s.role)
		if err != nil {
			return "", false, err
		}
		return pw, true, nil
	}
	if v := os.Getenv(s.role.env); v != "" {
		if len(v) > maxPasswordLen {
			return "", false, fmt.Errorf("%s: %w", s.role.env, errPasswordTooLong)
		}
		return v, true, nil
	}
	return "", false, nil
}

// readPasswordFile reads a password file: its whole contents, minus one
// trailing "\n" or "\r\n" so that `echo secret > file` works. A password that
// itself ends in a newline cannot be expressed; none should.
func readPasswordFile(path string, stdin *stdinClaim, role secretRole) (string, error) {
	var r io.Reader
	if path == "-" {
		if err := stdin.claim("-" + role.fileFlag + " -"); err != nil {
			return "", err
		}
		// Reading "-" from a terminal would echo the password as it is
		// typed; the prompt exists for exactly that case.
		if promptTerminal.IsTerminal() {
			return "", usagef("-%s - reads stdin, which is a terminal and would echo the %s; omit -%s to be prompted instead",
				role.fileFlag, role.what, role.fileFlag)
		}
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("reading the %s: %w", role.what, err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxPasswordLen+3))
	if err != nil {
		return "", fmt.Errorf("reading the %s: %w", role.what, err)
	}
	s := string(data)
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	if len(s) > maxPasswordLen {
		return "", fmt.Errorf("reading the %s from %s: %w", role.what, path, errPasswordTooLong)
	}
	return s, nil
}

// stdinClaim records which part of the command line has taken stdin, so two
// things (a "-" input and "-password-file -", say) never both read it.
type stdinClaim struct{ by string }

func (c *stdinClaim) claim(by string) error {
	if c.by != "" {
		return usagef("stdin cannot be used for both %s and %s", c.by, by)
	}
	c.by = by
	return nil
}

func (c *stdinClaim) taken() bool { return c.by != "" }

// canPrompt reports whether an interactive prompt is possible: stdin is a
// terminal and nothing else on the command line reads it.
func canPrompt(stdin *stdinClaim) bool {
	return !stdin.taken() && promptTerminal.IsTerminal()
}
