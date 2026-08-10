package render

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// Resolving the references a document makes to things outside itself.
//
// This is the first and only place where the engine can read anything the
// caller did not hand it, and it is written as a policy rather than as a
// convenience. §4.1 of the rendering proposal makes resolution the caller's job
// and forbids the engine a network; what is here is the shape that makes both
// enforceable rather than merely intended.
//
// # Deny by default
//
// An Input with no resolver loads nothing. There is no "well, it looks like a
// path, so read it" fallback, and there is no flag that turns one on: a caller
// who wants an <img> to draw something, or a <link rel=stylesheet> to style
// anything, must say where the bytes may come from. That is the difference
// between a template renderer and a file-disclosure primitive, because the
// documents this engine renders are untrusted — an invoice template from a
// customer, a report body from a form — and "src" and "href" are strings in one
// of them.
//
// # No network, at any level
//
// A reference with a scheme is refused here rather than passed to the resolver,
// so a caller cannot accidentally implement one by writing a resolver that hands
// the string to something else. An HTML-to-PDF engine that fetches URLs is a
// server-side request forgery primitive with a friendly interface: the attacker
// writes <img src="http://169.254.169.254/...">, the server fetches it, and the
// only question left is whether the response is visible in the PDF. This one
// does not fetch, and the refusal is reported so that a document relying on it
// is not silently blank.
//
// A "data:" reference is the exception and is not an exception to anything that
// matters: its bytes are in the document already, so honouring it reads nothing
// the caller did not supply. It is bounded by the same caps as everything else.

// ResourceResolver turns a reference written in a document into bytes.
//
// It is deliberately not an io.Reader factory or a URL fetcher. A resolver is
// handed the reference exactly as the document wrote it, with no scheme and no
// leading slash — those are refused before it is called — and returns the whole
// resource or an error. Returning an error is normal: a missing image is a
// finding, not a failure of the render.
//
// A resolver must bound what it returns. The engine caps what it will decode,
// but it cannot cap what a resolver allocates before returning, so a resolver
// reading from anywhere unbounded has to impose its own limit. DirResolver
// does.
type ResourceResolver interface {
	// Resolve returns the bytes of the resource a document referred to.
	Resolve(ref string) ([]byte, error)
}

// ErrNoResolver is what the engine reports when a document refers to something
// and the caller configured no resolver.
var ErrNoResolver = errors.New("no resource resolver is configured, so nothing outside the document can be loaded")

// maxResourceBytes is the largest resource DirResolver will read.
//
// It bounds the read itself rather than the decode: a file this size is
// allocated whole before anything looks at it, so a cap applied afterwards is a
// cap applied too late. Sixteen megabytes is several times the largest image
// anyone puts in a document and small enough that a directory full of them
// cannot exhaust a server.
//
// It is a variable rather than a constant so that a test can lower it far
// enough to watch it fire without writing sixteen megabytes to a disk. A bound
// that has only ever been observed not to trip is one nobody knows works.
var maxResourceBytes int64 = 16 << 20

// DirResolver serves files from one directory and from nowhere else.
//
// Containment is enforced by os.Root, which resolves every path component
// against the directory at the system-call level: a reference with "..", an
// absolute path, and a symbolic link pointing outside the directory are all
// refused by the kernel rather than by a string comparison this code performs.
// That distinction is the whole reason os.Root exists — a check on the name
// followed by an open is a race, and a check on the *resolved* name is one
// symlink away from being wrong.
//
// A resolver holds an open directory handle and should be closed when the
// caller is done with it.
type DirResolver struct {
	root *os.Root
	// max is the largest file this will read, defaulting to maxResourceBytes.
	max int64
}

// NewDirResolver opens dir as the only place a document may read from.
func NewDirResolver(dir string) (*DirResolver, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("render: rooting a resource resolver at %s: %w", dir, err)
	}
	return &DirResolver{root: root, max: maxResourceBytes}, nil
}

// WithMaxBytes returns the resolver with a different size cap. A cap of zero or
// less is refused rather than treated as "no limit": an unbounded resolver is
// the thing this type exists to prevent, and a zero value in a configuration
// struct must not switch it off.
func (d *DirResolver) WithMaxBytes(n int64) *DirResolver {
	if n <= 0 {
		return d
	}
	d.max = n
	return d
}

// Close releases the directory handle.
func (d *DirResolver) Close() error {
	if d == nil || d.root == nil {
		return nil
	}
	return d.root.Close()
}

// Resolve reads one file from the rooted directory.
func (d *DirResolver) Resolve(ref string) ([]byte, error) {
	if d == nil || d.root == nil {
		return nil, ErrNoResolver
	}
	name, err := resourcePath(ref)
	if err != nil {
		return nil, err
	}

	f, err := d.root.Open(name)
	if err != nil {
		// os.Root's error already says "path escapes from parent" for the
		// containment failures, which is the sentence a caller needs to see.
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		// A device, a socket or a named pipe. Reading /dev/zero through a
		// resolver rooted at /dev would produce bytes for ever, and reading a
		// fifo would block the render until something wrote to it — neither is
		// a file the cap below can save us from, because neither has a size.
		return nil, fmt.Errorf("render: %s is not a regular file", name)
	}
	max := d.max
	if max <= 0 {
		max = maxResourceBytes
	}
	if info.Size() > max {
		return nil, fmt.Errorf("render: %s is %d bytes, larger than the %d this engine will read",
			name, info.Size(), max)
	}

	// Read through a limit even though the size was checked. The two are not
	// the same guarantee: the size came from a stat and the file may grow
	// between the stat and the read, and on some filesystems a stat size is a
	// hint rather than a fact.
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("render: %s is larger than the %d bytes this engine will read", name, max)
	}
	return data, nil
}

// resourcePath turns a reference written in a document into a relative path,
// refusing everything that is not one.
//
// The refusals are the point, so each is named rather than folded into a single
// "invalid":
//
//   - A scheme means a URL. Nothing here fetches one, and reading "file:" as a
//     path would put the scheme back by another spelling.
//   - A leading slash is an absolute path, which is a reference to the
//     filesystem rather than to the document's own directory.
//   - A path component of ".." asks to leave the directory. os.Root refuses it
//     too; it is refused here as well so that the reason reported is the one
//     the author needs, and so the containment does not rest on one mechanism.
//
// The query and fragment are dropped, as a browser drops them when a URL
// resolves to a file. Per-cent escapes are decoded, because a document that
// writes "blue%2015.png" means a file with a space in it.
func resourcePath(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("render: the reference is empty")
	}
	if scheme, ok := schemeOf(ref); ok {
		return "", fmt.Errorf("render: %q names the %q scheme; this engine resolves no URLs", ref, scheme)
	}
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return "", errors.New("render: the reference has no path")
	}
	decoded, err := url.PathUnescape(ref)
	if err != nil {
		return "", fmt.Errorf("render: %q is not a readable reference: %w", ref, err)
	}
	ref = decoded

	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, `\`) {
		return "", fmt.Errorf("render: %q is an absolute path; a document may only refer to its own directory", ref)
	}
	// Both separators, because a document written on Windows uses the other one
	// and the check must not depend on which platform is reading it.
	for _, part := range strings.FieldsFunc(ref, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("render: %q leaves the directory it may read from", ref)
		}
	}
	return ref, nil
}

// schemeOf reports whether a reference begins with a URL scheme.
//
// The grammar is RFC 3986's: a letter followed by letters, digits, "+", "-" and
// ".", then a colon. A Windows drive letter — "c:/x" — matches it, and being
// refused as a scheme is the right outcome for that too.
func schemeOf(ref string) (string, bool) {
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			continue
		case c >= '0' && c <= '9', c == '+', c == '-', c == '.':
			if i == 0 {
				return "", false
			}
			continue
		case c == ':':
			if i == 0 {
				return "", false
			}
			return strings.ToLower(ref[:i]), true
		}
		return "", false
	}
	return "", false
}
