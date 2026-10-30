package pdfua

import "github.com/mgilbir/pdf0/object"

// ISO/TS 32005:2023, Table 5: the parent-child relationships between the
// standard structure elements of the PDF 1.7 and PDF 2.0 namespaces. A PDF
// 1.7 or PDF 2.0 element shall have no child of those namespaces that its
// row does not list (7.2). This is pdf0's encoding of the Children column,
// written from the published Technical Specification; Hn stands for H1, H2
// and so on, and contentItem for marked content, as in the standard. The
// Parents column states the same relationships from the other side, and is
// not repeated.
//
// TestISO32005TableAgreesWithVeraPDF compares the rules this table implies
// with veraPDF's PDF/UA-2 profile, which also states them, where the profile
// is present (make verapdf-profiles), and lists where the two differ.

// inclusion is a child type's valid usage in a row (Table 4).
type inclusion uint8

const (
	incAny        inclusion = iota + 1 // 0..n
	incAtMostOne                       // 0..1
	incExactlyOne                      // 1
	incGrouping                        // ø*: only where the parent is used as a grouping element
	incDescribed                       // ‡, [a], [b]: the type's own description says
)

// contentItem is the Children column's "content item": marked content.
const contentItem object.Name = "content item"

// iso32005Children is Table 5's Children column, by parent type.
var iso32005Children = map[object.Name]map[object.Name]inclusion{
	"StructTreeRoot": {
		"Document": incExactlyOne,
	},
	"Document": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny,
		"Private": incAny, "P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne,
		"Title": incAny, "Link": incAny, "Annot": incAny, "Form": incAny, "FENote": incAny,
		"Index": incAny, "L": incAny, "Table": incAny, "Figure": incAny, "Formula": incAny,
		"Artifact": incAny,
	},
	"DocumentFragment": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny,
		"Private": incAny, "P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne,
		"Title": incAny, "Link": incAny, "Annot": incAny, "Form": incAny, "FENote": incAny,
		"Index": incAny, "L": incAny, "Table": incAny, "Figure": incAny, "Formula": incAny,
		"Artifact": incAny,
	},
	"Part": {
		"Document": incDescribed, "DocumentFragment": incDescribed, "Part": incDescribed,
		"Art": incDescribed, "Div": incDescribed, "Sect": incDescribed, "TOC": incDescribed,
		"TOCI": incDescribed, "Aside": incDescribed, "BlockQuote": incDescribed,
		"NonStruct": incDescribed, "Private": incDescribed, "P": incDescribed, "Note": incDescribed,
		"Code": incDescribed, "Hn": incDescribed, "H": incDescribed, "Title": incDescribed,
		"Sub": incDescribed, "Lbl": incDescribed, "Link": incDescribed, "Reference": incDescribed,
		"Annot": incDescribed, "Form": incDescribed, "FENote": incDescribed, "Index": incDescribed,
		"L": incDescribed, "BibEntry": incDescribed, "Table": incDescribed, "Caption": incDescribed,
		"Figure": incDescribed, "Formula": incDescribed, "Artifact": incDescribed,
	},
	"Div": {
		"Document": incDescribed, "DocumentFragment": incDescribed, "Part": incDescribed,
		"Art": incDescribed, "Div": incDescribed, "Sect": incDescribed, "TOC": incDescribed,
		"TOCI": incDescribed, "Aside": incDescribed, "BlockQuote": incDescribed,
		"NonStruct": incDescribed, "Private": incDescribed, "P": incDescribed, "Note": incDescribed,
		"Code": incDescribed, "Hn": incDescribed, "H": incDescribed, "Title": incDescribed,
		"Sub": incDescribed, "Lbl": incDescribed, "Em": incDescribed, "Strong": incDescribed,
		"Span": incDescribed, "Quote": incDescribed, "Link": incDescribed, "Reference": incDescribed,
		"Annot": incDescribed, "Form": incDescribed, "Ruby": incDescribed, "RB": incDescribed,
		"RT": incDescribed, "RP": incDescribed, "Warichu": incDescribed, "WT": incDescribed,
		"WP": incDescribed, "FENote": incDescribed, "Index": incDescribed, "L": incDescribed,
		"LI": incDescribed, "LBody": incDescribed, "BibEntry": incDescribed, "Table": incDescribed,
		"TR": incDescribed, "TH": incDescribed, "TD": incDescribed, "THead": incDescribed,
		"TBody": incDescribed, "TFoot": incDescribed, "Caption": incDescribed, "Figure": incDescribed,
		"Formula": incDescribed, "Artifact": incDescribed,
	},
	"Art": {
		"DocumentFragment": incAny, "Part": incAny, "Div": incAny, "Sect": incAny, "TOC": incAny,
		"Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny,
		"Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Title": incAny, "Lbl": incAny,
		"Link": incAny, "Annot": incAny, "Form": incAny, "FENote": incAny, "Index": incAny, "L": incAny,
		"Table": incAny, "Caption": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
	},
	"Sect": {
		"DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny,
		"TOC": incAny, "Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Title": incAny,
		"Lbl": incAny, "Link": incAny, "Annot": incAny, "Form": incAny, "FENote": incAny,
		"Index": incAny, "L": incAny, "Table": incAny, "Caption": incAny, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny,
	},
	"TOC": {
		"Part": incAny, "TOC": incAny, "TOCI": incAny, "NonStruct": incAny, "Private": incAny,
		"Caption": incAtMostOne, "Artifact": incAny,
	},
	"TOCI": {
		"Div": incAny, "TOC": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny, "Lbl": incAny,
		"Reference": incAny, "Artifact": incAny,
	},
	"Aside": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Lbl": incAny,
		"Link": incAny, "Reference": incAny, "Annot": incAny, "Form": incAny, "FENote": incAny,
		"Index": incAny, "L": incAny, "Table": incAny, "Caption": incAtMostOne, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"BlockQuote": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Title": incAny,
		"Lbl": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny, "Form": incAny,
		"FENote": incAny, "Index": incAny, "L": incAny, "Table": incAny, "Caption": incAtMostOne,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"NonStruct": {
		"Document": incDescribed, "DocumentFragment": incDescribed, "Part": incDescribed,
		"Art": incDescribed, "Div": incDescribed, "Sect": incDescribed, "TOC": incDescribed,
		"TOCI": incDescribed, "Aside": incDescribed, "BlockQuote": incDescribed,
		"NonStruct": incDescribed, "Private": incDescribed, "P": incDescribed, "Note": incDescribed,
		"Code": incDescribed, "Hn": incDescribed, "H": incDescribed, "Title": incDescribed,
		"Sub": incDescribed, "Lbl": incDescribed, "Em": incDescribed, "Strong": incDescribed,
		"Span": incDescribed, "Quote": incDescribed, "Link": incDescribed, "Reference": incDescribed,
		"Annot": incDescribed, "Form": incDescribed, "Ruby": incDescribed, "RB": incDescribed,
		"RT": incDescribed, "RP": incDescribed, "Warichu": incDescribed, "WT": incDescribed,
		"WP": incDescribed, "FENote": incDescribed, "Index": incDescribed, "L": incDescribed,
		"LI": incDescribed, "LBody": incDescribed, "BibEntry": incDescribed, "Table": incDescribed,
		"TR": incDescribed, "TH": incDescribed, "TD": incDescribed, "THead": incDescribed,
		"TBody": incDescribed, "TFoot": incDescribed, "Caption": incDescribed, "Figure": incDescribed,
		"Formula": incDescribed, "Artifact": incDescribed, contentItem: incDescribed,
	},
	"Private": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "TOCI": incAny, "Aside": incAny, "BlockQuote": incAny,
		"NonStruct": incAny, "Private": incAny, "P": incAny, "Note": incAny, "Code": incAny,
		"Hn": incAny, "H": incAny, "Title": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny,
		"Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny,
		"Annot": incAny, "Form": incAny, "Ruby": incAny, "RB": incAny, "RT": incAny, "RP": incAny,
		"Warichu": incAny, "WT": incAny, "WP": incAny, "FENote": incAny, "Index": incAny, "L": incAny,
		"LI": incAny, "LBody": incAny, "BibEntry": incAny, "Table": incAny, "TR": incAny, "TH": incAny,
		"TD": incAny, "THead": incAny, "TBody": incAny, "TFoot": incAny, "Caption": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Title": {
		"Part": incAny, "Div": incAny, "Aside": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "L": incAny,
		"BibEntry": incAny, "Table": incAny, "Caption": incAtMostOne, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Sub": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Lbl": incAny,
		"Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "L": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny,
		"Artifact": incAny, contentItem: incAny,
	},
	"P": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "L": incAny, "BibEntry": incAny, "Table": incAny, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Note": {
		"DocumentFragment": incGrouping, "Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny,
		"Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny,
		"Note": incAny, "Code": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "Index": incAny,
		"L": incAny, "BibEntry": incAny, "Table": incAny, "Caption": incGrouping, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Code": {
		"DocumentFragment": incGrouping, "Part": incAny, "Div": incAny, "NonStruct": incAny,
		"Private": incAny, "Note": incAny, "Em": incAny, "Strong": incAny, "Span": incAny,
		"Link": incAny, "Reference": incAny, "Annot": incAny, "FENote": incAny, "BibEntry": incAny,
		"Artifact": incAny, contentItem: incAny,
	},
	"Hn": {
		"Art": incAtMostOne, "Sect": incAtMostOne, "NonStruct": incAny, "Private": incAny,
		"Note": incAny, "Code": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "BibEntry": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"H": {
		"Art": incAtMostOne, "Sect": incAtMostOne, "NonStruct": incAny, "Private": incAny,
		"Note": incAny, "Code": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "BibEntry": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Lbl": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Em": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Strong": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Span": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Quote": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Code": incAny, "Sub": incAny,
		"Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "BibEntry": incAny, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Link": {
		"DocumentFragment": incGrouping, "Part": incGrouping, "Art": incAny, "Div": incAny,
		"Sect": incAny, "Aside": incGrouping, "BlockQuote": incGrouping, "NonStruct": incAny,
		"Private": incAny, "P": incGrouping, "Note": incAny, "Code": incAny, "Hn": incGrouping,
		"H": incGrouping, "Title": incGrouping, "Sub": incAny, "Lbl": incAny, "Em": incAny,
		"Strong": incAny, "Span": incAny, "Quote": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incGrouping, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "L": incGrouping,
		"BibEntry": incAny, "Table": incGrouping, "Caption": incGrouping, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Reference": {
		"NonStruct": incAny, "Private": incAny, "Note": incAny, "Lbl": incAny, "Em": incAny,
		"Strong": incAny, "Span": incAny, "Link": incAny, "Annot": incAny, "FENote": incAny,
		"BibEntry": incAny, "Figure": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Annot": {
		"DocumentFragment": incGrouping, "Part": incGrouping, "Art": incAny, "Div": incAny,
		"Sect": incAny, "Aside": incGrouping, "BlockQuote": incGrouping, "NonStruct": incAny,
		"P": incGrouping, "Note": incAny, "Code": incAny, "Hn": incGrouping, "H": incGrouping,
		"Title": incGrouping, "Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incGrouping, "Annot": incAny,
		"Form": incGrouping, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "L": incGrouping,
		"BibEntry": incAny, "Table": incGrouping, "Caption": incGrouping, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Form": {
		"Part": incGrouping, "Div": incAny, "NonStruct": incAny, "Private": incAny, "Note": incAny,
		"Code": incGrouping, "Lbl": incAny, "Reference": incGrouping, "FENote": incAny, "L": incGrouping,
		"BibEntry": incAny, "Table": incGrouping, "Caption": incAtMostOne, "Figure": incGrouping,
		"Formula": incGrouping, "Artifact": incAny, contentItem: incAny,
	},
	"Ruby": {
		"NonStruct": incAny, "Private": incAny, "RB": incDescribed, "RT": incDescribed,
		"RP": incDescribed, contentItem: incAny,
	},
	"RB": {
		"NonStruct": incAny, "Private": incAny, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Artifact": incAny, contentItem: incDescribed,
	},
	"RT": {
		"NonStruct": incAny, "Private": incAny, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Artifact": incAny, contentItem: incDescribed,
	},
	"RP": {
		"NonStruct": incAny, "Private": incAny, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Artifact": incAny, contentItem: incDescribed,
	},
	"Warichu": {
		"NonStruct": incAny, "Private": incAny, "WT": incDescribed, "WP": incDescribed,
		contentItem: incAny,
	},
	"WT": {
		"NonStruct": incAny, "Private": incAny, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Artifact": incAny, contentItem: incDescribed,
	},
	"WP": {
		"NonStruct": incAny, "Private": incAny, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Artifact": incAny, contentItem: incDescribed,
	},
	"FENote": {
		"DocumentFragment": incGrouping, "Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny,
		"Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny,
		"Note": incAny, "Code": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "L": incAny,
		"Table": incAny, "Caption": incGrouping, "Figure": incAny, "Formula": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Index": {
		"Part": incAny, "Div": incAny, "Sect": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Hn": incAny, "H": incAny, "Reference": incAny, "Annot": incAny,
		"FENote": incAny, "L": incAny, "Table": incAny, "Caption": incAny, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny,
	},
	"L": {
		"NonStruct": incAny, "Private": incAny, "L": incAny, "LI": incAny, "Caption": incAtMostOne,
		"Artifact": incAny,
	},
	"LI": {
		"Div": incAny, "NonStruct": incAny, "Private": incAny, "Lbl": incAny, "LBody": incAny,
		"Artifact": incAny, contentItem: incAny,
	},
	"LBody": {
		"Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny, "Aside": incAny,
		"BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny, "Note": incAny,
		"Code": incAny, "Hn": incAny, "H": incAtMostOne, "Sub": incAny, "Em": incAny, "Strong": incAny,
		"Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny, "Annot": incAny,
		"Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny, "Index": incAny,
		"L": incAny, "BibEntry": incAny, "Table": incAny, "Caption": incAtMostOne, "Figure": incAny,
		"Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"BibEntry": {
		"Part": incAny, "Div": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny,
		"Note": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "FENote": incAny, "Figure": incAny, "Artifact": incAny,
		contentItem: incAny,
	},
	"Table": {
		"NonStruct": incAny, "Private": incAny, "TR": incAny, "THead": incAtMostOne, "TBody": incAny,
		"TFoot": incAtMostOne, "Caption": incAtMostOne, "Artifact": incAny,
	},
	"TR": {
		"NonStruct": incAny, "Private": incAny, "TH": incAny, "TD": incAny, "Artifact": incAny,
	},
	"TH": {
		"Art": incAny, "Div": incAny, "Sect": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Lbl": incAny,
		"Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "Index": incAny, "L": incAny, "BibEntry": incAny, "Table": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"TD": {
		"Art": incAny, "Div": incAny, "Sect": incAny, "NonStruct": incAny, "Private": incAny,
		"P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Lbl": incAny,
		"Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "Index": incAny, "L": incAny, "BibEntry": incAny, "Table": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"THead": {
		"NonStruct": incAny, "Private": incAny, "TR": incAny, "Artifact": incAny,
	},
	"TBody": {
		"NonStruct": incAny, "Private": incAny, "TR": incAny, "Artifact": incAny,
	},
	"TFoot": {
		"NonStruct": incAny, "Private": incAny, "TR": incAny, "Artifact": incAny,
	},
	"Caption": {
		"DocumentFragment": incGrouping, "Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny,
		"Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny,
		"Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne, "Sub": incAny, "Lbl": incAny,
		"Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny,
		"Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny,
		"FENote": incAny, "Index": incAny, "L": incAny, "BibEntry": incAny, "Table": incAny,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Figure": {
		"Part": incAny, "Art": incAny, "Div": incAny, "Sect": incAny, "Aside": incAny,
		"BlockQuote": incAny, "NonStruct": incAny, "Private": incAny, "P": incAny, "Note": incAny,
		"Code": incAny, "Hn": incAny, "H": incAtMostOne, "Sub": incGrouping, "Lbl": incAny, "Em": incAny,
		"Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny,
		"Annot": incAny, "Form": incAny, "Ruby": incAny, "Warichu": incAny, "FENote": incAny,
		"Index": incAny, "L": incAny, "BibEntry": incAny, "Table": incAny, "Caption": incAtMostOne,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
	"Formula": {
		"Part": incAny, "Div": incAny, "Aside": incAny, "BlockQuote": incAny, "NonStruct": incAny,
		"Private": incAny, "P": incAny, "Note": incAny, "Code": incAny, "Hn": incAny, "H": incAtMostOne,
		"Sub": incAny, "Lbl": incAny, "Em": incAny, "Strong": incAny, "Span": incAny, "Quote": incAny,
		"Link": incAny, "Reference": incAny, "Annot": incAny, "Form": incAny, "Ruby": incAny,
		"Warichu": incAny, "FENote": incAny, "Index": incAny, "L": incAny, "BibEntry": incAny,
		"Table": incAny, "Caption": incAtMostOne, "Figure": incAny, "Formula": incAny,
		"Artifact": incAny, contentItem: incAny,
	},
	"Artifact": {
		"Document": incAny, "DocumentFragment": incAny, "Part": incAny, "Art": incAny, "Div": incAny,
		"Sect": incAny, "TOC": incAny, "TOCI": incAny, "Aside": incAny, "BlockQuote": incAny,
		"NonStruct": incAny, "Private": incAny, "P": incAny, "Note": incAny, "Code": incAny,
		"Hn": incAny, "H": incAtMostOne, "Title": incAny, "Sub": incAny, "Lbl": incAny, "Em": incAny,
		"Strong": incAny, "Span": incAny, "Quote": incAny, "Link": incAny, "Reference": incAny,
		"Annot": incAny, "Form": incAny, "Ruby": incAny, "RB": incAny, "RT": incAny, "RP": incAny,
		"Warichu": incAny, "WT": incAny, "WP": incAny, "FENote": incAny, "Index": incAny, "L": incAny,
		"LI": incAny, "LBody": incAny, "BibEntry": incAny, "Table": incAny, "TR": incAny, "TH": incAny,
		"TD": incAny, "THead": incAny, "TBody": incAny, "TFoot": incAny, "Caption": incAtMostOne,
		"Figure": incAny, "Formula": incAny, "Artifact": incAny, contentItem: incAny,
	},
}

// iso32005Unenforced are Table 5 rules pdf0 does not report: relationships
// the published table (ISO/TS 32005:2023) does not list, which veraPDF's
// PDF/UA-2 profile permits and conforming documents use — a Code element
// holding its lines as Sub elements, as the WTPDF examples do, a Reference
// holding block-level content. Where the published table forbids what
// veraPDF permits, pdf0 reports nothing, so that it never fails a file
// veraPDF and the documents it was written for pass. The rules veraPDF has that the
// published table does not are not implemented
// (TestISO32005TableAgreesWithVeraPDF lists both).
var iso32005Unenforced = map[string]bool{
	"Annot-Private":     true,
	"Art-Reference":     true,
	"BibEntry-BibEntry": true, "BibEntry-Form": true, "BibEntry-Formula": true,
	"BibEntry-Quote": true, "BibEntry-Ruby": true, "BibEntry-Sub": true, "BibEntry-Warichu": true,
	"Code-Art": true, "Code-Aside": true, "Code-BlockQuote": true, "Code-Caption": true,
	"Code-Code": true, "Code-Figure": true, "Code-Form": true, "Code-Formula": true,
	"Code-Index": true, "Code-L": true, "Code-Lbl": true, "Code-P": true, "Code-Quote": true,
	"Code-Ruby": true, "Code-Sect": true, "Code-Sub": true, "Code-Table": true, "Code-Warichu": true,
	"FENote-BibEntry": true, "FENote-Index": true,
	"Link-Link":     true,
	"Quote-Div":     true,
	"Reference-Art": true, "Reference-Aside": true, "Reference-BlockQuote": true,
	"Reference-Caption": true, "Reference-Code": true, "Reference-Div": true,
	"Reference-DocumentFragment": true, "Reference-Form": true, "Reference-Formula": true,
	"Reference-H": true, "Reference-Hn": true, "Reference-L": true, "Reference-P": true,
	"Reference-Part": true, "Reference-Quote": true, "Reference-Reference": true,
	"Reference-Ruby": true, "Reference-Sect": true, "Reference-Sub": true, "Reference-Table": true,
	"Reference-Title": true, "Reference-Warichu": true,
	"Sect-Reference": true,
	"TOCI-Annot":     true,
	"Note-Caption":   true,
}
