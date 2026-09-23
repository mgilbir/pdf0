package pdf0

// Code in this file is the content tokenizers the module had before
// core.ContentLexer replaced them, kept verbatim (renamed, and qualified with
// core.) as the oracle for TestCorpusContentLexerDifferential. Do not fix
// them: they are the "before".

import (
	"iter"

	"github.com/mgilbir/pdf0/internal/core"
)

type oldItemKind int

const (
	oldItemOperator oldItemKind = iota
	oldItemName
	oldItemString
	oldItemNumber
	oldItemDict
)

func oldScanContentDict(data []byte, i int) int {
	const maxDepth = 64
	n := len(data)
	depth := 0
	for i < n {
		switch {
		case data[i] == '<' && i+1 < n && data[i+1] == '<':
			depth++
			i += 2
			if depth > maxDepth {
				return n
			}
		case data[i] == '>' && i+1 < n && data[i+1] == '>':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		case data[i] == '(':
			_, i = oldDecodeContentLiteralString(data, i)
		case data[i] == '<':
			i++
			for i < n && data[i] != '>' {
				i++
			}
			if i < n {
				i++
			}
		default:
			i++
		}
	}
	return n
}

func oldForEachContentItem(cancel core.Canceler, data []byte, fn func(kind oldItemKind, payload []byte)) {
	n := len(data)
	i := 0
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + core.CancelScanBytes
		}
		for i < n && core.IsContentWS(data[i]) {
			i++
		}
		if i >= n {
			return
		}
		switch b := data[i]; {
		case b == '%':
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(':
			str, next := oldDecodeContentLiteralString(data, i)
			fn(oldItemString, str)
			i = next
		case b == '<' && i+1 < n && data[i+1] == '<':
			end := oldScanContentDict(data, i)
			fn(oldItemDict, data[i:end])
			i = end
		case b == '<':
			i++
			start := i
			for i < n && data[i] != '>' {
				i++
			}
			fn(oldItemString, oldDecodeHexBytes(data[start:i]))
			if i < n {
				i++
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}' || b == ')':
			// A stray ')' (unbalanced by any '(') is not the start of a token;
			// consume it so the scan always advances. Without this, a content
			// stream with an unmatched ')' — e.g. leaked inline-image sample
			// data — spins forever, since ')' is a delimiter the default token
			// scan below cannot consume (a parser DoS on untrusted input).
			i++
		case b == '/':
			i++
			start := i
			for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
				i++
			}
			fn(oldItemName, data[start:i])
		default:
			start := i
			// Numeric tokens may be arbitrarily long (Annex C allows huge
			// precision); read them whole. Non-numeric keyword tokens are
			// capped to bound scanning over stray binary data.
			numeric := data[i] >= '0' && data[i] <= '9' || data[i] == '+' || data[i] == '-' || data[i] == '.'
			for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
				i++
			}
			if i == start {
				// Defensive: an unhandled delimiter would yield no token and no
				// progress. Skip it so the scan can never stall.
				i++
				continue
			}
			if !numeric && i-start > core.MaxContentTokenLen {
				continue // binary run, not a keyword; see scanStreamForDeviceOps
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				core.SkipInlineImage(data, &i)
				continue
			}
			if numeric {
				fn(oldItemNumber, tok)
				continue
			}
			fn(oldItemOperator, tok)
		}
	}
}

func oldForEachContentToken(cancel core.Canceler, data []byte, fn func(tok []byte, isName bool)) {
	n := len(data)
	i := 0
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + core.CancelScanBytes
		}
		for i < n && core.IsContentWS(data[i]) {
			i++
		}
		if i >= n {
			return
		}
		switch b := data[i]; {
		case b == '%': // comment to end of line
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
		case b == '(': // string literal with escapes and balanced parens
			depth := 1
			i++
			for i < n && depth > 0 {
				switch data[i] {
				case '\\':
					i++ // skip escaped char
				case '(':
					depth++
				case ')':
					depth--
				}
				i++
			}
		case b == '<':
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else { // hex string
				for i < n && data[i] != '>' {
					i++
				}
				if i < n {
					i++
				}
			}
		case b == '>':
			i++
			if i < n && data[i] == '>' {
				i++
			}
		case b == '[' || b == ']' || b == '{' || b == '}' || b == ')':
			// A stray ')' is a delimiter, not a token start; consume it so the
			// scan always advances (an unmatched ')' would otherwise spin
			// forever — a DoS on untrusted content).
			i++
		case b == '/':
			i++
			start := i
			for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
				i++
			}
			fn(data[start:i], true)
		default:
			start := i
			for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
				i++
			}
			if i == start {
				// Defensive: an unhandled delimiter yields no progress; skip it.
				i++
				continue
			}
			if i-start > core.MaxContentTokenLen {
				continue // binary run, not a token; see scanStreamForDeviceOps
			}
			tok := data[start:i]
			if len(tok) == 2 && tok[0] == 'B' && tok[1] == 'I' {
				core.SkipInlineImage(data, &i)
				continue
			}
			fn(tok, false)
		}
	}
}

func oldDecodeHexBytes(b []byte) []byte {
	var digits []byte
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
			digits = append(digits, c)
		}
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	hv := func(c byte) byte {
		switch {
		case c <= '9':
			return c - '0'
		case c >= 'a':
			return c - 'a' + 10
		}
		return c - 'A' + 10
	}
	for i := 0; i < len(out); i++ {
		out[i] = hv(digits[2*i])<<4 | hv(digits[2*i+1])
	}
	return out
}

func oldDecodeContentLiteralString(data []byte, i int) ([]byte, int) {
	n := len(data)
	var out []byte
	depth := 1
	i++
	for i < n && depth > 0 {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= n {
				break
			}
			e := data[i]
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\n': // line continuation
			case '\r':
				if i+1 < n && data[i+1] == '\n' {
					i++
				}
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && i+1 < n && data[i+1] >= '0' && data[i+1] <= '7'; k++ {
						i++
						v = v<<3 | int(data[i]-'0')
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth > 0 {
				out = append(out, c)
			}
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func oldTokenizeContent(cancel core.Canceler, data []byte) iter.Seq[core.ContentToken] {
	return func(yield func(core.ContentToken) bool) {
		i := 0
		nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
		for i < len(data) {
			if i >= nextCancelCheck {
				if cancel.Stopped() {
					return
				}
				nextCancelCheck = i + core.CancelScanBytes
			}
			c := data[i]
			switch {
			case core.IsContentWS(c):
				i++
			case c == '%':
				for i < len(data) && data[i] != '\n' && data[i] != '\r' {
					i++
				}
			case c == '(':
				s, ni := oldScanContentLiteral(data, i)
				if !yield(core.ContentToken{Kind: core.KindString, Str: s}) {
					return
				}
				i = ni
			case c == '<' && i+1 < len(data) && data[i+1] == '<':
				i += 2 // dictionary start — skip; not needed for text
			case c == '>' && i+1 < len(data) && data[i+1] == '>':
				i += 2
			case c == '<':
				s, ni := oldScanContentHex(data, i)
				if !yield(core.ContentToken{Kind: core.KindString, Str: s}) {
					return
				}
				i = ni
			case c == '/':
				n, ni := oldScanContentName(data, i)
				if !yield(core.ContentToken{Kind: core.KindName, Name: n}) {
					return
				}
				i = ni
			case c == '[':
				if !yield(core.ContentToken{Kind: core.KindArrayStart}) {
					return
				}
				i++
			case c == ']':
				if !yield(core.ContentToken{Kind: core.KindArrayEnd}) {
					return
				}
				i++
			case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
				raw, ni := oldScanContentNumberBytes(data, i)
				if !yield(core.ContentToken{Kind: core.KindNumber, Raw: raw}) {
					return
				}
				i = ni
			default:
				word, ni := oldScanContentWord(data, i)
				i = ni
				if word == "" {
					i++
					continue
				}
				if word == "BI" {
					i = core.SkipContentInlineImage(data, i)
					continue
				}
				if !yield(core.ContentToken{Kind: core.KindOp, Op: word}) {
					return
				}
			}
		}
	}
}

func oldScanContentLiteral(data []byte, i int) ([]byte, int) {
	i++ // '('
	var out []byte
	depth := 1
	for i < len(data) {
		c := data[i]
		switch c {
		case '\\':
			i++
			if i >= len(data) {
				return out, i
			}
			switch e := data[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '(', ')', '\\':
				out = append(out, e)
			default:
				if e >= '0' && e <= '7' {
					v := 0
					for k := 0; k < 3 && i < len(data) && data[i] >= '0' && data[i] <= '7'; k++ {
						v = v*8 + int(data[i]-'0')
						i++
					}
					out = append(out, byte(v))
					continue
				}
				out = append(out, e)
			}
			i++
		case '(':
			depth++
			out = append(out, c)
			i++
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, c)
			i++
		default:
			out = append(out, c)
			i++
		}
	}
	return out, i
}

func oldScanContentHex(data []byte, i int) ([]byte, int) {
	i++ // '<'
	var digits []byte
	for i < len(data) && data[i] != '>' {
		if !core.IsContentWS(data[i]) {
			digits = append(digits, data[i])
		}
		i++
	}
	if i < len(data) {
		i++ // '>'
	}
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	for k := 0; k < len(out); k++ {
		out[k] = oldHexNibble(digits[2*k])<<4 | oldHexNibble(digits[2*k+1])
	}
	return out, i
}

func oldHexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func oldScanContentName(data []byte, i int) (string, int) {
	i++ // '/'
	start := i
	for i < len(data) && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

func oldScanContentNumberBytes(data []byte, i int) ([]byte, int) {
	start := i
	if data[i] == '-' || data[i] == '+' {
		i++
	}
	for i < len(data) && ((data[i] >= '0' && data[i] <= '9') || data[i] == '.') {
		i++
	}
	return data[start:i], i
}

func oldScanContentWord(data []byte, i int) (string, int) {
	start := i
	for i < len(data) && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
		i++
	}
	return string(data[start:i]), i
}

func oldScanStreamForDeviceOps(cancel core.Canceler, data []byte) (usesRGB, usesCMYK, usesGray bool) {
	n := len(data)
	var lastName string
	sawColorOp := false
	paints := false
	defer func() {
		// Painting without ever selecting a colour uses the initial colour:
		// DeviceGray black (ISO 32000-1, 8.4.1).
		if paints && !sawColorOp {
			usesGray = true
		}
	}()
	// Scan for operators at word boundaries.
	// An operator token is an alphabetic sequence preceded by whitespace (or BOF)
	// and followed by whitespace, delimiter, or EOF.
	i := 0
	nextCancelCheck := 0 // poll before the first token, then per cancelScanBytes
	for i < n {
		if i >= nextCancelCheck {
			if cancel.Stopped() {
				return
			}
			nextCancelCheck = i + core.CancelScanBytes
		}
		// Skip whitespace
		for i < n && core.IsContentWS(data[i]) {
			i++
		}
		if i >= n {
			break
		}

		b := data[i]

		// Skip comments
		if b == '%' {
			for i < n && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}

		// Skip string literals (...)
		if b == '(' {
			depth := 1
			i++
			for i < n && depth > 0 {
				if data[i] == '\\' {
					i++ // skip escape char
					if i >= n {
						break
					}
				} else if data[i] == '(' {
					depth++
				} else if data[i] == ')' {
					depth--
				}
				i++
			}
			continue
		}

		// Skip hex strings and dict markers
		if b == '<' {
			i++
			if i < n && data[i] == '<' {
				i++ // <<
			} else {
				for i < n && data[i] != '>' {
					i++
				}
				if i < n {
					i++
				}
			}
			continue
		}
		if b == '>' {
			i++
			if i < n && data[i] == '>' {
				i++
			}
			continue
		}

		// Skip array/proc delimiters, and a stray ')' (a delimiter that would
		// otherwise stall the token scan below on untrusted content).
		if b == '[' || b == ']' || b == '{' || b == '}' || b == ')' {
			i++
			continue
		}

		// PDF names (/object.Name): remember the last one seen, so a following
		// cs/CS operator can be checked for direct device selection.
		if b == '/' {
			i++
			nameStart := i
			for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
				i++
			}
			lastName = string(data[nameStart:i])
			continue
		}

		// Read a token
		start := i
		for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
			i++
		}
		// A run longer than this is binary data, not a token: no PDF operator
		// or operand keyword is anywhere near this long. Discarding it whole is
		// what matters — cutting it at the cap and letting the scan re-enter
		// mid-run turns the tail into further "tokens", and a fragment of
		// binary read as an operator is a violation the file does not commit
		// (a stray 'k'/'g' fragment reads as DeviceCMYK/DeviceGray use).
		if i-start > core.MaxContentTokenLen {
			continue
		}

		tokLen := i - start

		// Skip names (start with /)
		if tokLen > 0 && data[start] == '/' {
			continue
		}

		switch string(data[start:i]) {
		case "rg", "RG", "g", "G", "k", "K", "cs", "CS", "sc", "scn", "SC", "SCN":
			sawColorOp = true
		case "f", "F", "f*", "S", "s", "B", "B*", "b", "b*", "Tj", "TJ", "'", "\"", "sh":
			paints = true
		}

		// Handle inline images: BI <dict> ID <binary> EI
		// Check for BI (begin inline image), parse dict for CS, then skip binary
		if tokLen == 2 && data[start] == 'B' && data[start+1] == 'I' {
			// Parse inline image dict until ID token
			// Look for /CS or /ColorSpace keys with device CS values
			foundID := false
			for i < n && !foundID {
				// Skip whitespace
				for i < n && core.IsContentWS(data[i]) {
					i++
				}
				if i >= n {
					break
				}
				// Check for ID token (end of inline image dict)
				if data[i] == 'I' && i+1 < n && data[i+1] == 'D' &&
					(i+2 >= n || core.IsContentWS(data[i+2])) {
					i += 2
					// Skip one whitespace byte after ID
					if i < n && core.IsContentWS(data[i]) {
						i++
					}
					foundID = true
					break
				}
				// Read key or value token
				if data[i] == '/' {
					// Read name
					keyStart := i + 1
					i++
					for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
						i++
					}
					key := string(data[keyStart:i])
					// If key is CS or ColorSpace, check the next value
					if key == "CS" || key == "ColorSpace" {
						// Skip whitespace
						for i < n && core.IsContentWS(data[i]) {
							i++
						}
						// Read value - could be /object.Name or /abbreviation
						if i < n && data[i] == '/' {
							valStart := i + 1
							i++
							for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
								i++
							}
							csVal := string(data[valStart:i])
							switch csVal {
							case "RGB", "DeviceRGB":
								usesRGB = true
							case "CMYK", "DeviceCMYK":
								usesCMYK = true
							case "G", "DeviceGray":
								usesGray = true
							}
						}
					}
				} else {
					// Skip non-name token (numbers, arrays, etc.)
					prev := i
					if data[i] == '[' || data[i] == ']' || data[i] == '(' || data[i] == ')' ||
						data[i] == '<' || data[i] == '>' {
						i++ // skip single delimiter
					} else {
						for i < n && !core.IsContentWS(data[i]) && !core.IsContentDelim(data[i]) {
							i++
						}
					}
					// Safety: if no progress, advance by 1
					if i == prev {
						i++
					}
				}
			}
			// Now skip binary data until EI at word boundary
			if foundID {
				for i < n {
					if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
						atBoundary := (i == 0 || core.IsContentWS(data[i-1]))
						endBoundary := (i+2 >= n || core.IsContentWS(data[i+2]) || core.IsContentDelim(data[i+2]))
						if atBoundary && endBoundary {
							i += 2
							break
						}
					}
					i++
				}
			}
			continue
		}

		// Handle ID token outside of BI context (shouldn't happen, but be safe)
		if tokLen == 2 && data[start] == 'I' && data[start+1] == 'D' {
			// Skip one whitespace byte after ID
			if i < n && core.IsContentWS(data[i]) {
				i++
			}
			// Scan for EI at word boundary
			for i < n {
				if data[i] == 'E' && i+1 < n && data[i+1] == 'I' {
					atBoundary := (i == 0 || core.IsContentWS(data[i-1]))
					endBoundary := (i+2 >= n || core.IsContentWS(data[i+2]) || core.IsContentDelim(data[i+2]))
					if atBoundary && endBoundary {
						i += 2
						break
					}
				}
				i++
			}
			continue
		}

		// Check for device color operators (only short alphabetic tokens)
		if tokLen == 2 {
			if data[start] == 'r' && data[start+1] == 'g' {
				usesRGB = true
			} else if data[start] == 'R' && data[start+1] == 'G' {
				usesRGB = true
			} else if (data[start] == 'c' && data[start+1] == 's') ||
				(data[start] == 'C' && data[start+1] == 'S') {
				// Direct device selection: /DeviceRGB cs (etc.). Named
				// resource selections (/CS0 cs) are covered by the
				// resource-dictionary walk.
				switch lastName {
				case "DeviceRGB":
					usesRGB = true
				case "DeviceCMYK":
					usesCMYK = true
				case "DeviceGray":
					usesGray = true
				}
			}
		} else if tokLen == 1 {
			switch data[start] {
			case 'g':
				usesGray = true
			case 'G':
				usesGray = true
			case 'k':
				usesCMYK = true
			case 'K':
				usesCMYK = true
			}
		}
	}
	return
}
