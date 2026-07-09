package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/mgilbir/pdf0"
)

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	pw := fs.String("password", "", "user or owner password")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pdf0 info [-password PW] <file>")
	}
	doc, err := readDoc(fs.Arg(0), *pw)
	if err != nil {
		return err
	}
	pages := 0
	for _, iobj := range doc.Objects {
		if d, ok := iobj.Value.(*pdf0.Dictionary); ok {
			if t, _ := d.Get("Type").(pdf0.Name); t == "Page" {
				pages++
			}
		}
	}
	fmt.Printf("version:   %s\n", doc.Version)
	fmt.Printf("objects:   %d\n", len(doc.Objects))
	fmt.Printf("pages:     %d\n", pages)
	fmt.Printf("encrypted: %v\n", doc.Encrypted)
	return nil
}

func cmdValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	level := fs.String("level", "2b", "PDF/A level: 1b, 2b, 3b, or 4")
	pw := fs.String("password", "", "user or owner password")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pdf0 validate [-level 1b|2b|3b|4] <file>")
	}
	lvl, ok := parseLevel(*level)
	if !ok {
		return fmt.Errorf("unknown level %q (want 1b, 2b, 3b, or 4)", *level)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var doc *pdf0.Document
	if *pw != "" {
		doc, err = pdf0.ReadWithPassword(bytes.NewReader(data), int64(len(data)), *pw)
	} else {
		doc, err = pdf0.Read(bytes.NewReader(data), int64(len(data)))
	}
	if err != nil {
		return err
	}
	errs := pdf0.ValidatePDFABytes(doc, lvl, data)
	if len(errs) == 0 {
		fmt.Printf("%s: no violations found for PDF/A-%s\n", fs.Arg(0), *level)
		return nil
	}
	for _, e := range errs {
		fmt.Println(e)
	}
	return fmt.Errorf("%d violation(s) found", len(errs))
}

func cmdDecrypt(args []string) error {
	fs := flag.NewFlagSet("decrypt", flag.ExitOnError)
	pw := fs.String("password", "", "user or owner password")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: pdf0 decrypt [-password PW] <in> <out>")
	}
	doc, err := readDoc(fs.Arg(0), *pw)
	if err != nil {
		return err
	}
	if !doc.Encrypted {
		return fmt.Errorf("%s is not encrypted", fs.Arg(0))
	}
	doc.RemoveEncryption()
	return writeDoc(doc, fs.Arg(1))
}

func cmdEncrypt(args []string) error {
	fs := flag.NewFlagSet("encrypt", flag.ExitOnError)
	user := fs.String("user", "", "user password")
	owner := fs.String("owner", "", "owner password (defaults to the user password)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: pdf0 encrypt -user PW [-owner PW] <in> <out>")
	}
	ownerPw := *owner
	if ownerPw == "" {
		ownerPw = *user
	}
	doc, err := readDoc(fs.Arg(0), "")
	if err != nil {
		return err
	}
	if err := doc.SetEncryption(*user, ownerPw); err != nil {
		return err
	}
	return writeDoc(doc, fs.Arg(1))
}

func cmdExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ExitOnError)
	pw := fs.String("password", "", "user or owner password")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pdf0 extract [-password PW] <file>")
	}
	doc, err := readDoc(fs.Arg(0), *pw)
	if err != nil {
		return err
	}
	fmt.Print(doc.ExtractText())
	return nil
}

func cmdRepair(args []string) error {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	level := fs.String("level", "2b", "target PDF/A level")
	pw := fs.String("password", "", "user or owner password")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: pdf0 repair [-level 1b|2b|3b|4] [-password PW] <in> <out>")
	}
	lvl, ok := parseLevel(*level)
	if !ok {
		return fmt.Errorf("unknown level %q", *level)
	}
	doc, err := readDoc(fs.Arg(0), *pw)
	if err != nil {
		return err
	}
	for _, a := range doc.Repair(lvl) {
		fmt.Println("fixed:", a.Description)
	}
	return writeDoc(doc, fs.Arg(1))
}

func parseLevel(s string) (pdf0.PDFALevel, bool) {
	switch s {
	case "1b":
		return pdf0.PDFA1b, true
	case "2b":
		return pdf0.PDFA2b, true
	case "3b":
		return pdf0.PDFA3b, true
	case "4":
		return pdf0.PDFA4, true
	}
	return 0, false
}

func writeDoc(doc *pdf0.Document, path string) error {
	var buf bytes.Buffer
	if err := doc.Write(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
