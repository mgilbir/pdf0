package testfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// recorder is a testing.TB that records the first Fatal or Skip instead of
// acting on it, so the failure paths themselves can be asserted. Embedding the
// interface supplies the unexported method; everything the package calls is
// overridden.
type recorder struct {
	testing.TB
	verdict string // "fatal", "skip" or ""
	msg     string
}

func (r *recorder) Helper() {}
func (r *recorder) stop(v string, msg string) {
	r.verdict, r.msg = v, msg
	runtime.Goexit()
}
func (r *recorder) Fatal(args ...any)                 { r.stop("fatal", strings.TrimSpace(fmt.Sprint(args...))) }
func (r *recorder) Fatalf(format string, args ...any) { r.stop("fatal", fmt.Sprintf(format, args...)) }
func (r *recorder) Skip(args ...any)                  { r.stop("skip", strings.TrimSpace(fmt.Sprint(args...))) }
func (r *recorder) Skipf(format string, args ...any)  { r.stop("skip", fmt.Sprintf(format, args...)) }

// observe runs fn on a recorder in its own goroutine (Fatal and Skip end it
// with runtime.Goexit, as the real ones do) and returns the verdict.
func observe(t *testing.T, fn func(tb testing.TB)) (verdict, msg string) {
	t.Helper()
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r.verdict, r.msg
}

func mkfile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, what, gotV, gotMsg, want string) {
	t.Helper()
	if gotV != want {
		t.Errorf("%s: verdict %q (%s), want %q", what, gotV, gotMsg, want)
	}
}

func TestCommitted(t *testing.T) {
	if p := Committed(t, "go.mod"); !strings.HasSuffix(p, "go.mod") {
		t.Errorf("Committed(go.mod) = %q", p)
	}
	// From a subpackage's working directory (this one), the path is resolved
	// against the module root: the C103 mistake cannot recur through here.
	if p := Committed(t, "testdata/xmp-rng/NOTICE.md"); !strings.HasPrefix(p, Root(t)) {
		t.Errorf("Committed resolved to %q, outside the module root %q", p, Root(t))
	}
	v, msg := observe(t, func(tb testing.TB) { Committed(tb, "testdata/no-such-committed-file") })
	expect(t, "missing committed file", v, msg, "fatal")
	v, msg = observe(t, func(tb testing.TB) { CommittedGlob(tb, "testdata/xmp-rng/*.nope") })
	expect(t, "committed glob matching nothing", v, msg, "fatal")
	if got := CommittedGlob(t, "testdata/xmp-rng/XMP_Properties-*.rng"); len(got) == 0 {
		t.Error("CommittedGlob found no RNG schemas")
	}
}

func TestDatasetResolution(t *testing.T) {
	tmp := t.TempDir()
	const env = "PDF0_TESTFILES_SELFTEST"

	absent := Dataset{Name: "absent", Dir: filepath.Join(tmp, "absent"), Env: env, Stamp: ".ok", Fetch: "make absent"}
	t.Run("absent and unconfigured skips", func(t *testing.T) {
		t.Setenv(env, "")
		v, msg := observe(t, func(tb testing.TB) { absent.Path(tb) })
		expect(t, "absent", v, msg, "skip")
	})
	t.Run("Lookup reports absence instead of skipping", func(t *testing.T) {
		t.Setenv(env, "")
		var ok bool
		var why string
		v, msg := observe(t, func(tb testing.TB) { _, ok, why = absent.Lookup(tb) })
		expect(t, "Lookup absent", v, msg, "")
		if ok || !strings.Contains(why, "make absent") {
			t.Errorf("Lookup = ok %v, reason %q", ok, why)
		}
		t.Setenv(env, filepath.Join(tmp, "nowhere"))
		v, msg = observe(t, func(tb testing.TB) { absent.Lookup(tb) })
		expect(t, "Lookup configured absent", v, msg, "fatal")
	})
	t.Run("configured and absent fails", func(t *testing.T) {
		t.Setenv(env, filepath.Join(tmp, "nowhere"))
		v, msg := observe(t, func(tb testing.TB) { absent.Path(tb) })
		expect(t, "configured absent", v, msg, "fatal")
	})

	// A directory an interrupted fetch left behind: only .git, no stamp.
	partial := filepath.Join(tmp, "partial")
	mkfile(t, filepath.Join(partial, ".git", "HEAD"))
	ds := Dataset{Name: "partial", Dir: partial, Env: env, Stamp: ".ok", Fetch: "make partial"}
	t.Run("unstamped default location skips", func(t *testing.T) {
		t.Setenv(env, "")
		v, msg := observe(t, func(tb testing.TB) { ds.Path(tb) })
		expect(t, "unstamped", v, msg, "skip")
		if !strings.Contains(msg, "did not complete") {
			t.Errorf("skip message does not say the fetch was incomplete: %s", msg)
		}
	})
	t.Run("configured and unstamped fails", func(t *testing.T) {
		t.Setenv(env, partial)
		v, msg := observe(t, func(tb testing.TB) { ds.Path(tb) })
		expect(t, "configured unstamped", v, msg, "fatal")
	})

	// Stamped, but no data: the C104 scenario. Configured or not, a walk
	// that finds nothing fails.
	empty := filepath.Join(tmp, "empty")
	mkfile(t, filepath.Join(empty, ".ok"))
	mkfile(t, filepath.Join(empty, ".git", "HEAD"))
	eds := Dataset{Name: "empty", Dir: empty, Env: env, Stamp: ".ok"}
	for _, configured := range []bool{false, true} {
		t.Run("stamped and empty fails", func(t *testing.T) {
			if configured {
				t.Setenv(env, empty)
			} else {
				t.Setenv(env, "")
			}
			v, msg := observe(t, func(tb testing.TB) { eds.PDFs(tb, "") })
			expect(t, "empty PDFs", v, msg, "fatal")
			v, msg = observe(t, func(tb testing.TB) { eds.Glob(tb, "*.pdf") })
			expect(t, "empty Glob", v, msg, "fatal")
			v, msg = observe(t, func(tb testing.TB) { eds.File(tb, "a.pdf") })
			expect(t, "missing File", v, msg, "fatal")
		})
	}

	// A complete data set, reached through a symlink: filepath.Walk does not
	// follow a symlinked root, which once made every corpus walk see nothing.
	full := filepath.Join(tmp, "full")
	mkfile(t, filepath.Join(full, ".ok"))
	mkfile(t, filepath.Join(full, "A", "one.pdf"))
	mkfile(t, filepath.Join(full, "A", "b", "two.PDF"))
	mkfile(t, filepath.Join(full, "A", "notes.txt"))
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(full, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	lds := Dataset{Name: "linked", Dir: link, Env: env, Stamp: ".ok"}
	t.Run("symlinked data set is walked", func(t *testing.T) {
		t.Setenv(env, "")
		// From the root itself: the symlink is the directory being walked.
		if got := lds.PDFs(t, ""); len(got) != 2 {
			t.Fatalf("PDFs from the root = %v, want the two PDFs", got)
		}
		got := lds.PDFs(t, "A")
		if len(got) != 2 {
			t.Fatalf("PDFs = %v, want the two PDFs", got)
		}
		if g := lds.Glob(t, "A/*.pdf"); len(g) != 1 {
			t.Errorf("Glob = %v, want one", g)
		}
		if f := lds.File(t, "A/notes.txt"); !strings.HasSuffix(f, "notes.txt") {
			t.Errorf("File = %q", f)
		}
	})

	// Sub: the variable may name the checkout or the data inside it; the
	// stamp is at the checkout's top either way.
	arl := filepath.Join(tmp, "arl")
	mkfile(t, filepath.Join(arl, ".ok"))
	mkfile(t, filepath.Join(arl, "tsv", "2.0", "Catalog.tsv"))
	sds := Dataset{Name: "sub", Dir: filepath.Join(tmp, "nowhere"), Env: env, Stamp: ".ok", Sub: "tsv/2.0"}
	for _, val := range []string{arl, filepath.Join(arl, "tsv", "2.0")} {
		t.Run("sub via "+filepath.Base(val), func(t *testing.T) {
			t.Setenv(env, val)
			if got := sds.Glob(t, "*.tsv"); len(got) != 1 {
				t.Errorf("Glob = %v", got)
			}
		})
	}
	t.Run("sub without its stamp fails when configured", func(t *testing.T) {
		bare := filepath.Join(tmp, "bare")
		mkfile(t, filepath.Join(bare, "tsv", "2.0", "Catalog.tsv"))
		t.Setenv(env, filepath.Join(bare, "tsv", "2.0"))
		v, msg := observe(t, func(tb testing.TB) { sds.Path(tb) })
		expect(t, "unstamped sub", v, msg, "fatal")
	})

	// Placed by hand: no stamp; present but empty still fails.
	hand := filepath.Join(tmp, "hand")
	if err := os.MkdirAll(hand, 0o755); err != nil {
		t.Fatal(err)
	}
	hds := Dataset{Name: "hand", Dir: hand}
	t.Run("hand-placed and empty fails", func(t *testing.T) {
		v, msg := observe(t, func(tb testing.TB) { hds.PDFs(tb, "") })
		expect(t, "hand empty", v, msg, "fatal")
	})
}
