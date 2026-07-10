// Command corpusprobe stress-tests the parser against a directory of
// (untrusted) PDFs, recording parse outcomes and — most importantly — any
// panics or hangs, which represent robustness bugs: the parser must return an
// error, never crash or loop, on malformed input.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
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
func probe(path string) outcome {
	data, err := os.ReadFile(path)
	if err != nil {
		return outcome{"readfail", err.Error(), path}
	}
	type r struct{ kind, detail string }
	ch := make(chan r, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- r{"panic", fmt.Sprintf("%v\n%s", rec, topFrames(debug.Stack()))}
			}
		}()
		doc, e := pdf0.Read(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			ch <- r{"error", e.Error()}
			return
		}
		// Exercise more of the pipeline on a successful parse — all of these
		// run on untrusted input and must also never panic.
		_ = doc.PageCount()
		var buf bytes.Buffer
		_ = doc.Write(&buf)
		_ = pdf0.ValidatePDFUA(doc)
		ch <- r{"ok", ""}
	}()
	select {
	case res := <-ch:
		return outcome{res.kind, res.detail, path}
	case <-time.After(perFileTimeout):
		return outcome{"timeout", "", path}
	}
}

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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: corpusprobe <dir> [workers]")
		os.Exit(2)
	}
	dir := os.Args[1]
	workers := 8
	if len(os.Args) > 2 {
		fmt.Sscanf(os.Args[2], "%d", &workers)
	}

	var files []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(strings.ToLower(p), ".pdf") {
			files = append(files, p)
		}
		return nil
	})
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
	// Per-file log of every non-ok outcome (path\tkind\tdetail).
	failLog, _ := os.Create(filepath.Join(os.TempDir(), "corpusprobe-failures.tsv"))
	if failLog != nil {
		defer failLog.Close()
	}
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
		if o.kind != "ok" && failLog != nil {
			fmt.Fprintf(failLog, "%s\t%s\t%s\n", o.path, o.kind, strings.ReplaceAll(o.detail, "\n", " ⏎ "))
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
}
