//go:build devtools

// Command corpusprobe stress-tests the parser against a directory of
// (untrusted) PDFs, recording parse outcomes and — most importantly — any
// panics or hangs, which represent robustness bugs: the parser must return an
// error, never crash or loop, on malformed input.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mgilbir/pdf0"
)

type outcome struct {
	kind   string // ok, error, panic, timeout, readfail
	detail string
	path   string
}

const perFileTimeout = 30 * time.Second

// probe reads one file under panic recovery and a timeout. A panic or a timeout
// is a parser bug.
//
// The timeout is expressed as a context deadline that pdf0 itself observes, not
// only as a select on the result channel. The two do different jobs and both are
// needed:
//
//   - The context stops the work. Before pdf0 had context variants this loop
//     abandoned the goroutine on timeout and the work carried on burning a core
//     and holding its memory until it finished on its own — with eight workers
//     and a 25-second file, a real leak, and the concrete reason cancellation
//     was added to the library.
//   - The select still bounds the wait, because a hang pdf0 does not check for
//     cancellation in would otherwise block this worker forever, and detecting
//     exactly that is what this program is for. A timeout that the context did
//     not resolve is therefore a stronger signal than it used to be: it means
//     the work did not stop when told to, not merely that it was slow.
func probe(path string) outcome {
	data, err := os.ReadFile(path)
	if err != nil {
		return outcome{"readfail", err.Error(), path}
	}
	ctx, cancel := context.WithTimeout(context.Background(), perFileTimeout)
	defer cancel()

	type r struct{ kind, detail string }
	ch := make(chan r, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- r{"panic", fmt.Sprintf("%v\n%s", rec, topFrames(debug.Stack()))}
			}
		}()
		doc, e := pdf0.ReadContext(ctx, bytes.NewReader(data), int64(len(data)))
		if e != nil {
			if ctx.Err() != nil {
				ch <- r{"timeout", "cancelled during read"}
				return
			}
			ch <- r{"error", e.Error()}
			return
		}
		// Exercise more of the pipeline on a successful parse — all of these
		// run on untrusted input and must also never panic.
		_ = doc.PageCount()
		// Stream the serialized output to io.Discard rather than buffering it:
		// a pathological file can legitimately produce a very large output (e.g.
		// a malformed xref that references one big stream from many objects), and
		// buffering it would OOM the probe on otherwise-handled input. A real
		// consumer writes to a streaming io.Writer, so this matches real usage
		// while still exercising the serializer.
		_ = doc.WriteContext(ctx, io.Discard)
		_ = pdf0.ValidatePDFUAContext(ctx, doc)
		if ctx.Err() != nil {
			ch <- r{"timeout", "cancelled during validation"}
			return
		}
		ch <- r{"ok", ""}
	}()
	select {
	case res := <-ch:
		return outcome{res.kind, res.detail, path}
	case <-ctx.Done():
		// The deadline fired and pdf0 did not return within the grace period
		// below, so the work is genuinely stuck rather than merely slow.
		select {
		case res := <-ch:
			return outcome{res.kind, res.detail, path}
		case <-time.After(unresponsiveGrace):
			return outcome{"timeout", "did not stop when cancelled", path}
		}
	}
}

// unresponsiveGrace is how long a cancelled probe is given to wind down before
// it is declared stuck. Cancellation is checked at coarse boundaries — per
// check, per page, per stream, per megabyte scanned — so a second is orders of
// magnitude more than a responsive run needs.
const unresponsiveGrace = time.Second

// topFrames extracts the first few pdf0 stack frames from a panic stack.
func topFrames(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
	var out []string
	for i := 0; i < len(lines) && len(out) < 8; i++ {
		if strings.Contains(lines[i], "mgilbir/pdf0") && !strings.Contains(lines[i], "corpusprobe") {
			out = append(out, strings.TrimSpace(lines[i]))
			if i+1 < len(lines) {
				out = append(out, strings.TrimSpace(lines[i+1]))
			}
		}
	}
	return strings.Join(out, " | ")
}

// normalize collapses file-specific numbers so similar errors group together.
var numRe = regexp.MustCompile(`\d+`)

func normalize(s string) string {
	s = numRe.ReplaceAllString(s, "N")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// maxWorkers bounds the worker count. Each worker holds a whole file and its
// parse in memory, so the useful number is a small multiple of the cores; the
// bound only stops a typo from spawning millions of goroutines.
const maxWorkers = 1024

// parseArgs validates the command line: a directory, and an optional worker
// count in 1..maxWorkers (default 8). "-1" used to panic in make(chan) and "0"
// to probe nothing and report success (audit 2026-09-22 C156).
func parseArgs(args []string) (dir string, workers int, err error) {
	if len(args) < 1 || len(args) > 2 {
		return "", 0, fmt.Errorf("want a directory and an optional worker count")
	}
	dir, workers = args[0], 8
	if len(args) == 2 {
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 || n > maxWorkers {
			return "", 0, fmt.Errorf("workers must be an integer from 1 to %d, got %q", maxWorkers, args[1])
		}
		workers = n
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return "", 0, err
	}
	if !fi.IsDir() {
		return "", 0, fmt.Errorf("%s is not a directory", dir)
	}
	return dir, workers, nil
}

// Exit status: 0 when every file was probed and none panicked or hung; 1 when
// some did, when the walk hit errors, or when there was nothing to probe; 2 on
// a usage error. testdata/cc/sweep.sh reads the report, not the status.
func main() {
	dir, workers, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpusprobe: %v\nusage: corpusprobe <dir> [workers]\n", err)
		os.Exit(2)
	}

	var files []string
	var walkErrs int
	walkErr := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			// Report and carry on: one unreadable subdirectory should not
			// stop the probe, but it must not pass unnoticed either.
			fmt.Fprintf(os.Stderr, "corpusprobe: walking %s: %v\n", p, err)
			walkErrs++
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(p), ".pdf") {
			files = append(files, p)
		}
		return nil
	})
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "corpusprobe: walking %s: %v\n", dir, walkErr)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "corpusprobe: no .pdf files under %s\n", dir)
		os.Exit(1)
	}

	// The per-file log is how testdata/cc/sweep.sh finds the files to
	// quarantine; without it a panic would be counted but its repro lost, so
	// failing to create it stops the run before any work.
	failLogPath := filepath.Join(os.TempDir(), "corpusprobe-failures.tsv")
	failLog, err := os.Create(failLogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpusprobe: creating the failure log: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("probing %d PDFs with %d workers, %s timeout\n", len(files), workers, perFileTimeout)

	jobs := make(chan string, workers)
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				results <- probe(p)
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
	}()
	go func() { wg.Wait(); close(results) }()

	counts := map[string]int{}
	errGroups := map[string]int{}
	var panics, timeouts []outcome
	var done int64
	var logErr error
	for o := range results {
		counts[o.kind]++
		switch o.kind {
		case "error":
			errGroups[normalize(o.detail)]++
		case "panic":
			panics = append(panics, o)
		case "timeout":
			timeouts = append(timeouts, o)
		}
		// Per-file log of every non-ok outcome (path\tkind\tdetail).
		if o.kind != "ok" && logErr == nil {
			_, logErr = fmt.Fprintf(failLog, "%s\t%s\t%s\n", o.path, o.kind, strings.ReplaceAll(o.detail, "\n", " ⏎ "))
		}
		if n := atomic.AddInt64(&done, 1); n%1000 == 0 {
			fmt.Printf("  ...%d done\n", n)
		}
	}

	fmt.Println("\n=== OUTCOMES ===")
	for _, k := range []string{"ok", "error", "panic", "timeout", "readfail"} {
		fmt.Printf("  %-9s %d\n", k, counts[k])
	}

	fmt.Println("\n=== TOP ERROR GROUPS (normalized) ===")
	type kv struct {
		k string
		n int
	}
	var eg []kv
	for k, n := range errGroups {
		eg = append(eg, kv{k, n})
	}
	sort.Slice(eg, func(i, j int) bool { return eg[i].n > eg[j].n })
	for i, e := range eg {
		if i >= 30 {
			break
		}
		fmt.Printf("  %5d  %s\n", e.n, e.k)
	}

	fmt.Printf("\n=== PANICS (%d) — THESE ARE BUGS ===\n", len(panics))
	for _, p := range panics {
		fmt.Printf("  %s\n    %s\n", filepath.Base(p.path), p.detail)
	}
	fmt.Printf("\n=== TIMEOUTS/HANGS (%d) — THESE ARE BUGS ===\n", len(timeouts))
	for _, t := range timeouts {
		fmt.Printf("  %s\n", t.path)
	}

	if err := failLog.Close(); err != nil && logErr == nil {
		logErr = err
	}
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "corpusprobe: writing %s: %v\n", failLogPath, logErr)
	}
	if len(panics) > 0 || len(timeouts) > 0 || counts["readfail"] > 0 || walkErrs > 0 || logErr != nil {
		os.Exit(1)
	}
}
