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
		v = append(v, d.checkPageRealContent(pg.dict, pg.objNum)...)
	}
	return v
}

func (d *Document) checkPageRealContent(page *Dictionary, objNum int) []UAViolation {
	content := getContentStreamData(d, page.Get("Contents"))
	if len(content) == 0 {
		return nil
	}
	var v []UAViolation
	reported := map[string]bool{}
	report := func(msg string) {
		if !reported[msg] {
			reported[msg] = true
			v = append(v, UAViolation{"7.1", msg, objNum})
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
	return v
}

func operandsHaveName(operands []contentToken, name string) bool {
	for _, o := range operands {
		if o.kind == ctName && o.name == name {
			return true
		}
	}
	return false
}
