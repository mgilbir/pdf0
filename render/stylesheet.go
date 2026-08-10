package render

import (
	"fmt"
	"strings"

	"github.com/mgilbir/pdf0/html"
)

// The CSS a document carries, and the CSS it points at.
//
// A <style> element and a <link rel=stylesheet> are the same thing to the
// cascade — both are author stylesheets, and the only difference between them is
// where the bytes come from. The difference matters to everything else: the text
// of a <style> arrived inside the string the caller handed over, and the bytes
// behind a <link> did not.
//
// So the second one is a *resource*, and goes through resource.go's policy in
// full: no scheme, no absolute path, no escape from the directory the resolver
// was rooted at, and nothing at all when there is no resolver. There is no
// second path here that could disagree with the one <img> uses, which is the
// only way two policies stay the same — see the note on backgrounds in image.go
// for the same argument about the same resolver.
//
// # Why a stylesheet needs its own caps as well
//
// An image is bounded by what it decodes to. A stylesheet is bounded by nothing
// once it is read: every rule in it is matched against every element, so a
// megabyte of selectors is quadratic work the document did not have to carry.
// Two caps answer that — one on a sheet and one on how many a document may pull
// in — and both are checked here rather than left to the resolver, because a
// caller may supply a resolver of their own and the engine's limits must not
// depend on which one they wrote.

// maxStylesheetBytes is the largest linked stylesheet this engine will read.
//
// A megabyte is more CSS than any document has: the largest sheets on the web
// are a few hundred kilobytes, and a sheet past this is not a document's styles
// but a payload wearing their name. It bounds what is parsed and, through that,
// what the cascade has to match — which is the cost that matters, since every
// rule is tried against every element.
//
// It is a variable so that a test can lower it and watch it fire. A cap nobody
// has seen trip is one nobody knows works.
var maxStylesheetBytes = 1 << 20

// maxDocumentStylesheets is how many linked stylesheets one document may pull
// in.
//
// The per-sheet cap alone is not a bound on a document: a page with a thousand
// <link> elements is a thousand reads and a thousand parses, each of them
// legal. This is the count that makes the total finite, and it is deliberately
// low — a document needs a handful of stylesheets, and one that names twenty is
// already doing something other than styling itself.
//
// It counts sheets *fetched*, not <link> elements seen: a document may name the
// same file twice and be charged once, because it is read once.
var maxDocumentStylesheets = 20

// authorSheet is one author stylesheet in document order.
type authorSheet struct {
	// name identifies the sheet in a finding. It is the href for a linked
	// sheet and empty for a <style> element, matching the Source.Sheet
	// convention in finding.go.
	name string
	// source is the CSS.
	source string
}

// documentStylesheets collects every author stylesheet a document carries, in
// the order the cascade must see them.
//
// Document order is not a detail. The last tie-break in the cascade is which
// rule was written later, so a <link> that follows a <style> has to arrive after
// it — and a browser orders the two by their position in the markup rather than
// by their kind. Collecting them in one walk is what makes that true by
// construction instead of by a sort somebody has to keep right.
func documentStylesheets(doc *html.Node, res ResourceResolver, rec *Recorder) []authorSheet {
	l := &sheetLoader{res: res, rec: rec, failed: map[string]bool{}}
	var out []authorSheet
	doc.Walk(func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return true
		}
		switch strings.ToLower(n.Name) {
		case "style":
			if text := n.TextContent(); text != "" {
				out = append(out, authorSheet{source: text})
			}
			// A <style> element's content is raw text, so there is nothing
			// below it to walk.
			return false
		case "link":
			if s, ok := l.link(n); ok {
				out = append(out, s)
			}
			return false
		}
		return true
	})
	return out
}

// sheetLoader fetches the stylesheets a document links to, under the caps.
type sheetLoader struct {
	res ResourceResolver
	rec *Recorder

	// cache holds the text of every reference already read, so a document
	// naming one sheet in ten <link> elements reads it once.
	//
	// It is a cache and not a suppression, and the difference is the cascade.
	// A repeated <link> is a stylesheet at *that* point in document order, and
	// dropping the repeat would let a <style> written between the two win a tie
	// it should lose. So the sheet is handed over again from here; what is
	// saved is the read.
	cache map[string]string
	// failed records the references already refused, so a document with ten
	// links to one missing file makes one attempt rather than ten. The
	// Recorder deduplicates the finding on its own; what this saves is the
	// system calls.
	failed map[string]bool
	// applied counts the stylesheets handed to the cascade from outside the
	// document, which is what maxDocumentStylesheets bounds.
	//
	// It counts sheets applied rather than files read, and that is what keeps
	// the cache above from being an amplifier: a document with a thousand links
	// to one megabyte reads it once and would otherwise parse and match it a
	// thousand times.
	applied int
	// capped records that the count cap was reported, so it is reported once.
	capped bool
}

// link turns one <link> element into a stylesheet, or explains why it did not.
func (l *sheetLoader) link(n *html.Node) (authorSheet, bool) {
	rel, _ := n.Attr("rel")
	if !relIsStylesheet(rel) {
		return authorSheet{}, false
	}
	href, _ := n.Attr("href")
	href = strings.TrimSpace(href)
	if href == "" {
		// A <link> with no href names nothing, exactly as an <img> with no src
		// does. There is no reference for a resolver to have refused.
		return authorSheet{}, false
	}
	if media, ok := n.Attr("media"); ok {
		apply, understood := mediaAppliesToPaper(media)
		if !understood {
			// A media query this engine cannot evaluate. Applying it would be a
			// guess and so would skipping it, and the guess that shows least is
			// the one nobody can see — so the sheet is left out and the reason
			// is named. A document whose whole appearance is behind a query
			// then reads as unstyled *and says so*, rather than reading as
			// styled by rules that may not have been meant for paper.
			l.rec.ReportDetail(Finding{
				Rule:   RuleUnsupportedValue,
				Source: AtHTML(n.Offset),
				Message: "the stylesheet at " + quoteValue(href) + " applies to " +
					quoteValue(strings.TrimSpace(media)) + ", and this engine evaluates no " +
					"media queries; it was not applied",
				Path:     PathOf(n),
				Property: "media",
			})
			return authorSheet{}, false
		}
		if !apply {
			// A sheet for a medium this is not. A page is printed, so a
			// screen-only sheet not applying is the correct answer rather than
			// a gap, and there is nothing to report.
			return authorSheet{}, false
		}
	}

	if l.failed[href] {
		// Already refused, and already reported. Retrying would be the same
		// answer at the cost of the same system calls.
		return authorSheet{}, false
	}
	// The document-wide count, checked before anything is read and against
	// sheets *applied*, so that the cache below cannot be used to get past it.
	if l.applied >= maxDocumentStylesheets {
		l.overCap(n, href)
		return authorSheet{}, false
	}
	if src, ok := l.cache[href]; ok {
		l.applied++
		return authorSheet{name: href, source: src}, true
	}

	src, fail := l.fetch(href)
	if fail != nil {
		l.failed[href] = true
		l.rec.ReportDetail(Finding{
			Rule:    fail.rule,
			Source:  AtHTML(n.Offset),
			Message: fail.message,
			Path:    PathOf(n),
		})
		return authorSheet{}, false
	}
	if l.cache == nil {
		l.cache = map[string]string{}
	}
	l.cache[href] = src
	l.applied++
	return authorSheet{name: href, source: src}, true
}

// overCap reports the document-wide count tripping.
//
// Two findings, because two different things are true and a caller filters on
// different ones: the guard tripped, which every other part of pdf0 reports as
// "limit"; and the document is missing styles it asked for, which is the thing
// that makes the page wrong. The first is raised once and the second names each
// sheet, because which stylesheet went missing is what an author needs.
func (l *sheetLoader) overCap(n *html.Node, href string) {
	if !l.capped {
		l.capped = true
		l.rec.Report(RuleLimit, NoSource, fmt.Sprintf(
			"this document applies more than the %d stylesheets this engine will load",
			maxDocumentStylesheets))
	}
	l.rec.ReportDetail(Finding{
		Rule:   RuleResourceBlocked,
		Source: AtHTML(n.Offset),
		Message: fmt.Sprintf("the stylesheet at %s was not applied: this document already "+
			"used the %d stylesheets this engine will load",
			quoteValue(href), maxDocumentStylesheets),
		Path: PathOf(n),
	})
}

// fetch obtains the text of one linked stylesheet, applying resource.go's
// policy and this file's size cap.
func (l *sheetLoader) fetch(href string) (string, *loadFailure) {
	data, fail := l.bytes(href)
	if fail != nil {
		return "", fail
	}
	if len(data) > maxStylesheetBytes {
		return "", &loadFailure{
			rule: RuleResourceBlocked,
			message: fmt.Sprintf(
				"the stylesheet at %s is %d bytes, more than the %d this engine will read",
				quoteValue(href), len(data), maxStylesheetBytes),
		}
	}
	return string(data), nil
}

// bytes is the fetch itself: the same three answers image.go's fetch gives, in
// the same order and for the same reasons.
func (l *sheetLoader) bytes(href string) ([]byte, *loadFailure) {
	if scheme, ok := schemeOf(href); ok {
		if scheme == "data" {
			return decodeDataURI(href, "stylesheet", RuleResourceBlocked)
		}
		return nil, &loadFailure{
			rule: RuleResourceBlocked,
			message: "the stylesheet at " + quoteValue(href) + " names the " + quoteValue(scheme) +
				" scheme; this engine resolves no URLs and fetches nothing, so it was not applied",
		}
	}
	if l.res == nil {
		return nil, &loadFailure{
			rule:    RuleResourceBlocked,
			message: "the stylesheet at " + quoteValue(href) + " was not loaded: " + ErrNoResolver.Error(),
		}
	}
	data, err := l.res.Resolve(href)
	if err != nil {
		return nil, &loadFailure{
			rule:    RuleResourceBlocked,
			message: "the stylesheet at " + quoteValue(href) + " was not loaded: " + err.Error(),
		}
	}
	if len(data) == 0 {
		// An empty file is a stylesheet with no rules, which is a legal thing
		// for a document to link and is not a failure of anything.
		return nil, nil
	}
	return data, nil
}

// relIsStylesheet reports whether a link's rel names it a stylesheet that
// applies.
//
// The attribute is a set of space-separated keywords, so "stylesheet" has to be
// one of them rather than a substring — "no-stylesheet" is not a stylesheet, and
// neither is a rel of "prefetch stylesheet-ish".
//
// "alternate stylesheet" is a stylesheet the reader may choose and that no
// browser applies unless they do. Nothing here has a reader to ask, so it is not
// applied, and that is the same answer a browser gives rather than a gap: there
// is nothing to report.
func relIsStylesheet(rel string) bool {
	var stylesheet, alternate bool
	for _, f := range strings.Fields(rel) {
		switch strings.ToLower(f) {
		case "stylesheet":
			stylesheet = true
		case "alternate":
			alternate = true
		}
	}
	return stylesheet && !alternate
}

// mediaAppliesToPaper says whether a link's media attribute admits this engine's
// output, and whether it could be read at all.
//
// Only the simple form is answered: a comma-separated list of bare media types,
// which is what a document that wants to distinguish print from screen writes.
// Anything with a feature query in it — "screen and (min-width: 40em)", "not
// print", "only screen" — is *not* answered, because answering it would mean
// evaluating a media query and this engine evaluates none. Guessing would be
// silent either way, and silent is what the reporting layer exists to prevent.
//
// The medium is print. A PDF page is a sheet of paper with a fixed size and no
// scrolling, which is the medium "print" was defined for, and it is why "screen"
// is a *correct* refusal here rather than an unimplemented one.
func mediaAppliesToPaper(media string) (applies, understood bool) {
	media = strings.TrimSpace(media)
	if media == "" {
		return true, true
	}
	for _, part := range strings.Split(media, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if !bareMediaTypes[part] {
			return false, false
		}
		if part == "all" || part == "print" {
			applies = true
		}
	}
	return applies, true
}

// bareMediaTypes is every media type that may stand alone in a media list.
//
// The deprecated ones are here because documents still carry them and because
// leaving them out would send a "media=tty" sheet down the unsupported path,
// where it would be reported as a query this engine cannot read. It can read it;
// the answer is simply no.
var bareMediaTypes = map[string]bool{
	"all": true, "print": true, "screen": true, "speech": true,
	"aural": true, "braille": true, "embossed": true, "handheld": true,
	"projection": true, "tty": true, "tv": true,
}
