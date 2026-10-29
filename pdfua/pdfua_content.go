package pdfua

import (
	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file owns the content-stream side of PDF/UA validation (ISO 14289-1
// 7.1/7.18/7.20): content must sit in a tagged or /Artifact marked-content
// sequence, the two kinds must not nest in one another, a form XObject holding
// an /MCID may be painted only once, and an OBJR-reached annotation must sit
// under the structure type its subtype implies. Each stream is tokenized once
// and the derived facts cached, since several checks read the same content;
// the property lists' /Lang values are among the facts, for pdfua_lang.go,
// which owns the language side of the content.

// uaCache is the PDF/UA engine's memo for one run: the per-stream content
// facts. It is reached through core.Slot rather than held on the shared run
// state, because nothing else reads it and the core package should not have to
// name these types.
type uaCache struct {
	streamFacts  map[*object.Stream]*streamContentFacts // stream -> real-content messages + Do names
	langEvents   map[*object.Stream][]langEvent         // stream -> language-scope events (pdfua_lang.go)
	untaggedForm map[*object.Stream][]string            // form with its own /Resources -> untaggedContent of it
	langForm     map[langFormKey][]string               // form with its own /Resources, outer scope -> langScope.replay of it
}

// uaSlot keys uaCache. An unexported empty struct cannot collide with another
// package's key.
type uaSlot struct{}

// uaMemo returns this run's PDF/UA memo.
func uaMemo(d core.View) *uaCache {
	c := core.Slot[uaCache](d.Run, uaSlot{})
	if c.streamFacts == nil {
		c.streamFacts = map[*object.Stream]*streamContentFacts{}
	}
	return c
}

// checkUARealContent implements Matterhorn checkpoint 01-005 (ISO 14289-1 7.1,
// veraPDF 7.1-t03 over every simple content item): on every page, real
// content — text, a painted path, an inline image, an image XObject, and what
// a form XObject paints — must appear inside a marked-content sequence, either
// tagged (linked to the structure tree) or an Artifact. Content drawn outside
// any marked-content sequence is neither, and is flagged; a clipping path that
// paints nothing is not content.
//
// A shading painted by sh is content too, and veraPDF reports one painted
// outside marked content, but it is not reported here: the PDF Association's
// conformant PDFUA-Reference-06 paints its page backgrounds that way (veraPDF
// 1.30.3 reports it, 7.1-3), and the reference files are this validator's
// false-positive oracle (docs/pdfua.md).
//
// It walks each page's content stream tracking marked-content nesting depth from
// BDC/BMC…EMC. (The Artifact/tagged *mis-nesting* conditions 01-003/01-004 need
// to distinguish structure-linked BDC from /OC and property-less BDC, which a
// plain depth count cannot; they are left to the structure-correlation pass.)
// A form XObject painted outside marked content is entered, its own content
// judged from outside any sequence, as veraPDF does; an image XObject so
// painted is reported.
func checkUARealContent(d core.View, cat *object.Dictionary) []Violation {
	var v []Violation
	for _, pg := range d.Pages(cat.Get("Pages")) {
		data, key, _ := d.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		for _, msg := range untaggedContent(d, data, key, d.Resources(pg.Dict), 0, map[*object.Stream]bool{}) {
			v = append(v, Violation{Clause: "7.1", Message: msg, Object: pg.ObjNum})
		}
	}
	return v
}

// untaggedContent is the real-content messages of one stream painted outside
// any marked-content sequence: its own, and those of the XObjects it paints
// there (path holds the forms being entered, so a form that paints itself is
// entered once).
func untaggedContent(d core.View, data []byte, key *object.Stream, res *object.Dictionary, depth int, path map[*object.Stream]bool) []string {
	if !d.Descend(depth) {
		return nil
	}
	f := contentFacts(d, data, key)
	msgs := append([]string(nil), f.realMsgs...)
	xobjs := d.ResolveDict(res.Get("XObject"))
	for _, name := range f.untaggedDo {
		d.Charge(1)
		x, _ := d.Resolve(xobjs.Get(object.Name(name))).(*object.Stream)
		if x == nil || path[x] {
			continue
		}
		switch st, _ := d.ResolveName(x.Dict.Get("Subtype")); st {
		case "Image":
			msgs = append(msgs, "page paints an image that is neither tagged nor marked as an /Artifact")
		case "Form":
			// A form with resources of its own is judged the same wherever it
			// is painted, so it is judged once: a form painted from many
			// pages, or from many forms, costs one walk.
			fres := d.ResolveDict(x.Dict.Get("Resources"))
			memo := uaMemo(d)
			if fres != nil {
				if m, ok := memo.untaggedForm[x]; ok {
					msgs = append(msgs, m...)
					continue
				}
			}
			own := fres != nil
			if !own {
				fres = res
			}
			fdata, _ := d.Content(x) // reason: presence-only; the producer recorded any declined trip
			path[x] = true
			m := untaggedContent(d, fdata, x, fres, depth+1, path)
			delete(path, x)
			if own {
				if memo.untaggedForm == nil {
					memo.untaggedForm = map[*object.Stream][]string{}
				}
				memo.untaggedForm[x] = m
			}
			msgs = append(msgs, m...)
		}
	}
	// Distinct, in first-seen order.
	seen := map[string]bool{}
	out := msgs[:0]
	for _, m := range msgs {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// streamContentFacts holds the facts several UA checks derive from a single
// tokenizing pass over one content stream: the real-content (7.1) violation
// messages and, for the form-XObject-painting rule (7.20), the sequence of
// XObject names invoked by Do operators, and for the language rule (7.2) the
// property lists' /Lang values. All depend only on the stream bytes, so a
// stream shared by many containers — or examined by more than one check — is
// tokenized once.
type streamContentFacts struct {
	realMsgs   []string // distinct real-content (7.1) messages, in first-seen order
	doNames    []string // the operand name in effect at each Do operator, in order
	untaggedDo []string // the Do operands painted outside any marked content, each once
	propLangs  [][]byte // the /Lang of each inline BDC property list, in order
	propNames  []string // the /Properties names BDC operators use, each once
}

// contentFacts returns the streamContentFacts for content, memoized per content
// stream (key) when a validation cache is present.
func contentFacts(d core.View, content []byte, key *object.Stream) *streamContentFacts {
	if key != nil {
		if f, ok := uaMemo(d).streamFacts[key]; ok {
			return f
		}
	}
	f := buildContentFacts(d.Cancel, content)
	if key != nil {
		uaMemo(d).streamFacts[key] = f
	}
	return f
}

// pathPaintOps are the path-painting operators (ISO 32000-1 Table 60) other
// than n, which ends a path without painting it.
var pathPaintOps = map[string]bool{"S": true, "s": true, "f": true, "F": true, "f*": true, "B": true, "B*": true, "b": true, "b*": true}

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
	once := map[string]bool{}
	addOnce := func(list *[]string, kind, name string) {
		if !once[kind+name] {
			once[kind+name] = true
			*list = append(*list, name)
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
	// Only a few things about an operator's operands matter here, so they are
	// tracked as the tokens stream past rather than buffered into a slice: is
	// the first operand the name /Artifact, is any operand the name /MCID, the
	// property list's /Lang, and what was the most recent name operand (the Do
	// target — which, unlike the others, deliberately carries across
	// operators). Buffering every operand meant copying a token struct for the
	// great majority of tokens in the stream, none of which is ever read back.
	firstOperand := true
	artifactTag := false // the first operand is the name /Artifact
	mcidProp := false    // some operand is the name /MCID
	var ops mcOperands   // the property list's /Lang, or its /Properties name
	// The lexer is read directly rather than through core.TokenizeContent,
	// which drops inline images: an inline image is content too.
	lx := core.NewContentLexer(cancel, content)
	var t core.ContentTok
	for lx.Next(&t) {
		tk, ok := contentToken(&t)
		if !ok {
			if t.Kind == core.ContentInlineImage && len(stack) == 0 {
				report("page paints an inline image that is neither tagged nor marked as an /Artifact")
			}
			continue
		}
		if tk.Kind != core.KindOp {
			ops.feed(tk)
			if tk.Kind == core.KindName {
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
			if tk.Op == "BDC" {
				if p := ops.props(); p.propName != "" {
					addOnce(&f.propNames, "p", p.propName)
				} else if ops.hasLang {
					f.propLangs = append(f.propLangs, ops.lang)
				}
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
			f.doNames = append(f.doNames, ops.lastName)
			if len(stack) == 0 {
				addOnce(&f.untaggedDo, "x", ops.lastName)
			}
		default:
			if pathPaintOps[tk.Op] && len(stack) == 0 {
				report("page paints a path that is neither tagged nor marked as an /Artifact")
			}
		}
		firstOperand, artifactTag, mcidProp = true, false, false
		ops.reset()
	}
	return f
}

// contentToken is the operand or operator a lexer token is, in the form
// core.TokenizeContent gives it; false for the tokens it drops (dictionary
// delimiters and inline images).
func contentToken(t *core.ContentTok) (core.ContentToken, bool) {
	switch t.Kind {
	case core.ContentOperator:
		return core.ContentToken{Kind: core.KindOp, Op: string(t.Raw)}, true
	case core.ContentNumber:
		return core.ContentToken{Kind: core.KindNumber, Raw: t.Raw}, true
	case core.ContentString, core.ContentHexString:
		return core.ContentToken{Kind: core.KindString, Str: t.Bytes()}, true
	case core.ContentName:
		return core.ContentToken{Kind: core.KindName, Name: t.Name()}, true
	case core.ContentArrayStart:
		return core.ContentToken{Kind: core.KindArrayStart}, true
	case core.ContentArrayEnd:
		return core.ContentToken{Kind: core.KindArrayEnd}, true
	}
	return core.ContentToken{}, false
}

// checkUAFormXObjectMCID enforces 7.20: a form XObject whose content is tagged
// (contains an /MCID marked-content sequence) must be painted at most once. If
// it is invoked by more than one Do operator, a single structure element would
// map to several renderings, breaking the one-to-one structure/content mapping.
func checkUAFormXObjectMCID(d core.View) []Violation {
	// The forms the document reaches: an orphan form is painted by nothing and
	// is not the document's (audit 2026-09-22 C83). A stream is always an
	// object of its own, so ObjNum is the form's.
	var forms []core.ReachableDict
	mcidForm := map[int]bool{}
	for _, r := range d.ReachableDicts() {
		if r.Stream == nil {
			continue
		}
		if st, _ := d.ResolveName(r.Dict.Get("Subtype")); st != "Form" {
			continue
		}
		forms = append(forms, r)
		if data, _ := d.Content(r.Stream); containsNameToken(d.Cancel, data, "MCID") { // reason: presence-only; the producer recorded any declined trip
			mcidForm[r.ObjNum] = true
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
	countDo := func(content []byte, key *object.Stream, res *object.Dictionary) {
		if res == nil {
			return
		}
		xobjs := d.ResolveDict(res.Get("XObject"))
		if xobjs == nil {
			return
		}
		name2num := map[string]int{}
		for k, xval := range xobjs.All() {
			if ref, ok := xval.(object.IndirectRef); ok {
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
	for _, pg := range d.Pages(d.CatalogPages()) {
		data, key, _ := d.ContentBytesAndKey(pg.Dict.Get("Contents")) // reason: presence-only; the producer recorded any declined trip
		countDo(data, key, d.ResolveDict(pg.Dict.Get("Resources")))
	}
	// Form XObject content sources (a form may invoke another form).
	for _, f := range forms {
		data, _ := d.Content(f.Stream) // reason: presence-only (counts invocations); the producer recorded any declined trip
		countDo(data, f.Stream, d.ResolveDict(f.Dict.Get("Resources")))
	}

	var v []Violation
	for _, num := range sortedInts(mcidForm) {
		if doCount[num] > 1 {
			v = append(v, Violation{Clause: "7.20", Message: "a form XObject containing marked content (/MCID) is painted by more than one Do operator", Object: num})
		}
	}
	return v
}

// containsNameToken reports whether a content stream contains the name token
// /name — as the content lexer reads it, so a "/MCID" inside a string, a
// comment or inline-image data is not one, and neither is "/MCIDExtra".
func containsNameToken(cancel core.Canceler, data []byte, name string) bool {
	lx := core.NewContentLexer(cancel, data)
	var t core.ContentTok
	for lx.Next(&t) {
		if t.Kind == core.ContentName && t.Name() == name {
			return true
		}
	}
	return false
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
//
// The enclosing element's type is its resolved type: /MyLink role-mapped to
// /Link is a Link element, and comparing the written /S reported "nested in a
// <MyLink> element, expected <Link>" on a conforming file (audit 2026-09-22
// C81). The OBJR is read from the flattened tree, which records the element
// that holds it as its parent.
func checkUAAnnotStructType(d core.View, cat *object.Dictionary) []Violation {
	nodes := structTree(d, cat)
	annotParent := map[int]object.Name{}
	for _, n := range nodes {
		// An OBJR references an object (often an annotation).
		if t, _ := d.ResolveName(n.Elem.Get("Type")); t != "OBJR" {
			continue
		}
		ref, ok := n.Elem.Get("Obj").(object.IndirectRef)
		if !ok {
			continue
		}
		var parentType object.Name
		if n.Parent >= 0 {
			parentType = nodes[n.Parent].StdType
		}
		annotParent[ref.Number] = parentType
	}

	var v []Violation
	for _, r := range d.ReachableDicts() {
		a, num := r.Dict, r.ObjNum
		if r.Stream != nil || !r.Top || !d.IsAnnotation(a) {
			continue
		}
		st, _ := d.ResolveName(a.Get("Subtype"))
		if st == "Popup" {
			continue
		}
		if f, _ := d.Resolve(a.Get("F")).(object.Integer); int(f)&0x2 != 0 {
			continue
		}
		parent, linked := annotParent[num]
		if !linked {
			continue // not reached via OBJR; the tagging check covers presence
		}
		// Each kind's own rule: a widget's is 7.18.4, a link's 7.18.5, and
		// every other annotation's 7.18.1 (veraPDF 7.18.4-1, 7.18.5-1).
		want, clause := object.Name("Annot"), "7.18.1"
		switch st {
		case "Widget":
			want, clause = "Form", "7.18.4"
		case "Link":
			want, clause = "Link", "7.18.5"
		}
		if parent != want {
			v = append(v, Violation{Clause: clause, Message: "annotation of subtype /" + string(st) + " is nested in a <" + string(parent) + "> element, expected <" + string(want) + ">", Object: num})
		}
	}
	return v
}
