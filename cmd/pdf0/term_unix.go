//go:build linux || darwin

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// stdinTerminal prompts on the process's stdin when it is a terminal.
//
// Echo is switched off with termios directly (TCGETS/TCSETS on Linux,
// TIOCGETA/TIOCSETA on Darwin) rather than through golang.org/x/term: the
// module depends on nothing but its author's own modules, and this is the
// whole of what that package would be used for.
type stdinTerminal struct{}

func getTermios(fd uintptr) (syscall.Termios, error) {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t))); errno != 0 {
		return t, errno
	}
	return t, nil
}

func setTermios(fd uintptr, t *syscall.Termios) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSetTermios, uintptr(unsafe.Pointer(t))); errno != 0 {
		return errno
	}
	return nil
}

// isTerminalFD reports whether fd is a terminal: the termios query succeeds
// only on a tty.
func isTerminalFD(fd uintptr) bool {
	_, err := getTermios(fd)
	return err == nil
}

// IsTerminal reports whether stdin is a terminal.
func (stdinTerminal) IsTerminal() bool { return isTerminalFD(os.Stdin.Fd()) }

// ReadPassword reads one line from stdin with echo off, restoring the
// terminal's settings afterwards — including when the read is interrupted by
// Ctrl-C, SIGTERM or SIGHUP, which would otherwise leave the user's shell with
// echo switched off.
func (stdinTerminal) ReadPassword(prompt string) (string, error) {
	fd := os.Stdin.Fd()
	old, err := getTermios(fd)
	if err != nil {
		return "", fmt.Errorf("stdin is not a terminal: %w", err)
	}
	quiet := old
	quiet.Lflag &^= syscall.ECHO
	// Keep line editing and signals: the user can still erase a typo and
	// interrupt the prompt. ECHONL echoes the final newline so the cursor
	// moves on.
	quiet.Lflag |= syscall.ICANON | syscall.ISIG | syscall.ECHONL
	quiet.Iflag |= syscall.ICRNL

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	defer func() {
		signal.Stop(sigs)
		close(done)
	}()
	go func() {
		select {
		case s := <-sigs:
			_ = setTermios(fd, &old) // best effort: the process is exiting
			fmt.Fprintln(os.Stderr)
			code := 130
			if sig, ok := s.(syscall.Signal); ok {
				code = 128 + int(sig)
			}
			os.Exit(code)
		case <-done:
		}
	}()

	// Echo goes off before the prompt appears, so nothing typed in answer to
	// the prompt can be echoed.
	if err := setTermios(fd, &quiet); err != nil {
		return "", fmt.Errorf("switching terminal echo off: %w", err)
	}
	fmt.Fprint(os.Stderr, prompt)
	line, readErr := readLine(os.Stdin)
	if err := setTermios(fd, &old); err != nil && readErr == nil {
		readErr = fmt.Errorf("restoring the terminal: %w", err)
	}
	return line, readErr
}
