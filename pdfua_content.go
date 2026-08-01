package pdf0

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/syntax"
)

// This file owns the content-stream side of PDF/UA validation (ISO 14289-1
// 7.1/7.18/7.20): text must sit in a tagged or /Artifact marked-content
// sequence, the two kinds must not nest in one another, a form XObject holding
// an /MCID may be painted only once, and an OBJR-reached annotation must sit
// under the structure type its subtype implies. Each stream is tokenized once
// and the derived facts cached, since several checks read the same content.

// checkUARealContent implements Matterhorn checkpoint 01-005: on every page,
// real content (text) must appear inside a marked-content sequence — either
// tagged (linked to the structure tree) or an Artifact. Text drawn outside any
// marked-content sequence is neither, and is flagged.
//
// It walks each page's content stream tracking marked-content nesting depth from
// BDC/BMC…EMC. (The Artifact/tagged *mis-nesting* conditions 01-003/01-004 need
// to distinguish structure-linked BDC from /OC and property-less BDC, which a
// plain depth count cannot; they are left to the structure-correlation pass.)
func checkUARealContent(d *Document, cat *Dictionary) []UAViolation {
	var v []UAViolation
	for _, pg := range collectPages(d, cat.Get("Pages")) {
		data, key := d.contentBytesAndKey(pg.Dict.Get("Contents"))
		for _, msg := range contentFacts(d, data, key).realMsgs {
			v = append(v, UAViolation{"7.1", msg, pg.ObjNum})
		}
	}
	return v
}

// streamContentFacts holds the facts several UA checks derive from a single
// core.TokenizeContent pass over one content stream: the real-content (7.1)
// violation messages and, for the form-XObject-painting rule (7.20), the
// sequence of XObject names invoked by Do operators. Both depend only on the
// stream bytes, so a stream shared by many containers — or examined by more
// than one check — is tokenized once.
type streamContentFacts struct {
	realMsgs []string // distinct real-content (7.1) messages, in first-seen order
	doNames  []string // the operand name in effect at each Do operator, in order
}

// contentFacts returns the streamContentFacts for content, memoized per content
// stream (key) when a validation cache is present.
func contentFacts(d *Document, content []byte, key *Stream) *streamContentFacts {
	if key != nil {
		if c := d.valCache; c != nil {
			if f, ok := c.pdfua.streamFacts[key]; ok {
				return f
			}
		}
	}
	f := buildContentFacts(d.canceler(), content)
	if key != nil {
		if c := d.valCache; c != nil {
			if c.pdfua.streamFacts == nil {
				c.pdfua.streamFacts = make(map[*Stream]*streamContentFacts)
			}
			c.pdfua.streamFacts[key] = f
		}
	}
	return f
}

func buildContentFacts(cancel core.Canceler, content []byte) *streamContentFacts {
	f := &streamContentFacts{}
	if len(content) == 0 {
		return f
	}
	reported := map[string]bool{}
	report := func(msg string) {
		if !reported[msg] {
			reported[msg] = true
			f.realMsgs = append(f.realMsgs, msg)
		}
	}

	// Classify each marked-content sequence. A BDC/BMC is an Artifact if its tag
	// is /Artifact, tagged (structure-linked) if its properties carry an /MCID,
	// and otherwise transparent (/OC optional content, or property-less) — which
	// participates in nesting balance but not in the mis-nesting rules.
	const (
		mcTransparent = iota
		mcArtifact
		mcTagged
	)
	var stack []int
	hasAncestor := func(kind int) bool {
		for _, s := range stack {
			if s == kind {
				return true
			}
		}
		return false
	}
	// Only three things about an operator's operands matter here, so they are
	// tracked as the tokens stream past rather than buffered into a slice: is
	// the first operand the name /Artifact, is any operand the name /MCID, and
	// what was the most recent name operand (the Do target — which, unlike the
	// other two, deliberately carries across operators). Buffering every operand
	// meant copying a token struct for the great majority of tokens in the
	// stream, none of which is ever read back.
	firstOperand := true
	artifactTag := false // the first operand is the name /Artifact
	mcidProp := false    // some operand is the name /MCID
	var lastName string  // most recent name operand (for the Do-operator target)
	for tk := range core.TokenizeContent(cancel, content) {
		if tk.Kind != core.KindOp {
			if tk.Kind == core.KindName {
				lastName = tk.Name
				if firstOperand && tk.Name == "Artifact" {
					artifactTag = true
				}
				if tk.Name == "MCID" {
					mcidProp = true
				}
			}
			firstOperand = false
			continue
		}
		switch tk.Op {
		case "BDC", "BMC":
			kind := mcTransparent
			switch {
			case artifactTag:
				kind = mcArtifact
			case mcidProp:
				kind = mcTagged
			}
			if kind == mcArtifact && hasAncestor(mcTagged) {
				report("content marked as /Artifact is nested inside tagged content")
			}
			if kind == mcTagged && hasAncestor(mcArtifact) {
				report("tagged content is nested inside an /Artifact")
			}
			stack = append(stack, kind)
		case "EMC":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case "Tj", "TJ", "'", "\"":
			if len(stack) == 0 {
				report("page contains text that is neither tagged nor marked as an /Artifact")
			}
		case "Do":
			f.doNames = append(f.doNames, lastName)
		}
		firstOperand, artifactTag, mcidProp = true, false, false
	}
	return f
}

// checkUAFormXObjectMCID enforces 7.20: a form XObject whose content is tagged
// (contains an /MCID marked-content sequence) must be painted at most once. If
// it is invoked by more than one Do operator, a single structure element would
// map to several renderings, breaking the one-to-one structure/content mapping.
func checkUAFormXObjectMCID(d *Document) []UAViolation {
	mcidForm := map[int]bool{}
	for num, iobj := range d.Objects {
		s, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, _ := s.Dict.Get("Subtype").(Name); st != "Form" {
			continue
		}
		if bytesContainsToken(decodeContentStream(d, s), "/MCID") {
			mcidForm[num] = true
		}
	}
	if len(mcidForm) == 0 {
		return nil
	}

	doCount := map[int]int{}
	// countDo resolves the Do-invoked XObject names of one content stream
	// against its container's /XObject resources and tallies each target. The
	// name sequence comes from the shared per-stream facts, so the content is
	// tokenized once even though the real-content check scans the same pages.
	countDo := func(content []byte, key *Stream, res *Dictionary) {
		if res == nil {
			return
		}
		xobjs := d.ResolveDict(res.Get("XObject"))
		if xobjs == nil {
			return
		}
		name2num := map[string]int{}
		for i, k := range xobjs.Keys {
			if ref, ok := xobjs.Values[i].(IndirectRef); ok {
				name2num[string(k)] = ref.Number
			}
		}
		for _, name := range contentFacts(d, content, key).doNames {
			if n, ok := name2num[name]; ok {
				doCount[n]++
			}
		}
	}
	// Page content sources.
	for _, pg := range collectPages(d, d.catalogPages()) {
		data, key := d.contentBytesAndKey(pg.Dict.Get("Contents"))
		countDo(data, key, d.ResolveDict(pg.Dict.Get("Resources")))
	}
	// Form XObject content sources (a form may invoke another form).
	for _, iobj := range d.Objects {
		s, ok := iobj.Value.(*Stream)
		if !ok {
			continue
		}
		if st, _ := s.Dict.Get("Subtype").(Name); st != "Form" {
			continue
		}
		countDo(decodeContentStream(d, s), s, d.ResolveDict(s.Dict.Get("Resources")))
	}

	var v []UAViolation
	for _, num := range sortedInts(mcidForm) {
		if doCount[num] > 1 {
			v = append(v, UAViolation{"7.20", "a form XObject containing marked content (/MCID) is painted by more than one Do operator", num})
		}
	}
	return v
}

// bytesContainsToken reports whether tok appears in data followed by a
// delimiter/whitespace (so "/MCID" does not match "/MCIDExtra").
func bytesContainsToken(data []byte, tok string) bool {
	for i := 0; ; {
		j := indexBytes(data[i:], tok)
		if j < 0 {
			return false
		}
		end := i + j + len(tok)
		if end >= len(data) || syntax.IsWhitespace(data[end]) || core.IsContentDelim(data[end]) {
			return true
		}
		i = i + j + 1
	}
}

func indexBytes(b []byte, s string) int {
	n := len(s)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(b); i++ {
		if string(b[i:i+n]) == s {
			return i
		}
	}
	return -1
}

func sortedInts(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// checkUAAnnotStructType correlates annotations with the structure tree via OBJR
// references and flags a mismatch between an annotation's subtype and its
// enclosing structure element: a Widget must sit under <Form>, a Link under
// <Link>, and any other annotation under <Annot> (Matterhorn 28-002/010/011).
// Annotations not reachable through an OBJR are left to the tagging check.
func checkUAAnnotStructType(d *Document, cat *Dictionary) []UAViolation {
	root := d.ResolveDict(cat.Get("StructTreeRoot"))
	if root == nil {
		return nil
	}
	annotParent := map[int]Name{}
	seen := map[int]bool{}
	var walk func(node Object, parentType Name)
	walk = func(node Object, parentType Name) {
		if ref, ok := node.(IndirectRef); ok {
			if seen[ref.Number] {
				return
			}
			seen[ref.Number] = true
		}
		elem := d.ResolveDict(node)
		if elem == nil {
			if arr, ok := d.Resolve(node).(Array); ok {
				for _, kid := range arr {
					walk(kid, parentType)
				}
			}
			return
		}
		// An OBJR structure element references an object (often an annotation).
		if t, _ := elem.Get("Type").(Name); t == "OBJR" {
			if ref, ok := elem.Get("Obj").(IndirectRef); ok {
				annotParent[ref.Number] = parentType
			}
			return
		}
		s, _ := elem.Get("S").(Name)
		if k := elem.Get("K"); k != nil {
			switch kids := d.Resolve(k).(type) {
			case Array:
				for _, kid := range kids {
					walk(kid, s)
				}
			default:
				walk(k, s)
			}
		}
	}
	walk(root.Get("K"), "")

	var v []UAViolation
	for num, iobj := range d.Objects {
		a, ok := iobj.Value.(*Dictionary)
		if !ok || !isAnnotation(a) {
			continue
		}
		st, _ := a.Get("Subtype").(Name)
		if st == "Popup" {
			continue
		}
		if f, _ := d.Resolve(a.Get("F")).(Integer); int(f)&0x2 != 0 {
			continue
		}
		parent, linked := annotParent[num]
		if !linked {
			continue // not reached via OBJR; the tagging check covers presence
		}
		want := Name("Annot")
		switch st {
		case "Widget":
			want = "Form"
		case "Link":
			want = "Link"
		}
		if parent != want {
			v = append(v, UAViolation{"7.18.1", "annotation of subtype /" + string(st) + " is nested in a <" + string(parent) + "> element, expected <" + string(want) + ">", num})
		}
	}
	return v
}
