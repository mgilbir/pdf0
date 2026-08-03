package pdf0

import (
	"os"
	"path/filepath"
	"testing"
)

// Finding the test corpora, and telling "nobody fetched it" apart from
// "somebody pointed at the wrong place".
//
// The corpora are large and gitignored, fetched on demand by `make corpus` and
// `make arlington`, so a developer without them has to be able to run the
// suite: absent means skip. But several of the tests that read them are
// *ratchets* — they assert a count of findings over thousands of documents —
// and a skip is indistinguishable from a pass in go test's output. So a path
// that was configured and does not resolve has to fail, loudly, rather than
// quietly turning the oracle off.
//
// That is not hypothetical. ARLINGTON_MODEL pointed at the repository root
// instead of the tsv/2.0 directory inside it, and the whole Arlington oracle
// skipped while the run reported success.

// corpusRoot returns the veraPDF corpus directory.
//
// Unset and absent is a skip. Set and unresolvable is a failure that says so.
func corpusRoot(t *testing.T) string {
	t.Helper()
	return testDataDir(t, "VERAPDF_CORPUS", "testdata/verapdf-corpus", "", "make corpus")
}

// corpusPath is corpusRoot joined with the rest of a path, for a test that
// wants one particular file out of the corpus.
func corpusPath(t *testing.T, parts ...string) string {
	t.Helper()
	return filepath.Join(append([]string{corpusRoot(t)}, parts...)...)
}

// corpusSubdir returns a named directory inside the corpus, skipping when the
// corpus is absent and failing when it is present but does not contain it —
// which means the corpus moved, not that it is missing.
func corpusSubdir(t *testing.T, name string) string {
	t.Helper()
	root := corpusRoot(t)
	dir := filepath.Join(root, name)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the corpus at %q has no %q directory.\n"+
			"The corpus is present, so this is a layout change rather than a missing download;\n"+
			"skipping would report success having checked nothing.", root, name)
	}
	return dir
}

// testDataDir is the shared resolution: the environment overrides the default,
// a nested subdirectory is accepted when that is the usual mistake, and the
// distinction between unconfigured and misconfigured is preserved.
//
// marker is a file that must exist inside the directory, or "" to accept the
// directory itself. nested is a path to try beneath it before giving up, for
// the case where someone names a checkout rather than the data inside it.
func testDataDir(t *testing.T, env, fallback, marker, fetch string) string {
	t.Helper()
	configured := os.Getenv(env)
	dir := configured
	if dir == "" {
		dir = fallback
	}
	if dataDirOK(dir, marker) {
		return dir
	}
	if configured == "" {
		t.Skipf("%s not present; run `%s`", fallback, fetch)
	}
	t.Fatalf("%s is set to %q and there is nothing usable there.\n"+
		"Failing rather than skipping: the tests that read this are ratchets, and a skip\n"+
		"would report success having checked nothing.", env, configured)
	return ""
}

func dataDirOK(dir, marker string) bool {
	if dir == "" {
		return false
	}
	probe := dir
	if marker != "" {
		probe = filepath.Join(dir, marker)
	}
	_, err := os.Stat(probe)
	return err == nil
}
