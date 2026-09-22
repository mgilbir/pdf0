package pdf0

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkReadCorpus reads every PDF in the veraPDF corpus once per
// iteration: a corpus-sized measure of the parser and the object model on
// real files. Files that fail to read are skipped (the corpus contains
// deliberately broken ones); the count read is reported so two runs can be
// checked to have measured the same work.
func BenchmarkReadCorpus(b *testing.B) {
	root := os.Getenv("VERAPDF_CORPUS")
	if root == "" {
		root = "testdata/verapdf-corpus"
	}
	root, _ = filepath.EvalSymlinks(root)
	var files [][]byte
	var total int64
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".pdf") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil {
			files = append(files, data)
			total += int64(len(data))
		}
		return nil
	})
	if len(files) == 0 {
		b.Skip("veraPDF corpus not present")
	}
	b.SetBytes(total)
	b.ReportAllocs()
	ok := 0
	for b.Loop() {
		ok = 0
		for _, data := range files {
			if _, err := Read(bytes.NewReader(data), int64(len(data))); err == nil {
				ok++
			}
		}
	}
	b.ReportMetric(float64(ok), "files-read")
}
