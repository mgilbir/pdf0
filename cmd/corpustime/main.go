// Command corpustime times each parse stage of one PDF with a generous budget,
// to distinguish a truly-hanging stage from a merely-slow huge file.
package main

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"github.com/mgilbir/pdf0"
)

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
	for _, path := range os.Args[1:] {
		fi, _ := os.Stat(path)
		fmt.Printf("%s (%d MB)\n", path, fi.Size()/(1024*1024))
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Println("  read fail:", err)
			continue
		}
		var doc *pdf0.Document
		if !stage("Read", 180*time.Second, func() {
			doc, _ = pdf0.Read(bytes.NewReader(data), int64(len(data)))
		}) || doc == nil {
			continue
		}
		stage("PageCount", 60*time.Second, func() { _ = doc.PageCount() })
		stage("Write", 180*time.Second, func() { var b bytes.Buffer; _ = doc.Write(&b) })
		stage("ValidatePDFUA", 180*time.Second, func() { _ = pdf0.ValidatePDFUA(doc) })
	}
}
