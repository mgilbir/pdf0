package iso32005

import "github.com/mgilbir/pdf0/object"

// ISO/TS 32005:2023, Table 5: the parent-child relationships between the
// standard structure elements of the PDF 1.7 and PDF 2.0 namespaces. A PDF
// 1.7 or PDF 2.0 element shall have no child of those namespaces that its
// row does not list (7.2). This is pdf0's encoding of the Children column,
// written from the published Technical Specification; Hn stands for H1, H2
// and so on, and ContentItem for marked content, as in the standard. The
// Parents column states the same relationships from the other side, and is
// not repeated. Where both parent and child are PDF 2.0 elements, the table
// is ISO 32000-2 Annex L's too, as its errata of July 2024 give it: the one
// pair the two disagree on, a Figure in a WP, is the errata's.
//
// TestISO32005TableAgreesWithAnnexL compares the table with Annex L's
// machine-readable matrix, read from a copy of ISO 32000-2 named by
// PDF0_ISO32000_2, when there is one.
//
// TestISO32005TableAgreesWithVeraPDF compares the rules this table implies
// with veraPDF's PDF/UA-2 profile, which also states them, where the profile
// is present (make verapdf-profiles), and lists where the two differ.

// Inclusion is a child type's valid usage in a row (Table 4).
type Inclusion uint8

const (
	Any        Inclusion = iota + 1 // 0..n
	AtMostOne                       // 0..1
	ExactlyOne                      // 1
	Grouping                        // ø*: only where the parent is used as a grouping element
	Described                       // ‡, [a], [b]: the type's own description says
)

// ContentItem is the Children column's "content item": marked content.
const ContentItem object.Name = "content item"

// Children is Table 5's Children column, by parent type.
var Children = map[object.Name]map[object.Name]Inclusion{
	"StructTreeRoot": {
		"Document": ExactlyOne,
	},
	"Document": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "Aside": Any, "BlockQuote": Any, "NonStruct": Any,
		"Private": Any, "P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne,
		"Title": Any, "Link": Any, "Annot": Any, "Form": Any, "FENote": Any,
		"Index": Any, "L": Any, "Table": Any, "Figure": Any, "Formula": Any,
		"Artifact": Any,
	},
	"DocumentFragment": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "Aside": Any, "BlockQuote": Any, "NonStruct": Any,
		"Private": Any, "P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne,
		"Title": Any, "Link": Any, "Annot": Any, "Form": Any, "FENote": Any,
		"Index": Any, "L": Any, "Table": Any, "Figure": Any, "Formula": Any,
		"Artifact": Any,
	},
	"Part": {
		"Document": Described, "DocumentFragment": Described, "Part": Described,
		"Art": Described, "Div": Described, "Sect": Described, "TOC": Described,
		"TOCI": Described, "Aside": Described, "BlockQuote": Described,
		"NonStruct": Described, "Private": Described, "P": Described, "Note": Described,
		"Code": Described, "Hn": Described, "H": Described, "Title": Described,
		"Sub": Described, "Lbl": Described, "Link": Described, "Reference": Described,
		"Annot": Described, "Form": Described, "FENote": Described, "Index": Described,
		"L": Described, "BibEntry": Described, "Table": Described, "Caption": Described,
		"Figure": Described, "Formula": Described, "Artifact": Described,
	},
	"Div": {
		"Document": Described, "DocumentFragment": Described, "Part": Described,
		"Art": Described, "Div": Described, "Sect": Described, "TOC": Described,
		"TOCI": Described, "Aside": Described, "BlockQuote": Described,
		"NonStruct": Described, "Private": Described, "P": Described, "Note": Described,
		"Code": Described, "Hn": Described, "H": Described, "Title": Described,
		"Sub": Described, "Lbl": Described, "Em": Described, "Strong": Described,
		"Span": Described, "Quote": Described, "Link": Described, "Reference": Described,
		"Annot": Described, "Form": Described, "Ruby": Described, "RB": Described,
		"RT": Described, "RP": Described, "Warichu": Described, "WT": Described,
		"WP": Described, "FENote": Described, "Index": Described, "L": Described,
		"LI": Described, "LBody": Described, "BibEntry": Described, "Table": Described,
		"TR": Described, "TH": Described, "TD": Described, "THead": Described,
		"TBody": Described, "TFoot": Described, "Caption": Described, "Figure": Described,
		"Formula": Described, "Artifact": Described,
	},
	"Art": {
		"DocumentFragment": Any, "Part": Any, "Div": Any, "Sect": Any, "TOC": Any,
		"Aside": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any,
		"Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Title": Any, "Lbl": Any,
		"Link": Any, "Annot": Any, "Form": Any, "FENote": Any, "Index": Any, "L": Any,
		"Table": Any, "Caption": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
	},
	"Sect": {
		"DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any, "Sect": Any,
		"TOC": Any, "Aside": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Title": Any,
		"Lbl": Any, "Link": Any, "Annot": Any, "Form": Any, "FENote": Any,
		"Index": Any, "L": Any, "Table": Any, "Caption": Any, "Figure": Any,
		"Formula": Any, "Artifact": Any,
	},
	"TOC": {
		"Part": Any, "TOC": Any, "TOCI": Any, "NonStruct": Any, "Private": Any,
		"Caption": AtMostOne, "Artifact": Any,
	},
	"TOCI": {
		"Div": Any, "TOC": Any, "NonStruct": Any, "Private": Any, "P": Any, "Lbl": Any,
		"Reference": Any, "Artifact": Any,
	},
	"Aside": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Lbl": Any,
		"Link": Any, "Reference": Any, "Annot": Any, "Form": Any, "FENote": Any,
		"Index": Any, "L": Any, "Table": Any, "Caption": AtMostOne, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"BlockQuote": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Title": Any,
		"Lbl": Any, "Link": Any, "Reference": Any, "Annot": Any, "Form": Any,
		"FENote": Any, "Index": Any, "L": Any, "Table": Any, "Caption": AtMostOne,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"NonStruct": {
		"Document": Described, "DocumentFragment": Described, "Part": Described,
		"Art": Described, "Div": Described, "Sect": Described, "TOC": Described,
		"TOCI": Described, "Aside": Described, "BlockQuote": Described,
		"NonStruct": Described, "Private": Described, "P": Described, "Note": Described,
		"Code": Described, "Hn": Described, "H": Described, "Title": Described,
		"Sub": Described, "Lbl": Described, "Em": Described, "Strong": Described,
		"Span": Described, "Quote": Described, "Link": Described, "Reference": Described,
		"Annot": Described, "Form": Described, "Ruby": Described, "RB": Described,
		"RT": Described, "RP": Described, "Warichu": Described, "WT": Described,
		"WP": Described, "FENote": Described, "Index": Described, "L": Described,
		"LI": Described, "LBody": Described, "BibEntry": Described, "Table": Described,
		"TR": Described, "TH": Described, "TD": Described, "THead": Described,
		"TBody": Described, "TFoot": Described, "Caption": Described, "Figure": Described,
		"Formula": Described, "Artifact": Described, ContentItem: Described,
	},
	"Private": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "TOCI": Any, "Aside": Any, "BlockQuote": Any,
		"NonStruct": Any, "Private": Any, "P": Any, "Note": Any, "Code": Any,
		"Hn": Any, "H": Any, "Title": Any, "Sub": Any, "Lbl": Any, "Em": Any,
		"Strong": Any, "Span": Any, "Quote": Any, "Link": Any, "Reference": Any,
		"Annot": Any, "Form": Any, "Ruby": Any, "RB": Any, "RT": Any, "RP": Any,
		"Warichu": Any, "WT": Any, "WP": Any, "FENote": Any, "Index": Any, "L": Any,
		"LI": Any, "LBody": Any, "BibEntry": Any, "Table": Any, "TR": Any, "TH": Any,
		"TD": Any, "THead": Any, "TBody": Any, "TFoot": Any, "Caption": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Title": {
		"Part": Any, "Div": Any, "Aside": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "L": Any,
		"BibEntry": Any, "Table": Any, "Caption": AtMostOne, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Sub": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Lbl": Any,
		"Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "L": Any, "BibEntry": Any, "Figure": Any, "Formula": Any,
		"Artifact": Any, ContentItem: Any,
	},
	"P": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "L": Any, "BibEntry": Any, "Table": Any, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Note": {
		"DocumentFragment": Grouping, "Part": Any, "Art": Any, "Div": Any, "Sect": Any,
		"Aside": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any,
		"Note": Any, "Code": Any, "Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "Index": Any,
		"L": Any, "BibEntry": Any, "Table": Any, "Caption": Grouping, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Code": {
		"DocumentFragment": Grouping, "Part": Any, "Div": Any, "NonStruct": Any,
		"Private": Any, "Note": Any, "Em": Any, "Strong": Any, "Span": Any,
		"Link": Any, "Reference": Any, "Annot": Any, "FENote": Any, "BibEntry": Any,
		"Artifact": Any, ContentItem: Any,
	},
	"Hn": {
		"Art": AtMostOne, "Sect": AtMostOne, "NonStruct": Any, "Private": Any,
		"Note": Any, "Code": Any, "Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "BibEntry": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"H": {
		"Art": AtMostOne, "Sect": AtMostOne, "NonStruct": Any, "Private": Any,
		"Note": Any, "Code": Any, "Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "BibEntry": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Lbl": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "BibEntry": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Em": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "BibEntry": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Strong": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "BibEntry": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Span": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "BibEntry": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Quote": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Code": Any, "Sub": Any,
		"Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "BibEntry": Any, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Link": {
		"DocumentFragment": Grouping, "Part": Grouping, "Art": Any, "Div": Any,
		"Sect": Any, "Aside": Grouping, "BlockQuote": Grouping, "NonStruct": Any,
		"Private": Any, "P": Grouping, "Note": Any, "Code": Any, "Hn": Grouping,
		"H": Grouping, "Title": Grouping, "Sub": Any, "Lbl": Any, "Em": Any,
		"Strong": Any, "Span": Any, "Quote": Any, "Reference": Any, "Annot": Any,
		"Form": Grouping, "Ruby": Any, "Warichu": Any, "FENote": Any, "L": Grouping,
		"BibEntry": Any, "Table": Grouping, "Caption": Grouping, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Reference": {
		"NonStruct": Any, "Private": Any, "Note": Any, "Lbl": Any, "Em": Any,
		"Strong": Any, "Span": Any, "Link": Any, "Annot": Any, "FENote": Any,
		"BibEntry": Any, "Figure": Any, "Artifact": Any, ContentItem: Any,
	},
	"Annot": {
		"DocumentFragment": Grouping, "Part": Grouping, "Art": Any, "Div": Any,
		"Sect": Any, "Aside": Grouping, "BlockQuote": Grouping, "NonStruct": Any,
		"P": Grouping, "Note": Any, "Code": Any, "Hn": Grouping, "H": Grouping,
		"Title": Grouping, "Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Grouping, "Annot": Any,
		"Form": Grouping, "Ruby": Any, "Warichu": Any, "FENote": Any, "L": Grouping,
		"BibEntry": Any, "Table": Grouping, "Caption": Grouping, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Form": {
		"Part": Grouping, "Div": Any, "NonStruct": Any, "Private": Any, "Note": Any,
		"Code": Grouping, "Lbl": Any, "Reference": Grouping, "FENote": Any, "L": Grouping,
		"BibEntry": Any, "Table": Grouping, "Caption": AtMostOne, "Figure": Grouping,
		"Formula": Grouping, "Artifact": Any, ContentItem: Any,
	},
	"Ruby": {
		"NonStruct": Any, "Private": Any, "RB": Described, "RT": Described,
		"RP": Described, ContentItem: Any,
	},
	"RB": {
		"NonStruct": Any, "Private": Any, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Artifact": Any, ContentItem: Described,
	},
	"RT": {
		"NonStruct": Any, "Private": Any, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Artifact": Any, ContentItem: Described,
	},
	"RP": {
		"NonStruct": Any, "Private": Any, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Artifact": Any, ContentItem: Described,
	},
	"Warichu": {
		"NonStruct": Any, "Private": Any, "WT": Described, "WP": Described,
		ContentItem: Any,
	},
	"WT": {
		"NonStruct": Any, "Private": Any, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Artifact": Any, ContentItem: Described,
	},
	"WP": {
		"NonStruct": Any, "Private": Any, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Artifact": Any, ContentItem: Described,
		// The published Technical Specification omits it; ISO 32000-2's
		// Annex L, as its errata of July 2024 give it, permits a Figure in
		// a WP, and ISO/TS 32005 7.2 defers to ISO 32000-2 "and associated
		// errata for PDF 2.0 elements".
		"Figure": Any,
	},
	"FENote": {
		"DocumentFragment": Grouping, "Part": Any, "Art": Any, "Div": Any, "Sect": Any,
		"Aside": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any,
		"Note": Any, "Code": Any, "Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "L": Any,
		"Table": Any, "Caption": Grouping, "Figure": Any, "Formula": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Index": {
		"Part": Any, "Div": Any, "Sect": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Hn": Any, "H": Any, "Reference": Any, "Annot": Any,
		"FENote": Any, "L": Any, "Table": Any, "Caption": Any, "Figure": Any,
		"Formula": Any, "Artifact": Any,
	},
	"L": {
		"NonStruct": Any, "Private": Any, "L": Any, "LI": Any, "Caption": AtMostOne,
		"Artifact": Any,
	},
	"LI": {
		"Div": Any, "NonStruct": Any, "Private": Any, "Lbl": Any, "LBody": Any,
		"Artifact": Any, ContentItem: Any,
	},
	"LBody": {
		"Part": Any, "Art": Any, "Div": Any, "Sect": Any, "Aside": Any,
		"BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any, "Note": Any,
		"Code": Any, "Hn": Any, "H": AtMostOne, "Sub": Any, "Em": Any, "Strong": Any,
		"Span": Any, "Quote": Any, "Link": Any, "Reference": Any, "Annot": Any,
		"Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any, "Index": Any,
		"L": Any, "BibEntry": Any, "Table": Any, "Caption": AtMostOne, "Figure": Any,
		"Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"BibEntry": {
		"Part": Any, "Div": Any, "NonStruct": Any, "Private": Any, "P": Any,
		"Note": Any, "Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "FENote": Any, "Figure": Any, "Artifact": Any,
		ContentItem: Any,
	},
	"Table": {
		"NonStruct": Any, "Private": Any, "TR": Any, "THead": AtMostOne, "TBody": Any,
		"TFoot": AtMostOne, "Caption": AtMostOne, "Artifact": Any,
	},
	"TR": {
		"NonStruct": Any, "Private": Any, "TH": Any, "TD": Any, "Artifact": Any,
	},
	"TH": {
		"Art": Any, "Div": Any, "Sect": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Lbl": Any,
		"Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "Index": Any, "L": Any, "BibEntry": Any, "Table": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"TD": {
		"Art": Any, "Div": Any, "Sect": Any, "NonStruct": Any, "Private": Any,
		"P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Lbl": Any,
		"Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "Index": Any, "L": Any, "BibEntry": Any, "Table": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"THead": {
		"NonStruct": Any, "Private": Any, "TR": Any, "Artifact": Any,
	},
	"TBody": {
		"NonStruct": Any, "Private": Any, "TR": Any, "Artifact": Any,
	},
	"TFoot": {
		"NonStruct": Any, "Private": Any, "TR": Any, "Artifact": Any,
	},
	"Caption": {
		"DocumentFragment": Grouping, "Part": Any, "Art": Any, "Div": Any, "Sect": Any,
		"Aside": Any, "BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any,
		"Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne, "Sub": Any, "Lbl": Any,
		"Em": Any, "Strong": Any, "Span": Any, "Quote": Any, "Link": Any,
		"Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any,
		"FENote": Any, "Index": Any, "L": Any, "BibEntry": Any, "Table": Any,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Figure": {
		"Part": Any, "Art": Any, "Div": Any, "Sect": Any, "Aside": Any,
		"BlockQuote": Any, "NonStruct": Any, "Private": Any, "P": Any, "Note": Any,
		"Code": Any, "Hn": Any, "H": AtMostOne, "Sub": Grouping, "Lbl": Any, "Em": Any,
		"Strong": Any, "Span": Any, "Quote": Any, "Link": Any, "Reference": Any,
		"Annot": Any, "Form": Any, "Ruby": Any, "Warichu": Any, "FENote": Any,
		"Index": Any, "L": Any, "BibEntry": Any, "Table": Any, "Caption": AtMostOne,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
	"Formula": {
		"Part": Any, "Div": Any, "Aside": Any, "BlockQuote": Any, "NonStruct": Any,
		"Private": Any, "P": Any, "Note": Any, "Code": Any, "Hn": Any, "H": AtMostOne,
		"Sub": Any, "Lbl": Any, "Em": Any, "Strong": Any, "Span": Any, "Quote": Any,
		"Link": Any, "Reference": Any, "Annot": Any, "Form": Any, "Ruby": Any,
		"Warichu": Any, "FENote": Any, "Index": Any, "L": Any, "BibEntry": Any,
		"Table": Any, "Caption": AtMostOne, "Figure": Any, "Formula": Any,
		"Artifact": Any, ContentItem: Any,
	},
	"Artifact": {
		"Document": Any, "DocumentFragment": Any, "Part": Any, "Art": Any, "Div": Any,
		"Sect": Any, "TOC": Any, "TOCI": Any, "Aside": Any, "BlockQuote": Any,
		"NonStruct": Any, "Private": Any, "P": Any, "Note": Any, "Code": Any,
		"Hn": Any, "H": AtMostOne, "Title": Any, "Sub": Any, "Lbl": Any, "Em": Any,
		"Strong": Any, "Span": Any, "Quote": Any, "Link": Any, "Reference": Any,
		"Annot": Any, "Form": Any, "Ruby": Any, "RB": Any, "RT": Any, "RP": Any,
		"Warichu": Any, "WT": Any, "WP": Any, "FENote": Any, "Index": Any, "L": Any,
		"LI": Any, "LBody": Any, "BibEntry": Any, "Table": Any, "TR": Any, "TH": Any,
		"TD": Any, "THead": Any, "TBody": Any, "TFoot": Any, "Caption": AtMostOne,
		"Figure": Any, "Formula": Any, "Artifact": Any, ContentItem: Any,
	},
}

// Unenforced are Table 5 rules pdf0 does not report: relationships
// the published table (ISO/TS 32005:2023) does not list, which veraPDF's
// PDF/UA-2 profile permits and conforming documents use — a Code element
// holding its lines as Sub elements, as the WTPDF examples do, a Reference
// holding block-level content. Where the published table forbids what
// veraPDF permits, pdf0 reports nothing, so that it never fails a file
// veraPDF and the documents it was written for pass. The rules veraPDF has that the
// published table does not are not implemented
// (TestISO32005TableAgreesWithVeraPDF lists both).
var Unenforced = map[string]bool{
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

// AllDescribed reports whether a row defers every child to the types'
// descriptions (‡): the grouping elements Part, Div and NonStruct.
func AllDescribed(row map[object.Name]Inclusion) bool {
	for _, inc := range row {
		if inc != Described {
			return false
		}
	}
	return true
}
