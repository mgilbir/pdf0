package pdf0

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// Finding the veraPDF corpus, and telling "nobody fetched it" apart from
// "somebody pointed at the wrong place" and from "it is there and empty".
//
// The corpus is large and gitignored, fetched on demand by `make corpus`, so a
// developer without it has to be able to run the suite: absent means skip. But
// the tests that read it are *ratchets*, asserting a count of findings over
// thousands of documents, and a skip or an empty walk is indistinguishable from
// a pass in go test's output. The resolution lives in internal/testfiles, shared
// by every data set the suite reads:
//
//   - the Makefile's .ok stamp, not the directory, is what says the corpus is
//     there (an interrupted fetch leaves a directory holding only .git);
//   - VERAPDF_CORPUS set to anything unusable fails;
//   - a walk that finds zero files fails;
//   - walks start from the resolved directory, because filepath.Walk does not
//     follow a symlinked root. A corpus linked into a worktree used to walk
//     zero files, and TestCorpusParsesEntirely and the Arlington corpus oracle
//     reported "0 files" and passed (audit 2026-09-22 C104).

// corpusRoot returns the veraPDF corpus directory, symlinks resolved.
func corpusRoot(t *testing.T) string {
	t.Helper()
	return testfiles.VeraPDFCorpus.Path(t)
}

// corpusSubdir returns a named directory inside the corpus, skipping when the
// corpus is absent and failing when it is present but does not contain it,
// which means the corpus moved, not that it is missing.
func corpusSubdir(t *testing.T, name string) string {
	t.Helper()
	return testfiles.VeraPDFCorpus.File(t, name)
}

// corpusFileNamed returns the one corpus PDF whose path contains sub, failing
// when the corpus is present and no file matches.
func corpusFileNamed(t *testing.T, sub string) string {
	t.Helper()
	files := testfiles.VeraPDFCorpus.Files(t, "", func(p string) bool {
		return testfiles.IsPDF(p) && strings.Contains(p, sub)
	})
	return files[len(files)-1]
}

// corpusTestFiles returns the corpus's test documents under rel (every PDF
// whose name marks it -pass- or -fail-), failing if there are none.
func corpusTestFiles(t *testing.T, rel string) []string {
	t.Helper()
	return testfiles.VeraPDFCorpus.Files(t, rel, func(p string) bool {
		base := filepath.Base(p)
		return testfiles.IsPDF(p) && (strings.Contains(base, "-pass-") || strings.Contains(base, "-fail-"))
	})
}
