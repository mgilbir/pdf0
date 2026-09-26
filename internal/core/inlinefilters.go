package core

// The filters of a content stream's inline images, for the rules that restrict
// them: PDF/A (6.1.10 and siblings) and PDF/R. It moved here from pdfa when
// PDF/R needed it (audit 2026-09-22 C146), so there is one reading of an
// inline image's /F for both, and it reads through the one content lexer.

// InlineImageFilters returns the /F (or /Filter) filter name(s) of every
// inline image in a content stream that declares any, in order.
func InlineImageFilters(cancel Canceler, data []byte) [][]string {
	var out [][]string
	lx := NewContentLexer(cancel, data)
	var t ContentTok
	for lx.Next(&t) {
		if t.Kind != ContentInlineImage {
			continue
		}
		var filters []string
		for _, p := range ParseInlineImageParams(t.Params) {
			if p.Key != "F" && p.Key != "Filter" {
				continue
			}
			for _, v := range p.Value {
				if v.Kind == ContentName {
					filters = append(filters, v.Name())
				}
			}
		}
		if filters != nil {
			out = append(out, filters)
		}
	}
	return out
}
