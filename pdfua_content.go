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

	depth := 0
	for _, tk := range tokenizeContent(content) {
		if tk.kind != ctOp {
			continue
		}
		switch tk.op {
		case "BDC", "BMC":
			depth++
		case "EMC":
			if depth > 0 {
				depth--
			}
		case "Tj", "TJ", "'", "\"":
			if depth == 0 {
				report("page contains text that is neither tagged nor marked as an /Artifact")
			}
		}
	}
	return v
}
