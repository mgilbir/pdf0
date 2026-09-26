// Package testfiles finds the test data a test reads, and refuses to let a
// missing or empty data set pass as a green run.
//
// Two kinds of data are involved, and they fail differently:
//
//   - Committed data (the vendored XMP RelaxNG schemas, the spec-example JSON,
//     the shaping corpus) is part of the repository. If a test cannot find it,
//     the test is looking in the wrong place or the checkout is broken, never
//     "the developer did not fetch it". Committed fails the test.
//   - Fetched data (the veraPDF corpus, the Arlington model, the codec samples,
//     the hand-placed Cal Poly suite) is large, gitignored and optional. A
//     developer without it must still be able to run the suite, so absent is
//     a skip. But a skip reads as a pass, so everything short of absent is a
//     failure: configured through the environment and unusable, fetched but
//     without the completion stamp the Makefile writes, or present and matching
//     zero files.
//
// Paths are resolved against the module root, not the working directory, so a
// test in a subpackage reads the same data as one at the root. (A test in pdfa/
// that named "testdata/xmp-rng" looked in pdfa/testdata, found nothing, and
// skipped on every run since the package split: audit 2026-09-22 C103.)
//
// Walks start from the data set's directory with symlinks resolved.
// filepath.Walk does not follow a symlinked root, so a corpus linked into a
// worktree used to walk zero files and every ratchet over it passed.
package testfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

const modulePath = "github.com/mgilbir/pdf0"

var (
	rootOnce sync.Once
	rootDir  string
	rootErr  error
)

// Root returns the module root: the nearest directory at or above the working
// directory whose go.mod declares this module.
func Root(t testing.TB) string {
	t.Helper()
	rootOnce.Do(func() { rootDir, rootErr = findRoot() })
	if rootErr != nil {
		t.Fatalf("testfiles: %v", rootErr)
	}
	return rootDir
}

func findRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for line := range strings.Lines(string(b)) {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "module" && f[1] == modulePath {
					return dir, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod for %s at or above %s", modulePath, wd)
		}
		dir = parent
	}
}

// Committed returns the path of data committed to the repository at rel, a
// slash-separated path relative to the module root. It fails the test if the
// path does not exist.
func Committed(t testing.TB, rel string) string {
	t.Helper()
	p := filepath.Join(Root(t), filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("committed test data %s is missing (%v).\n"+
			"It is part of the repository, so this is a broken checkout or a wrong path,\n"+
			"not an optional download; skipping would pass having checked nothing.", rel, err)
	}
	return p
}

// CommittedGlob returns the committed files matching pattern (slash-separated,
// relative to the module root, filepath.Match syntax), sorted. Zero matches
// fail the test.
func CommittedGlob(t testing.TB, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(Root(t), filepath.FromSlash(pattern)))
	if err != nil {
		t.Fatalf("testfiles: bad pattern %q: %v", pattern, err)
	}
	if len(m) == 0 {
		t.Fatalf("no committed test data matches %s.\n"+
			"It is part of the repository, so this is a broken checkout or a wrong path;\n"+
			"skipping would pass having checked nothing.", pattern)
	}
	slices.Sort(m)
	return m
}

// Dataset is test data that is not committed: fetched by a make target, or
// placed by hand where it cannot be redistributed.
type Dataset struct {
	// Name is how messages refer to it.
	Name string
	// Dir is its default location, slash-separated, relative to the module
	// root (or absolute).
	Dir string
	// Env, if set, names an environment variable that overrides Dir. Setting
	// it means "I intend this to run": anything unusable there fails.
	Env string
	// Stamp is the file, relative to Dir, that the fetch writes last, so that
	// a fetch that was interrupted is not mistaken for a complete one. Empty
	// for data placed by hand, where the directory's presence is the signal.
	Stamp string
	// Sub, if set, is where the data sits inside the fetched checkout (the
	// stamp stays at the checkout's top). Env may name either the checkout or
	// the Sub directory inside it.
	Sub string
	// Fetch is the command that fetches it, or a note on where to place it.
	Fetch string
}

// Path returns the data set's directory (its Sub directory, if it has one)
// with symlinks resolved.
//
// Unconfigured and absent skips: no Env override, and nothing at the default
// location, or a default location without the completion stamp (a fetch that
// was interrupted, which the skip message says). Configured and unusable
// fails: an Env override that does not resolve, or that names a checkout
// without its stamp.
func (d Dataset) Path(t testing.TB) string {
	t.Helper()
	dir, skip := d.resolve(t)
	if skip != "" {
		t.Skip(skip)
	}
	return dir
}

// Lookup is Path for a test that can use this data set but does not need it:
// where Path would skip, Lookup returns false and the reason, and the test
// carries on without it. Configured-and-unusable still fails.
func (d Dataset) Lookup(t testing.TB) (dir string, ok bool, reason string) {
	t.Helper()
	dir, skip := d.resolve(t)
	return dir, skip == "", skip
}

// resolve returns the directory, or the reason to skip; it fails t itself for
// everything that must not skip.
func (d Dataset) resolve(t testing.TB) (dir, skip string) {
	t.Helper()
	configured := ""
	if d.Env != "" {
		configured = os.Getenv(d.Env)
	}
	base := configured
	if base == "" {
		base = filepath.FromSlash(d.Dir)
		if !filepath.IsAbs(base) {
			base = filepath.Join(Root(t), base)
		}
	}
	checkout, data := d.locate(base)
	if data == "" {
		if configured == "" {
			return "", fmt.Sprintf("%s not present at %s; %s", d.Name, d.Dir, d.Fetch)
		}
		t.Fatalf("%s is set to %q and there is no %s there.\n"+
			"Failing rather than skipping: a skip would report success having checked nothing.",
			d.Env, configured, d.Name)
	}
	if d.Stamp != "" {
		if _, err := os.Stat(filepath.Join(checkout, d.Stamp)); err != nil {
			msg := fmt.Sprintf("%s at %s has no %s stamp: the fetch did not complete (or the data was placed by hand); %s",
				d.Name, checkout, d.Stamp, d.Fetch)
			if configured == "" {
				return "", msg
			}
			t.Fatalf("%s is set, but %s", d.Env, msg)
		}
	}
	resolved, err := filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatalf("%s: %v", d.Name, err)
	}
	return resolved, ""
}

// locate returns the checkout directory and the data directory for base, or
// "" for data when neither base nor base/Sub is a directory.
func (d Dataset) locate(base string) (checkout, data string) {
	isDir := func(p string) bool {
		fi, err := os.Stat(p) // follows symlinks
		return err == nil && fi.IsDir()
	}
	if d.Sub == "" {
		if isDir(base) {
			return base, base
		}
		return "", ""
	}
	sub := filepath.FromSlash(d.Sub)
	// base names the checkout ...
	if isDir(filepath.Join(base, sub)) {
		return base, filepath.Join(base, sub)
	}
	// ... or the data directory inside it.
	if isDir(base) && strings.HasSuffix(filepath.Clean(base), string(filepath.Separator)+sub) {
		return strings.TrimSuffix(filepath.Clean(base), string(filepath.Separator)+sub), base
	}
	return "", ""
}

// Files walks the data set's directory (or rel beneath it) and returns the
// regular files for which match returns true, as sorted absolute paths. A nil
// match takes every file. Zero files fail the test: the data set is present,
// so an empty result means it is broken or the test is looking in the wrong
// place, and either way the test would check nothing.
func (d Dataset) Files(t testing.TB, rel string, match func(path string) bool) []string {
	t.Helper()
	root := filepath.Join(d.Path(t), filepath.FromSlash(rel))
	var out []string
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type().IsRegular() && (match == nil || match(p)) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: walking %s: %v", d.Name, root, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s is present at %s, but no file under %q matched.\n"+
			"Failing rather than skipping: the data set is broken or has moved,\n"+
			"and a pass here would mean nothing was checked.", d.Name, d.Path(t), rel)
	}
	slices.Sort(out)
	return out
}

// PDFs is Files for every .pdf (any case) under rel.
func (d Dataset) PDFs(t testing.TB, rel string) []string {
	t.Helper()
	return d.Files(t, rel, IsPDF)
}

// Glob returns the files in the data set's directory matching pattern
// (slash-separated, relative to the directory, filepath.Match syntax), sorted.
// Zero matches fail the test, as for Files.
func (d Dataset) Glob(t testing.TB, pattern string) []string {
	t.Helper()
	dir := d.Path(t)
	m, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
	if err != nil {
		t.Fatalf("%s: bad pattern %q: %v", d.Name, pattern, err)
	}
	if len(m) == 0 {
		t.Fatalf("%s is present at %s, but nothing matches %q.\n"+
			"Failing rather than skipping: the data set is broken or has moved,\n"+
			"and a pass here would mean nothing was checked.", d.Name, dir, pattern)
	}
	slices.Sort(m)
	return m
}

// File returns the path of one named file inside the data set, failing the
// test if the data set is present but the file is not: that is a layout
// change, not a missing download.
func (d Dataset) File(t testing.TB, rel string) string {
	t.Helper()
	p := filepath.Join(d.Path(t), filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s is present, but has no %s (%v).\n"+
			"This is a layout change rather than a missing download;\n"+
			"skipping would report success having checked nothing.", d.Name, rel, err)
	}
	return p
}

// IsPDF reports whether path names a .pdf file, in any case.
func IsPDF(path string) bool { return strings.EqualFold(filepath.Ext(path), ".pdf") }
