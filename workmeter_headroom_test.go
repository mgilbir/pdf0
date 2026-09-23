package pdf0

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/testfiles"
	"github.com/mgilbir/pdf0/pdfa"
	"github.com/mgilbir/pdf0/pdfx"
)

// maxHeadroomFile is the largest file TestWorkMeterHeadroom measures unless
// PDF0_WORK_BIG is set: the Cal Poly suite's larger variants run to 330 MB,
// and every validator over each is minutes of the suite. They were measured
// when the default was set (see DefaultMaxWork).
const maxHeadroomFile = 32 << 20

// workHeadroom is how far inside its default work budget every real document
// must stay: a file that charges more than an eighth of core.DefaultWork(its
// size) in any one run fails TestWorkMeterHeadroom.
const workHeadroom = 8

// workOps are the operations a run is measured for: every validator and both
// extractors, each its own run, as a caller would make them.
var workOps = []struct {
	name string
	run  func(rd *Document, raw []byte)
}{
	{"PDF/A-2b", func(rd *Document, _ []byte) { ValidatePDFA(rd, pdfa.PDFA2b) }},
	{"PDF/A-1b", func(rd *Document, _ []byte) { ValidatePDFA(rd, pdfa.PDFA1b) }},
	{"PDF/A-2a", func(rd *Document, _ []byte) { ValidatePDFA(rd, pdfa.PDFA2a) }},
	{"PDF/A-4", func(rd *Document, _ []byte) { ValidatePDFA(rd, pdfa.PDFA4) }},
	{"PDF/UA", func(rd *Document, _ []byte) { ValidatePDFUA(rd) }},
	{"PDF/X-4", func(rd *Document, _ []byte) { ValidatePDFX(rd, pdfx.PDFX4) }},
	{"PDF/VT", func(rd *Document, _ []byte) { ValidatePDFVT(rd) }},
	{"text", func(rd *Document, _ []byte) { _, _ = rd.ExtractText() }},
	// The iterator, so that one decoded image is live at a time: the run's
	// work is the same, and a corpus of scans does not have to fit in memory.
	{"images", func(rd *Document, _ []byte) {
		for range rd.Images() {
		}
	}},
}

type workSample struct {
	file, op string
	size     int64
	used     int64
	elapsed  time.Duration
}

// measureWork runs every op over the file at path, each in its own run, and
// returns what each charged.
func measureWork(t *testing.T, path string) []workSample {
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil // an unreadable file does no validation work
	}
	var out []workSample
	for _, op := range workOps {
		rd := beginRun(doc)
		start := time.Now()
		op.run(rd, raw)
		out = append(out, workSample{file: path, op: op.name, size: int64(len(raw)), used: rd.valCache.run.shared.Meter.Used(), elapsed: time.Since(start)})
	}
	return out
}

// TestWorkMeterHeadroom holds every real document the suite can see to an
// eighth of its default work budget in every run: each validator and each
// extractor over the veraPDF corpus, the PDF 2.0 examples, the PDF/VT, WTPDF
// and Factur-X suites, and whatever directory PDF0_WORK_CORPUS names (the
// Common Crawl sample the default was measured against). It is what makes the
// default a measured one: a change that makes some walk charge far more than
// it did, or a real file that needs far more than the files measured, fails
// here rather than as a refused validation in someone's service.
//
// The heaviest runs are logged, with their wall time, which is what the
// budget costs in time: see DefaultMaxWork.
func TestWorkMeterHeadroom(t *testing.T) {
	var files []string
	for _, ds := range []testfiles.Dataset{testfiles.VeraPDFCorpus, testfiles.PDF20Examples, testfiles.CalPolyPDFVT, testfiles.WTPDF, testfiles.FacturX} {
		if _, ok, reason := ds.Lookup(t); !ok {
			t.Logf("%s not measured: %s", ds.Name, reason)
			continue
		}
		if ds.Name == testfiles.VeraPDFCorpus.Name {
			files = append(files, ds.Files(t, "", testfiles.IsPDF)...) // a tree
		} else {
			files = append(files, ds.Glob(t, "*.pdf")...) // flat, of links
		}
	}
	if dir := os.Getenv("PDF0_WORK_CORPUS"); dir != "" {
		extra, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
		if err != nil || len(extra) == 0 {
			t.Fatalf("PDF0_WORK_CORPUS=%s holds no PDFs (%v)", dir, err)
		}
		files = append(files, extra...)
	}
	if len(files) == 0 {
		t.Skip("no corpus present to measure")
	}
	progress := os.Getenv("PDF0_WORK_PROGRESS") != ""
	csv := os.Getenv("PDF0_WORK_CSV")
	var all []workSample
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.Size() > maxHeadroomFile && os.Getenv("PDF0_WORK_BIG") == "" {
			continue
		}
		if progress {
			fmt.Fprintln(os.Stderr, "measuring", f)
		}
		got := measureWork(t, f)
		all = append(all, got...)
		if csv != "" {
			out, err := os.OpenFile(csv, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				for _, s := range got {
					fmt.Fprintf(out, "%d\t%d\t%d\t%s\t%s\n", s.used, s.size, s.elapsed.Microseconds(), s.op, s.file)
				}
				out.Close()
			}
		}
	}
	// Heaviest relative to its own budget first.
	share := func(s workSample) float64 { return float64(s.used) / float64(core.DefaultWork(s.size)) }
	sort.Slice(all, func(i, j int) bool { return share(all[i]) > share(all[j]) })
	var total time.Duration
	for _, s := range all {
		total += s.elapsed
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d files, %d runs, %v in all; the runs nearest their budget:\n", len(files), len(all), total.Round(time.Millisecond))
	for _, s := range all[:min(10, len(all))] {
		fmt.Fprintf(&b, "  %12d of %12d units  %8v  %-9s %s\n", s.used, core.DefaultWork(s.size), s.elapsed.Round(time.Millisecond), s.op, s.file)
	}
	t.Log(b.String())
	if top := all[0]; top.used > core.DefaultWork(top.size)/workHeadroom {
		t.Errorf("%s over %s charged %d units, more than 1/%d of its default budget %d", top.op, top.file, top.used, workHeadroom, core.DefaultWork(top.size))
	} else {
		t.Logf("headroom: every run is at least %.0fx inside its default budget", 1/share(top))
	}
}
