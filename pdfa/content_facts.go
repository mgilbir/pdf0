package pdfa

import "github.com/mgilbir/pdf0/internal/core"

// Five rules judge a content stream by its tokens alone, whatever draws it and
// whatever its names resolve to: the content-stream implementation limits
// (ISO 19005-1 6.1.12, -2/-3 6.1.13), the hexadecimal-string format (6.1.6,
// -4 6.1.5), the inline-image filters (6.1.10, -4 6.1.9), the inline-image
// /Interpolate and /Intent entries, and the ActualText of a marked-content
// property list (-4 6.2.10.8). They read the same token stream — every token,
// dictionary operands included — so one pass over each stream records what all
// five need (contentBytesFacts), and each rule reads the record. They used to
// tokenise every stream once each, and once per page that drew it.

// contentBytesFacts is what one content stream's tokens say for the
// token-level rules.
type contentBytesFacts struct {
	// bigNumbers is each distinct number operand over the strictest content
	// limits any part sets (checkContentNumberLimit at strictContentLimits):
	// a part with looser limits judges them again.
	bigNumbers []string
	// longStrings is each distinct length of a string operand over the
	// strictest string-length limit.
	longStrings []int
	// hexOdd and hexNonDigit are whether some hexadecimal string has an odd
	// number of digits, or a character that is not a hexadecimal digit.
	hexOdd, hexNonDigit bool
	// inlineFilters is the /F (or /Filter) names of each inline image that
	// declares any, in order (core.InlineImageFilters).
	inlineFilters [][]string
	// inlineEntries is each inline image's parameters as key -> first value
	// token (inlineImageEntries).
	inlineEntries []map[string]string
	// actualTexts is the value of each /ActualText entry: a name token
	// /ActualText followed by a string.
	actualTexts [][]byte
}

// strictContentLimits are the tightest content-stream operand limits of any
// part (ISO 19005-1's): a number or string within them is within every
// part's.
var strictContentLimits = implLimits{stringLen: 32767, realLimit: 32767}

// scanContentBytes makes one pass over a content stream for contentBytesFacts.
func scanContentBytes(cancel core.Canceler, data []byte) *contentBytesFacts {
	f := &contentBytesFacts{}
	seenNum := map[string]bool{}
	seenLen := map[int]bool{}
	afterActualText := false
	lx := core.NewContentLexer(cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		switch t.Kind {
		case core.ContentNumber:
			checkContentNumberLimit(string(t.Raw), strictContentLimits, 0, func(string, int) {
				if s := string(t.Raw); !seenNum[s] {
					seenNum[s] = true
					f.bigNumbers = append(f.bigNumbers, s)
				}
			})
		case core.ContentString, core.ContentHexString:
			v := t.Bytes()
			if n := len(v); n > strictContentLimits.stringLen && !seenLen[n] {
				seenLen[n] = true
				f.longStrings = append(f.longStrings, n)
			}
			if t.Kind == core.ContentHexString {
				body := t.HexBody()
				f.hexOdd = f.hexOdd || !hexStringEven(body)
				f.hexNonDigit = f.hexNonDigit || !hexStringDigitsOnly(body)
			}
			if afterActualText {
				f.actualTexts = append(f.actualTexts, v)
			}
		case core.ContentInlineImage:
			params := core.ParseInlineImageParams(t.Params)
			entries := map[string]string{}
			for _, p := range params {
				switch {
				case p.Array:
					entries[p.Key] = "[array]"
				case len(p.Value) == 0:
				case p.Value[0].Kind == core.ContentName:
					entries[p.Key] = p.Value[0].Name()
				default:
					entries[p.Key] = string(p.Value[0].Raw)
				}
			}
			if filters := core.InlineFilterNames(params); filters != nil {
				f.inlineFilters = append(f.inlineFilters, filters)
			}
			f.inlineEntries = append(f.inlineEntries, entries)
		}
		afterActualText = t.Kind == core.ContentName && t.Name() == "ActualText"
	}
	return f
}

type contentBytesFactsSlot struct{}

// contentBytesFactsOf is scanContentBytes over every content stream of the
// document (collectContentStreamData: each distinct content once, under the
// lowest object number that holds it), once per run.
func contentBytesFactsOf(doc core.View) map[int]*contentBytesFacts {
	memo := core.Slot[map[int]*contentBytesFacts](doc.Run, contentBytesFactsSlot{})
	if *memo != nil {
		return *memo
	}
	out := map[int]*contentBytesFacts{}
	for num, data := range collectContentStreamData(doc) {
		out[num] = scanContentBytes(doc.Cancel, data)
	}
	if !doc.Cancel.Stopped() {
		*memo = out
	}
	return out
}
