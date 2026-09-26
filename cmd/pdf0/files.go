package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// The CLI never destroys input by accident (audit 2026-09-22 C155). Every
// command that writes a file goes through checkOutput before doing any work
// and through writeOutput to write, which together guarantee:
//
//   - the output is never one of the inputs, compared by file identity
//     (os.SameFile: through symlinks and hard links, not by spelling), even
//     with -force;
//   - an existing output is replaced only with -force, and that check is
//     repeated atomically at write time (a hard link to the finished temp
//     file fails if the name appeared in the meantime);
//   - the output appears whole or not at all: the bytes go to a temp file in
//     the destination's directory, are synced, and are then renamed or linked
//     into place, so a failure never leaves a truncated file;
//   - output that holds decrypted content is created 0600.
//
// "-" means stdin as an input and stdout as an output.

// inputFile is one PDF input, read whole.
type inputFile struct {
	name string      // as given on the command line; "-" is stdin
	data []byte      // the file's bytes
	info os.FileInfo // for identity checks; nil when stdin is not a regular file
}

// readInput reads one input. "-" reads stdin, which only one input may do.
func readInput(name string, stdin *stdinClaim) (*inputFile, error) {
	if name == "-" {
		if err := stdin.claim("the input \"-\""); err != nil {
			return nil, err
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		in := &inputFile{name: "<stdin>", data: data}
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode().IsRegular() {
			in.info = fi
		}
		return in, nil
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	return &inputFile{name: name, data: data, info: fi}, nil
}

// checkOutput refuses an output that would destroy an input, or overwrite an
// existing file without -force. It runs before any work is done, so a refused
// command has no side effects.
func checkOutput(out string, force bool, inputs []*inputFile) error {
	if out == "-" {
		if isTerminalFD(os.Stdout.Fd()) {
			return usagef("refusing to write a PDF to a terminal; redirect stdout or name an output file")
		}
		fi, err := os.Stdout.Stat()
		if err == nil && fi.Mode().IsRegular() {
			if in := sameAsInput(fi, inputs); in != nil {
				// The shell truncated it before pdf0 started; nothing can
				// be saved, but say what happened instead of a parse error.
				return usagef("stdout is redirected to the input %s, which the shell has already truncated", in.name)
			}
		}
		return nil
	}
	lfi, err := os.Lstat(out)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi, err := os.Stat(out); err == nil {
		if in := sameAsInput(fi, inputs); in != nil {
			return usagef("output %s is the input %s; write to a different file", out, in.name)
		}
		if fi.IsDir() {
			return usagef("output %s is a directory", out)
		}
	}
	if !force {
		what := "already exists"
		if lfi.Mode()&fs.ModeSymlink != 0 {
			what = "already exists (as a symlink)"
		}
		return usagef("output %s %s; pass -force to replace it", out, what)
	}
	return nil
}

// sameAsInput returns the input that fi is the same file as, or nil.
func sameAsInput(fi os.FileInfo, inputs []*inputFile) *inputFile {
	for _, in := range inputs {
		if in.info != nil && os.SameFile(fi, in.info) {
			return in
		}
	}
	return nil
}

// Output permissions: ordinary output is created 0666 and trimmed by the umask,
// as any file a shell redirect creates; decrypted content is 0600 whatever the
// umask.
const (
	permOutput    fs.FileMode = 0o666
	permPlaintext fs.FileMode = 0o600
)

// writeOutput writes data to out ("-" is stdout) atomically, re-checking the
// overwrite rule at the moment the file is put in place.
func writeOutput(out string, force bool, data []byte, perm fs.FileMode) error {
	if out == "-" {
		_, err := stdout.Write(data)
		return err
	}
	// Write through a symlink to its target, as a shell redirect would,
	// rather than replacing the link itself with a regular file.
	target := out
	if resolved, err := filepath.EvalSymlinks(out); err == nil {
		target = resolved
	}
	tmp, err := createTemp(filepath.Dir(target), filepath.Base(target), perm)
	if err != nil {
		return err
	}
	name := tmp.Name()
	placed := false
	defer func() {
		if !placed {
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", out, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", out, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}

	if force {
		if err := os.Rename(name, target); err != nil {
			return fmt.Errorf("writing %s: %w", out, err)
		}
		placed = true
		return nil
	}
	// Without -force, a hard link is the atomic no-clobber rename: it fails
	// if the name exists, however recently it appeared.
	err = os.Link(name, target)
	switch {
	case err == nil:
		os.Remove(name) // the output is complete under its own name now
		placed = true
		return nil
	case errors.Is(err, fs.ErrExist):
		return usagef("output %s already exists; pass -force to replace it", out)
	}
	// A file system without hard links: fall back to check-then-rename,
	// which leaves only a narrow window for a racing writer.
	if _, statErr := os.Lstat(target); statErr == nil {
		return usagef("output %s already exists; pass -force to replace it", out)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	placed = true
	return nil
}

// createTemp creates a new file next to the output with the given permission
// bits (subject to the umask), failing rather than reusing an existing name.
// os.CreateTemp is not used because it always creates 0600, which would make
// every ordinary output private.
func createTemp(dir, base string, perm fs.FileMode) (*os.File, error) {
	for range 16 {
		var rnd [8]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, "."+base+".pdf0-"+hex.EncodeToString(rnd[:])+".tmp")
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("creating the output in %s: %w", dir, err)
		}
		// O_CREATE's mode is trimmed by the umask but never widened, so
		// 0600 stays 0600. Chmod anyway for plaintext, in case the file
		// system ignores the create mode.
		if perm == permPlaintext {
			if err := f.Chmod(permPlaintext); err != nil {
				f.Close()
				os.Remove(name)
				return nil, err
			}
		}
		return f, nil
	}
	return nil, fmt.Errorf("creating the output in %s: could not find an unused temporary name", dir)
}
