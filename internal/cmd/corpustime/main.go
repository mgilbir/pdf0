//go:build devtools

// Command corpustime times each parse stage of one PDF with a generous budget,
// to distinguish a truly-hanging stage from a merely-slow huge file.
//
// Exit status: 0 when every stage of every file completed; 1 when a file could
// not be read or parsed, a stage failed, or a stage hung; 2 on a usage error.
//
// A hung stage is abandoned, not stopped: its goroutine keeps running while the
// next file is timed, so after a HANG the remaining timings share the CPU with
// it. The file's later stages are skipped rather than run against a document a
// hung stage may still be using.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mgilbir/pdf0"
)

// stage runs fn with a budget and reports its time. It returns false if fn
// did not finish within the budget.
func stage(name string, budget time.Duration, fn func()) bool {
	done := make(chan struct{})
	start := time.Now()
	go func() { defer close(done); fn() }()
	select {
	case <-done:
		fmt.Printf("  %-14s %.1fs\n", name, time.Since(start).Seconds())
		return true
	case <-time.After(budget):
		fmt.Printf("  %-14s HANG (>%s)\n", name, budget)
		return false
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: corpustime <file.pdf> [file.pdf ...]")
		os.Exit(2)
	}
	ok := true
	for _, path := range os.Args[1:] {
		if !timeFile(path) {
			ok = false
		}
	}
	if !ok {
		os.Exit(1)
	}
}

// timeFile times every stage of one file and reports whether all of them
// completed without error.
func timeFile(path string) bool {
	// The size comes from the bytes read, not a separate Stat whose error
	// was once ignored and whose nil FileInfo was then dereferenced (audit
	// 2026-09-22 C156).
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("%s\n  read fail: %v\n", path, err)
		return false
	}
	fmt.Printf("%s (%.1f MB)\n", path, float64(len(data))/(1<<20))

	var doc *pdf0.Document
	var readErr error
	if !stage("Read", 180*time.Second, func() {
		doc, readErr = pdf0.Read(bytes.NewReader(data), int64(len(data)))
	}) {
		return false
	}
	if readErr != nil {
		fmt.Printf("  Read error: %v\n", readErr)
		return false
	}
	if !stage("PageCount", 60*time.Second, func() { _ = doc.PageCount() }) {
		return false
	}
	var writeErr error
	if !stage("Write", 180*time.Second, func() { writeErr = doc.Write(io.Discard) }) {
		return false
	}
	if writeErr != nil {
		fmt.Printf("  Write error: %v\n", writeErr)
		return false
	}
	var findings int
	if !stage("ValidatePDFUA", 180*time.Second, func() { findings = len(pdf0.ValidatePDFUA(doc)) }) {
		return false
	}
	// Findings are the validator's result, not a failure of the stage.
	fmt.Printf("  %-14s %d finding(s)\n", "", findings)
	return true
}
