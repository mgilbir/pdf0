//go:build !linux && !darwin

package main

import "os"

// appends cannot tell where the file status flags are not read; the caller
// then says only what it knows.
func appends(*os.File) bool { return false }
