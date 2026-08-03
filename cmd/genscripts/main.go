// Command genscripts generates the Unicode script ranges, and the OpenType
// script tags each script selects, from Unicode's own Scripts.txt and
// PropertyValueAliases.txt.
//
// A font's GSUB and GPOS tables state their rules per script: a Greek run must
// be given the features 'grek' declares and not the ones 'arab' does. Which
// script a character belongs to is Unicode's to say, so it is read from the UCD
// rather than guessed; which four-byte tag OpenType calls that script by is the
// OpenType registry's to say, and is derived here.
//
// The derivation is the one every shaper uses: an OpenType script tag is the
// character's ISO 15924 code with its first letter lowercased — Grek becomes
// 'grek', Deva becomes 'deva'. Only a handful of scripts break that rule, and
// they are listed below. The Indic scripts carry two tags, an older one and a
// second-generation one a font declares when it wants the reordering rules a
// modern shaper applies; the newer is tried first, which is what a shaper does.
//
//	go run ./cmd/genscripts <Scripts.txt> <PropertyValueAliases.txt> > fonts/scripts.go
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strconv"
	"strings"
)

// otTagOverrides names the scripts whose OpenType tag is not their ISO 15924
// code lowercased, and those that carry more than one tag.
//
// The key is the Unicode script's long name, and the generator fails if one is
// not in the data — so a script renamed or removed upstream is noticed here
// rather than silently losing its tag.
var otTagOverrides = map[string][]string{
	// OpenType has one tag for the two Japanese kana scripts, and it is
	// Katakana's. Hiragana's own code, 'hira', names nothing in any font.
	"Hiragana": {"kana"},

	// ISO 15924 pads a short code with nothing; OpenType pads it with spaces,
	// because a tag is always four bytes.
	"Lao": {"lao "},
	"Nko": {"nko "},
	"Vai": {"vai "},
	"Yi":  {"yi  "},

	// The Indic scripts each have a second-generation tag. A font declares it
	// to say its rules are written for a shaper that reorders, which the
	// package does for Devanagari and for no other; but the tag is the one such
	// a font declares its features under whatever the shaper can do with them,
	// so it is tried first and the older one after.
	"Bengali":    {"bng2", "beng"},
	"Devanagari": {"dev2", "deva"},
	"Gujarati":   {"gjr2", "gujr"},
	"Gurmukhi":   {"gur2", "guru"},
	"Kannada":    {"knd2", "knda"},
	"Malayalam":  {"mlm2", "mlym"},
	"Myanmar":    {"mym2", "mymr"},
	"Oriya":      {"ory2", "orya"},
	"Tamil":      {"tml2", "taml"},
	"Telugu":     {"tel2", "telu"},
}

// noTag are the scripts that select no tag of their own: they are not scripts a
// font declares rules for. Text in them takes the default script, which is what
// a run of digits and punctuation should get.
var noTag = map[string]bool{"Common": true, "Inherited": true, "Unknown": true}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: genscripts <Scripts.txt> <PropertyValueAliases.txt>")
		os.Exit(2)
	}
	version, ranges := readScripts(os.Args[1])
	codes := readAliases(os.Args[2])

	// Every script the ranges name, in a stable order, with Common, Inherited
	// and Unknown first so the reader can name them as constants.
	names := map[string]bool{}
	for _, r := range ranges {
		names[r.name] = true
	}
	for n := range noTag {
		names[n] = true
	}
	ordered := []string{"Common", "Inherited", "Unknown"}
	rest := make([]string, 0, len(names))
	for n := range names {
		if !noTag[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	ordered = append(ordered, rest...)

	index := map[string]int{}
	for i, n := range ordered {
		index[n] = i
	}

	// Tags per script, with the overrides checked against the data.
	for n := range otTagOverrides {
		if !names[n] {
			fmt.Fprintf(os.Stderr, "genscripts: override names script %q, which the data does not have\n", n)
			os.Exit(1)
		}
	}
	tags := make([][]string, len(ordered))
	for i, n := range ordered {
		switch {
		case noTag[n]:
			tags[i] = nil
		case otTagOverrides[n] != nil:
			tags[i] = otTagOverrides[n]
		default:
			code, ok := codes[n]
			if !ok {
				fmt.Fprintf(os.Stderr, "genscripts: no ISO 15924 code for script %q\n", n)
				os.Exit(1)
			}
			tags[i] = []string{strings.ToLower(code[:1]) + code[1:]}
		}
	}

	// Collapse to ranges. A script runs in long blocks, so a range table is a
	// couple of thousand entries where a per-character map would be a million.
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].lo < ranges[j].lo })
	var merged []scriptRange
	for _, r := range ranges {
		if n := len(merged); n > 0 && merged[n-1].name == r.name && merged[n-1].hi+1 == r.lo {
			merged[n-1].hi = r.hi
			continue
		}
		merged = append(merged, r)
	}

	// The table is written into a buffer and formatted before it is emitted, so
	// that the committed file is gofmt-clean however the generator was run.
	w := &bytes.Buffer{}
	fmt.Fprintf(w, `// Code generated by cmd/genscripts from Unicode's Scripts.txt and
// PropertyValueAliases.txt. DO NOT EDIT.

package fonts

// Unicode scripts, and the OpenType script tags each one selects. Unicode %s.
//
// A font states its layout rules per script, so shaping a run has to know which
// script the run is in — that is Unicode's Script property, and these %d ranges
// are it. A character no range names is of unknown script, which selects the
// default tag, as do Common and Inherited: a digit, a space and a combining
// accent are not text in a script of their own and take the script of what they
// are written among.
//
// A script's tags are in the order a shaper tries them, which matters only for
// the Indic scripts, where the second-generation tag comes first.

type scriptRange struct {
	lo, hi rune
	script uint16
}

// The three scripts that decide nothing, at fixed indices so the resolver can
// name them.
const (
	scriptCommon    = 0
	scriptInherited = 1
	scriptUnknown   = 2
)

// scriptOpenTypeTags gives the tags each script selects, indexed as
// scriptRanges indexes scripts. A nil entry selects no tag of its own.
var scriptOpenTypeTags = [...][]string{
`, version, len(merged))
	for i, n := range ordered {
		if tags[i] == nil {
			fmt.Fprintf(w, "\t%d: nil, // %s\n", i, n)
			continue
		}
		quoted := make([]string, len(tags[i]))
		for k, t := range tags[i] {
			quoted[k] = strconv.Quote(t)
		}
		fmt.Fprintf(w, "\t%d: {%s}, // %s\n", i, strings.Join(quoted, ", "), n)
	}
	fmt.Fprint(w, "}\n\n// scriptRanges maps a character to its script, sorted by code point.\nvar scriptRanges = [...]scriptRange{\n")
	for _, r := range merged {
		fmt.Fprintf(w, "\t{0x%04X, 0x%04X, %d}, // %s\n", r.lo, r.hi, index[r.name], r.name)
	}
	fmt.Fprintln(w, "}")

	src, err := format.Source(w.Bytes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "genscripts:", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(src); err != nil {
		fmt.Fprintln(os.Stderr, "genscripts:", err)
		os.Exit(1)
	}
}

type scriptRange struct {
	lo, hi rune
	name   string
}

// readScripts parses Scripts.txt, returning the Unicode version it declares and
// one entry per range it lists.
func readScripts(path string) (string, []scriptRange) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	version := "unknown"
	var out []scriptRange
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			// The first line names the file, and with it the version:
			// "# Scripts-17.0.0.txt".
			first = false
			if i := strings.Index(line, "Scripts-"); i >= 0 {
				if j := strings.Index(line[i:], ".txt"); j > 0 {
					version = line[i+len("Scripts-") : i+j]
				}
			}
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) < 2 {
			continue
		}
		lo, hi, ok := parseRange(strings.TrimSpace(fields[0]))
		if !ok {
			continue
		}
		out = append(out, scriptRange{lo: lo, hi: hi, name: strings.TrimSpace(fields[1])})
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return version, out
}

// parseRange reads "0041..005A" or "0041".
func parseRange(s string) (rune, rune, bool) {
	lo, hi := s, s
	if i := strings.Index(s, ".."); i >= 0 {
		lo, hi = s[:i], s[i+2:]
	}
	a, err := strconv.ParseUint(lo, 16, 32)
	if err != nil {
		return 0, 0, false
	}
	b, err := strconv.ParseUint(hi, 16, 32)
	if err != nil {
		return 0, 0, false
	}
	return rune(a), rune(b), true
}

// readAliases parses the sc property of PropertyValueAliases.txt: the ISO 15924
// code for each script's long name.
func readAliases(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Split(line, ";")
		if len(fields) < 3 || strings.TrimSpace(fields[0]) != "sc" {
			continue
		}
		out[strings.TrimSpace(fields[2])] = strings.TrimSpace(fields[1])
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return out
}
