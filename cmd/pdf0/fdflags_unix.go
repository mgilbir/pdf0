//go:build linux || darwin

package main

import (
	"os"
	"syscall"
)

// appends reports whether f was opened for appending: a shell's `>>`, which
// leaves the file's content where it was and puts every write after it.
func appends(f *os.File) bool {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFL, 0)
	return errno == 0 && flags&syscall.O_APPEND != 0
}
