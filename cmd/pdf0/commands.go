package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mgilbir/pdf0"
	"github.com/mgilbir/pdf0/pdfa"
)

// source is one command's PDF input together with how its password is
// obtained.
type source struct {
	in       *inputFile
	pw       *secretFlags
	stdin    *stdinClaim
	password string
	supplied bool // a password came from a file, the environment or a prompt
}

// open resolves the password flags and reads the input. It is split from
// parse so a command can check its output (checkOutput) after the input is
// known but before any prompt or work.
func open(name string, pw *secretFlags, stdin *stdinClaim) (*source, error) {
	s := &source{pw: pw, stdin: stdin}
	if pw != nil {
		p, ok, err := pw.resolve(stdin)
		if err != nil {
			return nil, err
		}
		s.password, s.supplied = p, ok
	}
	in, err := readInput(name, stdin)
	if err != nil {
		return nil, err
	}
	s.in = in
	return s, nil
}

// parse parses the input. When the file is locked, no password was supplied
// and prompt is set, it asks for one on the terminal (if there is one) and
// parses again.
func (s *source) parse(prompt bool) (*pdf0.Document, error) {
	doc, err := parseDoc(s.in.data, s.password)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.in.name, err)
	}
	if doc.Locked() && !s.supplied && prompt && s.pw != nil && canPrompt(s.stdin) {
		p, err := promptTerminal.ReadPassword(fmt.Sprintf("Password for %s: ", s.in.name))
		if err != nil {
			return nil, err
		}
		s.password, s.supplied = p, true
		if doc, err = parseDoc(s.in.data, p); err != nil {
			return nil, fmt.Errorf("%s: %w", s.in.name, err)
		}
	}
	return doc, nil
}

// parseUnlocked is parse for a command that needs the content: a document
// still locked afterwards is an error saying why.
func (s *source) parseUnlocked() (*pdf0.Document, error) {
	doc, err := s.parse(true)
	if err != nil {
		return nil, err
	}
	if doc.Locked() {
		return nil, lockedError(s.in.name, doc.LockReason(), s.supplied, s.pw)
	}
	return doc, nil
}

// lockedError explains why a locked document cannot be used, from the
// library's Document.LockReason. "No password was given" and "the password
// given is wrong" are different failures with different remedies, so they get
// different messages (audit 2026-09-22 C155); an unsupported or malformed
// /Encrypt is reported as such, since no password would help.
func lockedError(name string, reason error, supplied bool, pw *secretFlags) error {
	hint := "-password-file FILE, PDF0_PASSWORD, or run from a terminal to be prompted"
	if pw != nil {
		hint = pw.hint()
	}
	switch {
	case reason == nil:
		// Locked always has a reason; keep the message honest if not.
		return fmt.Errorf("could not decrypt %s: the document is locked for an unknown reason", name)
	case !errors.Is(reason, pdf0.ErrWrongPassword):
		return fmt.Errorf("could not decrypt %s: %w", name, reason)
	case !supplied:
		return fmt.Errorf("%s is encrypted and no password was supplied (give it with %s)", name, hint)
	}
	return fmt.Errorf("could not decrypt %s: the password is wrong (%w)", name, reason)
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return usagef("usage: pdf0 info [-password-file F] <file>")
	}
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	// info reports structure, which a locked file still has; it does not
	// prompt, but a supplied password is used and shows in "locked".
	doc, err := src.parse(false)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "version:   %s\n", doc.Version)
	fmt.Fprintf(stdout, "objects:   %d\n", len(doc.Objects))
	// The page tree, not a /Type /Page scan: orphan page objects outside the
	// tree must not inflate the count (audit C47).
	fmt.Fprintf(stdout, "pages:     %d\n", doc.PageCount())
	fmt.Fprintf(stdout, "encrypted: %v\n", doc.Encrypted)
	fmt.Fprintf(stdout, "locked:    %v\n", doc.Locked())
	return nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	level := fs.String("level", "2b", "PDF/A level: 1b, 2b, 3b, or 4")
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return usagef("usage: pdf0 validate [-level 1b|2b|3b|4] [-password-file F] <file>")
	}
	lvl, ok := parseLevel(*level)
	if !ok {
		return usagef("unknown level %q (want 1b, 2b, 3b, or 4)", *level)
	}
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	doc, err := src.parseUnlocked()
	if err != nil {
		return err
	}
	errs := pdf0.ValidatePDFABytes(doc, lvl, src.in.data)
	if len(errs) == 0 {
		fmt.Fprintf(stdout, "%s: no violations found for PDF/A-%s\n", src.in.name, *level)
		return nil
	}
	for _, e := range errs {
		fmt.Fprintln(stdout, e)
	}
	return violationsf("%d violation(s) found", len(errs))
}

func cmdDecrypt(args []string) error {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	force := fs.Bool("force", false, "replace <out> if it exists")
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 2 {
		return usagef("usage: pdf0 decrypt [-force] [-password-file F] <in> <out>")
	}
	out := fs.Arg(1)
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	if err := checkOutput(out, *force, []*inputFile{src.in}); err != nil {
		return err
	}
	doc, err := src.parse(true)
	if err != nil {
		return err
	}
	// Locked first: a trailer /Encrypt that Read could not use leaves the
	// document locked without marking it Encrypted.
	if doc.Locked() {
		return lockedError(src.in.name, doc.LockReason(), src.supplied, pw)
	}
	if !doc.Encrypted {
		return fmt.Errorf("%s is not encrypted", src.in.name)
	}
	doc.RemoveEncryption()
	return writeDoc(doc, out, *force, permPlaintext)
}

func cmdEncrypt(args []string) error {
	fs := flag.NewFlagSet("encrypt", flag.ExitOnError)
	force := fs.Bool("force", false, "replace <out> if it exists")
	user := addSecretFlags(fs, userPassword)
	owner := addSecretFlags(fs, ownerPassword)
	fs.Parse(args)
	if fs.NArg() != 2 {
		return usagef("usage: pdf0 encrypt [-force] [-user-password-file F] [-owner-password-file F] <in> <out>")
	}
	out := fs.Arg(1)
	stdin := &stdinClaim{}
	userPw, haveUser, err := user.resolve(stdin)
	if err != nil {
		return err
	}
	ownerPw, haveOwner, err := owner.resolve(stdin)
	if err != nil {
		return err
	}
	src, err := open(fs.Arg(0), nil, stdin)
	if err != nil {
		return err
	}
	if err := checkOutput(out, *force, []*inputFile{src.in}); err != nil {
		return err
	}
	doc, err := src.parse(false)
	if err != nil {
		return err
	}
	if doc.Encrypted {
		return fmt.Errorf("%s is already encrypted; decrypt it first", src.in.name)
	}
	if !haveUser {
		if !canPrompt(stdin) {
			return usagef("encrypt needs a user password: give it with %s", user.hint())
		}
		if userPw, err = promptTerminal.ReadPassword("User password for " + out + ": "); err != nil {
			return err
		}
		confirm, err := promptTerminal.ReadPassword("Repeat the user password: ")
		if err != nil {
			return err
		}
		if confirm != userPw {
			return usagef("the passwords did not match; nothing was written")
		}
	}
	// An empty user password opens the file for anyone: encrypted, but not
	// protected.
	if userPw == "" {
		return usagef("the user password must not be empty")
	}
	if !haveOwner {
		ownerPw = userPw
	}
	if err := doc.SetEncryption(userPw, ownerPw); err != nil {
		return err
	}
	return writeDoc(doc, out, *force, permOutput)
}

func cmdExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return usagef("usage: pdf0 extract [-password-file F] <file>")
	}
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	doc, err := src.parseUnlocked()
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, doc.ExtractText())
	return err
}

func cmdRepair(args []string) error {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	force := fs.Bool("force", false, "replace <out> if it exists")
	level := fs.String("level", "2b", "target PDF/A level: 1b, 2b, 3b, or 4")
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 2 {
		return usagef("usage: pdf0 repair [-force] [-level 1b|2b|3b|4] [-password-file F] <in> <out>")
	}
	lvl, ok := parseLevel(*level)
	if !ok {
		return usagef("unknown level %q (want 1b, 2b, 3b, or 4)", *level)
	}
	out := fs.Arg(1)
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	if err := checkOutput(out, *force, []*inputFile{src.in}); err != nil {
		return err
	}
	// A locked file cannot be repaired: its content is ciphertext, and
	// writing it back would report "0 fixes" for work never attempted.
	doc, err := src.parseUnlocked()
	if err != nil {
		return err
	}
	// Repair removes encryption, so an encrypted input's content leaves in
	// the clear.
	perm := permOutput
	if doc.Encrypted {
		perm = permPlaintext
	}
	// The report goes to stderr when the PDF itself is going to stdout.
	var report io.Writer = stdout
	if out == "-" {
		report = os.Stderr
	}
	actions := doc.Repair(lvl)
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return err
	}
	// Re-read and re-validate the bytes about to be written. A repaired file
	// pdf0 cannot read back is broken, not clean: it is an error and nothing
	// is written.
	rt, err := readBack(buf.Bytes(), "")
	if err != nil {
		return fmt.Errorf("the repaired document does not read back (nothing written): %w", err)
	}
	remaining := len(pdf0.ValidatePDFABytes(rt, lvl, buf.Bytes()))
	if err := writeOutput(out, *force, buf.Bytes(), perm); err != nil {
		return err
	}
	// Repair only removes forbidden document-level constructs; always say what
	// was done and what still fails, instead of exiting silently when nothing
	// was fixable (audit C47).
	for _, a := range actions {
		fmt.Fprintln(report, "fixed:", a.Description)
	}
	name := out
	if out == "-" {
		name = "<stdout>"
	}
	fmt.Fprintf(report, "%s: %d fix(es) applied, %d violation(s) remain\n", name, len(actions), remaining)
	if remaining > 0 {
		if out == "-" {
			return violationsf("%d violation(s) remain after repair", remaining)
		}
		return violationsf("%d violation(s) remain after repair (run: pdf0 validate -level %s %s)", remaining, *level, out)
	}
	return nil
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	force := fs.Bool("force", false, "replace <out> if it exists")
	fs.Parse(args)
	// Two inputs at least: with one, "merge a.pdf b.pdf" read as "merge a and
	// b" silently replaced a.pdf with a copy of b.pdf (audit 2026-09-22 C155).
	if fs.NArg() < 3 {
		return usagef("usage: pdf0 merge [-force] <out> <in1> <in2> [in3 ...]")
	}
	out := fs.Arg(0)
	stdin := &stdinClaim{}
	var inputs []*inputFile
	for _, name := range fs.Args()[1:] {
		in, err := readInput(name, stdin)
		if err != nil {
			return err
		}
		inputs = append(inputs, in)
	}
	if err := checkOutput(out, *force, inputs); err != nil {
		return err
	}
	var merged *pdf0.Document
	for _, in := range inputs {
		doc, err := parseDoc(in.data, "")
		if err != nil {
			return fmt.Errorf("%s: %w", in.name, err)
		}
		// Copying an encrypted source's ciphertext streams into the plaintext
		// merged document would corrupt it; require decrypted inputs.
		if doc.Encrypted {
			return fmt.Errorf("%s is encrypted; decrypt it before merging", in.name)
		}
		if merged == nil {
			merged = doc
			continue
		}
		merged.AppendPages(doc)
	}
	return writeDoc(merged, out, *force, permOutput)
}

func cmdUA(args []string) error {
	fs := flag.NewFlagSet("ua", flag.ExitOnError)
	pw := addSecretFlags(fs, readPassword)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return usagef("usage: pdf0 ua [-password-file F] <file>")
	}
	src, err := open(fs.Arg(0), pw, &stdinClaim{})
	if err != nil {
		return err
	}
	doc, err := src.parseUnlocked()
	if err != nil {
		return err
	}
	v := pdf0.ValidatePDFUA(doc)
	if len(v) == 0 {
		fmt.Fprintf(stdout, "%s: no PDF/UA violations found (foundational checks)\n", src.in.name)
		return nil
	}
	for _, e := range v {
		fmt.Fprintln(stdout, e)
	}
	return violationsf("%d PDF/UA violation(s)", len(v))
}

// readBack is how repair re-reads its own output: parseDoc, a variable only so
// a test can make the read-back fail.
var readBack = parseDoc

func parseLevel(s string) (pdfa.Level, bool) {
	switch s {
	case "1b":
		return pdfa.PDFA1b, true
	case "2b":
		return pdfa.PDFA2b, true
	case "3b":
		return pdfa.PDFA3b, true
	case "4":
		return pdfa.PDFA4, true
	}
	return 0, false
}

// writeDoc serialises doc in full, then writes it with writeOutput: a
// serialisation failure leaves no output at all.
func writeDoc(doc *pdf0.Document, out string, force bool, perm os.FileMode) error {
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return err
	}
	return writeOutput(out, force, buf.Bytes(), perm)
}
