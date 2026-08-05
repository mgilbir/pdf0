package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
	"testing"
)

// TestUAFontDicts covers the dictionary-level clause 7.21 font rules.
func TestUAFontDicts(t *testing.T) {
	// Build a document holding one font dictionary (object 10) plus its
	// descendant/descriptor, and run the per-font check directly.
	run := func(build func(doc core.View) *object.Dictionary) []Violation {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		f := build(doc)
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: f}
		return checkOneUAFontDict(doc, f)
	}

	// CIDFontType2 embedded, no CIDToGIDMap -> flagged.
	bad := run(func(doc core.View) *object.Dictionary {
		fd := &object.Dictionary{}
		fd.Set("FontFile2", object.IndirectRef{Number: 20})
		cid := &object.Dictionary{}
		cid.Set("Subtype", object.Name("CIDFontType2"))
		cid.Set("FontDescriptor", fd)
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: cid}
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 11}})
		return f
	})
	if len(bad) == 0 {
		t.Error("CIDFontType2 without CIDToGIDMap not flagged")
	}
	// With CIDToGIDMap present -> clean.
	ok := run(func(doc core.View) *object.Dictionary {
		fd := &object.Dictionary{}
		fd.Set("FontFile2", object.IndirectRef{Number: 20})
		cid := &object.Dictionary{}
		cid.Set("Subtype", object.Name("CIDFontType2"))
		cid.Set("FontDescriptor", fd)
		cid.Set("CIDToGIDMap", object.Name("Identity"))
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: cid}
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 11}})
		return f
	})
	if len(ok) != 0 {
		t.Errorf("CIDFontType2 with CIDToGIDMap wrongly flagged: %v", ok)
	}

	// Symbolic TrueType with an Encoding -> flagged.
	sym := run(func(doc core.View) *object.Dictionary {
		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(4)) // symbolic
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("TrueType"))
		f.Set("FontDescriptor", fd)
		f.Set("Encoding", object.Name("WinAnsiEncoding"))
		return f
	})
	if len(sym) == 0 {
		t.Error("symbolic TrueType with Encoding not flagged")
	}
	// Non-symbolic TrueType without a standard encoding -> flagged.
	ns := run(func(doc core.View) *object.Dictionary {
		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(32)) // non-symbolic
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("TrueType"))
		f.Set("FontDescriptor", fd)
		return f
	})
	if len(ns) == 0 {
		t.Error("non-symbolic TrueType without MacRoman/WinAnsi not flagged")
	}
	// Non-symbolic TrueType with WinAnsiEncoding -> clean.
	nsok := run(func(doc core.View) *object.Dictionary {
		fd := &object.Dictionary{}
		fd.Set("Flags", object.Integer(32))
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("TrueType"))
		f.Set("FontDescriptor", fd)
		f.Set("Encoding", object.Name("WinAnsiEncoding"))
		return f
	})
	if len(nsok) != 0 {
		t.Errorf("non-symbolic TrueType with WinAnsiEncoding wrongly flagged: %v", nsok)
	}
}

// TestUACIDToGIDMapValue flags a /CIDToGIDMap name other than Identity.
func TestUACIDToGIDMapValue(t *testing.T) {
	mk := func(v object.Object) core.View {
		doc := mkView(map[int]*object.IndirectObject{}, object.Dictionary{})
		cid := &object.Dictionary{}
		cid.Set("Subtype", object.Name("CIDFontType2"))
		if v != nil {
			cid.Set("CIDToGIDMap", v)
		}
		doc.Objects[11] = &object.IndirectObject{Number: 11, Value: cid}
		f := &object.Dictionary{}
		f.Set("Subtype", object.Name("Type0"))
		f.Set("DescendantFonts", object.Array{object.IndirectRef{Number: 11}})
		doc.Objects[10] = &object.IndirectObject{Number: 10, Value: f}
		return doc
	}
	check := func(doc core.View) []Violation {
		return checkOneUAFontDict(doc, doc.Objects[10].Value.(*object.Dictionary))
	}
	if !hasUAClause(check(mk(object.Name("NoIdentity"))), "7.21.3.2") {
		t.Error("CIDToGIDMap /NoIdentity not flagged")
	}
	if hasUAClause(check(mk(object.Name("Identity"))), "7.21.3.2") {
		t.Error("CIDToGIDMap /Identity wrongly flagged")
	}
	// A stream value is valid.
	if hasUAClause(check(mk(&object.Stream{})), "7.21.3.2") {
		t.Error("CIDToGIDMap stream wrongly flagged")
	}
}
