package pdf0

import "testing"

// TestUAFontDicts covers the dictionary-level clause 7.21 font rules.
func TestUAFontDicts(t *testing.T) {
	// Build a document holding one font dictionary (object 10) plus its
	// descendant/descriptor, and run the per-font check directly.
	run := func(build func(doc *Document) *Dictionary) []UAViolation {
		doc := &Document{Objects: map[int]*IndirectObject{}}
		f := build(doc)
		doc.Objects[10] = &IndirectObject{Number: 10, Value: f}
		return doc.checkOneUAFontDict(f)
	}

	// CIDFontType2 embedded, no CIDToGIDMap -> flagged.
	bad := run(func(doc *Document) *Dictionary {
		fd := &Dictionary{}
		fd.Set("FontFile2", IndirectRef{Number: 20})
		cid := &Dictionary{}
		cid.Set("Subtype", Name("CIDFontType2"))
		cid.Set("FontDescriptor", fd)
		doc.Objects[11] = &IndirectObject{Number: 11, Value: cid}
		f := &Dictionary{}
		f.Set("Subtype", Name("Type0"))
		f.Set("DescendantFonts", Array{IndirectRef{Number: 11}})
		return f
	})
	if len(bad) == 0 {
		t.Error("CIDFontType2 without CIDToGIDMap not flagged")
	}
	// With CIDToGIDMap present -> clean.
	ok := run(func(doc *Document) *Dictionary {
		fd := &Dictionary{}
		fd.Set("FontFile2", IndirectRef{Number: 20})
		cid := &Dictionary{}
		cid.Set("Subtype", Name("CIDFontType2"))
		cid.Set("FontDescriptor", fd)
		cid.Set("CIDToGIDMap", Name("Identity"))
		doc.Objects[11] = &IndirectObject{Number: 11, Value: cid}
		f := &Dictionary{}
		f.Set("Subtype", Name("Type0"))
		f.Set("DescendantFonts", Array{IndirectRef{Number: 11}})
		return f
	})
	if len(ok) != 0 {
		t.Errorf("CIDFontType2 with CIDToGIDMap wrongly flagged: %v", ok)
	}

	// Symbolic TrueType with an Encoding -> flagged.
	sym := run(func(doc *Document) *Dictionary {
		fd := &Dictionary{}
		fd.Set("Flags", Integer(4)) // symbolic
		f := &Dictionary{}
		f.Set("Subtype", Name("TrueType"))
		f.Set("FontDescriptor", fd)
		f.Set("Encoding", Name("WinAnsiEncoding"))
		return f
	})
	if len(sym) == 0 {
		t.Error("symbolic TrueType with Encoding not flagged")
	}
	// Non-symbolic TrueType without a standard encoding -> flagged.
	ns := run(func(doc *Document) *Dictionary {
		fd := &Dictionary{}
		fd.Set("Flags", Integer(32)) // non-symbolic
		f := &Dictionary{}
		f.Set("Subtype", Name("TrueType"))
		f.Set("FontDescriptor", fd)
		return f
	})
	if len(ns) == 0 {
		t.Error("non-symbolic TrueType without MacRoman/WinAnsi not flagged")
	}
	// Non-symbolic TrueType with WinAnsiEncoding -> clean.
	nsok := run(func(doc *Document) *Dictionary {
		fd := &Dictionary{}
		fd.Set("Flags", Integer(32))
		f := &Dictionary{}
		f.Set("Subtype", Name("TrueType"))
		f.Set("FontDescriptor", fd)
		f.Set("Encoding", Name("WinAnsiEncoding"))
		return f
	})
	if len(nsok) != 0 {
		t.Errorf("non-symbolic TrueType with WinAnsiEncoding wrongly flagged: %v", nsok)
	}
}
