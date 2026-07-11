package pdf0

// checkUARealContent implements Matterhorn checkpoint 01-005: on every page,
// real content (text) must appear inside a marked-content sequence — either
// tagged (linked to the structure tree) or an Artifact. Text drawn outside any
// marked-content sequence is neither, and is flagged.
//
// It walks each page's content stream tracking marked-content nesting depth from
// BDC/BMC…EMC. (The Artifact/tagged *mis-nesting* conditions 01-003/01-004 need
// to distinguish structure-linked BDC from /OC and property-less BDC, which a
// plain depth count cannot; they are left to the structure-correlation pass.)
func (d *Document) checkUARealContent(cat *Dictionary) []UAViolation {
	var v []UAViolation
	for _, pg := range collectPages(d, cat.Get("Pages")) {
		data, key := d.contentBytesAndKey(pg.dict.Get("Contents"))
		for _, msg := range d.realContentMessages(data, key) {
			v = append(v, UAViolation{"7.1", msg, pg.objNum})
		}
	}
	return v
}

// realContentMessages returns the distinct real-content violation messages for
// a page's content, memoized per content stream (key) so a stream shared by
// many pages is analyzed once. The analysis depends only on the content bytes
// (marked-content nesting and text operators), not the page, so the same stream
// always yields the same messages — each page emits them under its own object
// number.
func (d *Document) realContentMessages(content []byte, key *Stream) []string {
	if key != nil {
		if c := d.valCache; c != nil {
			if m, ok := c.realContent[key]; ok {
				return m
			}
		}
	}
	msgs := analyzeRealContent(content)
	if key != nil {
		if c := d.valCache; c != nil {
			if c.realContent == nil {
				c.realContent = make(map[*Stream][]string)
			}
			c.realContent[key] = msgs
		}
	}
	return msgs
}

func analyzeRealContent(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	var msgs []string
	reported := map[string]bool{}
	report := func(msg string) {
		if !reported[msg] {
			reported[msg] = true
			msgs = append(msgs, msg)
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
	var operands []contentToken
	for _, tk := range tokenizeContent(content) {
		if tk.kind != ctOp {
			operands = append(operands, tk)
			continue
		}
		switch tk.op {
		case "BDC", "BMC":
			kind := mcTransparent
			switch {
			case len(operands) > 0 && operands[0].kind == ctName && operands[0].name == "Artifact":
				kind = mcArtifact
			case operandsHaveName(operands, "MCID"):
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
		}
		operands = operands[:0]
	}
	return msgs
}

// checkUAFormXObjectMCID enforces 7.20: a form XObject whose content is tagged
// (contains an /MCID marked-content sequence) must be painted at most once. If
// it is invoked by more than one Do operator, a single structure element would
// map to several renderings, breaking the one-to-one structure/content mapping.
func (d *Document) checkUAFormXObjectMCID() []UAViolation {
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
	countDo := func(content []byte, res *Dictionary) {
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
		var last string
		for _, tk := range tokenizeContent(content) {
			switch {
			case tk.kind == ctName:
				last = tk.name
			case tk.kind == ctOp && tk.op == "Do":
				if n, ok := name2num[last]; ok {
					doCount[n]++
				}
			}
		}
	}
	// Page content sources.
	for _, pg := range collectPages(d, d.catalogPages()) {
		countDo(getContentStreamData(d, pg.dict.Get("Contents")), d.ResolveDict(pg.dict.Get("Resources")))
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
		countDo(decodeContentStream(d, s), d.ResolveDict(s.Dict.Get("Resources")))
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
		if end >= len(data) || isWhitespace(data[end]) || isContentDelim(data[end]) {
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

func operandsHaveName(operands []contentToken, name string) bool {
	for _, o := range operands {
		if o.kind == ctName && o.name == name {
			return true
		}
	}
	return false
}

// checkUAAnnotStructType correlates annotations with the structure tree via OBJR
// references and flags a mismatch between an annotation's subtype and its
// enclosing structure element: a Widget must sit under <Form>, a Link under
// <Link>, and any other annotation under <Annot> (Matterhorn 28-002/010/011).
// Annotations not reachable through an OBJR are left to the tagging check.
func (d *Document) checkUAAnnotStructType(cat *Dictionary) []UAViolation {
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
