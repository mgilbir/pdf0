package pdf0

// The ISO 32000 examples the spec-example tests read, extracted from the
// specifications themselves on the machine that runs them.
//
// The examples are ISO's text, and no permission to redistribute them has
// been found (THIRD_PARTY_NOTICES), so they are not committed. The two PDFs
// are placed by hand (spec/pdf2.0, spec/pdf1.7); where one is, the tests run
// cmd/extract_spec_examples over it and read what it writes. Where it is not,
// they skip, as every test over hand-placed data does; a placed directory
// without its PDF, an extractor that fails, or an extraction that comes out
// short fails instead.
//
// Extracting ISO 32000-2 takes about twelve seconds, so the result is kept in
// testdata/spec-examples/ (gitignored), named by a digest of everything it is
// made from: the PDF, the extractor, and the versions of pdftotext and python3
// that ran it. A change to any of them is a new name, so what the tests read
// is always what the extractor makes of the PDF now, and there is no copy to
// fall out of date.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mgilbir/pdf0/internal/testfiles"
)

// isoSpec is one of the two specifications and how its examples are made.
type isoSpec struct {
	ds     testfiles.Dataset
	pdf    string // the PDF's name in ds
	script string // the extractor, from the module root
	cache  string // the kept JSON's name, before its digest
	// min is the fewest examples a sound extraction yields: ISO 32000-2 has
	// 338 and ISO 32000-1 has 299. Fewer means the extractor or the PDF is
	// not the one the tests were written against.
	min int
}

// The two specifications. They are functions rather than variables so that
// the testing guide's data set table, which follows the calls a test makes,
// sees which tests read the hand-placed PDFs.
func iso32000_2() isoSpec {
	return isoSpec{
		ds:     testfiles.SpecPDF20,
		pdf:    "ISO_32000-2_sponsored-ec2.pdf",
		script: "cmd/extract_spec_examples/main.py",
		cache:  "spec_examples",
		min:    300,
	}
}

func iso32000_1() isoSpec {
	return isoSpec{
		ds:     testfiles.SpecPDF17,
		pdf:    "PDF32000_2008.pdf",
		script: "cmd/extract_spec_examples/main17.py",
		cache:  "spec_examples_17",
		min:    250,
	}
}

// specExamplesDir is where the extracted examples are kept, from the module
// root. .gitignore names it.
const specExamplesDir = "testdata/spec-examples"

var (
	isoExamplesMu   sync.Mutex
	isoExamplesMemo = map[string][]byte{}
)

// isoSpecExamples returns the extracted examples of s, as the extractor's
// JSON. It skips when s's PDF was never placed, or pdftotext or python3 is not
// installed.
func isoSpecExamples(t *testing.T, s isoSpec) []byte {
	t.Helper()
	isoExamplesMu.Lock()
	defer isoExamplesMu.Unlock()
	if data, ok := isoExamplesMemo[s.cache]; ok {
		return data
	}

	pdf := s.ds.File(t, s.pdf) // skips if the directory was never placed
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed; the ISO examples are extracted with it")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed; the ISO examples are extracted with it")
	}
	root := testfiles.Root(t)
	script := filepath.Join(root, filepath.FromSlash(s.script))

	h := sha256.New()
	for _, f := range []string{pdf, script} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%s %d\n", filepath.Base(f), len(b))
		h.Write(b)
	}
	for _, cmd := range [][]string{{"pdftotext", "-v"}, {"python3", "--version"}} {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", cmd, err, out)
		}
		h.Write(out)
	}
	dir := filepath.Join(root, filepath.FromSlash(specExamplesDir))
	kept := filepath.Join(dir, s.cache+"."+hex.EncodeToString(h.Sum(nil))[:16]+".json")

	if data, err := os.ReadFile(kept); err == nil {
		if n, err := countExamples(data); err == nil && n >= s.min {
			isoExamplesMemo[s.cache] = data
			return data
		}
		// It is written whole or not at all (below), so this is a hand
		// edit: make it again.
	}

	txt := filepath.Join(t.TempDir(), "spec.txt")
	if out, err := exec.Command("pdftotext", "-layout", pdf, txt).CombinedOutput(); err != nil {
		t.Fatalf("pdftotext -layout %s: %v\n%s", pdf, err, out)
	}
	data, err := exec.Command("python3", script, txt).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("python3 %s: %v\n%s", s.script, err, exit.Stderr)
		}
		t.Fatalf("python3 %s: %v", s.script, err)
	}
	n, err := countExamples(data)
	if err != nil {
		t.Fatalf("%s wrote no JSON array of examples: %v", s.script, err)
	}
	if n < s.min {
		t.Fatalf("%s found %d examples in %s, and a sound extraction finds at least %d.\n"+
			"The PDF is not the edition the tests expect, or the extraction broke;\n"+
			"either way the tests would check a fraction of the examples.", s.script, n, pdf, s.min)
	}

	// Keep it under its own name, in one rename, so that a reader finds the
	// whole file or none; and drop what older inputs made.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, s.cache+".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), kept)
	}
	if werr != nil {
		os.Remove(f.Name())
		t.Fatal(werr)
	}
	if old, err := filepath.Glob(filepath.Join(dir, s.cache+".*.json")); err == nil {
		for _, o := range old {
			if o != kept {
				os.Remove(o)
			}
		}
	}
	isoExamplesMemo[s.cache] = data
	return data
}

// countExamples returns how many examples an extractor's JSON holds.
func countExamples(data []byte) (int, error) {
	var examples []json.RawMessage
	if err := json.Unmarshal(data, &examples); err != nil {
		return 0, err
	}
	return len(examples), nil
}
