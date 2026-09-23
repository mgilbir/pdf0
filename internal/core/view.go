package core

import (
	"strconv"

	"github.com/mgilbir/pdf0/object"
)

// View is what a subsystem needs in order to read a document: the object graph,
// enough of the file's identity to judge it, the budget it must stay inside, and
// somewhere to say so when it cannot.
//
// It exists so that a subsystem does not have to name Document. Document carries
// the public API — twenty-eight exported methods that callers depend on — and a
// method must be declared in the package that declares its type, so Document
// cannot move below the packages that would need it without taking the whole
// public surface along. A View is passed *down* instead: Document builds one and
// hands it over, which leaves the facade at the top of the graph where it
// belongs and nothing underneath depending on it.
//
// A View is a value, and a cheap one: five words plus two pointers. Build it per
// call rather than caching it, so that a Document mutated between operations is
// never read through a stale copy. What must be shared across the calls of one
// operation lives behind Run.
type View struct {
	// Objects is the object graph, keyed by object number. It is the same map
	// the Document holds, not a copy: resolving through a View sees whatever the
	// Document currently holds.
	Objects map[int]*object.IndirectObject
	// Trailer is the file trailer.
	Trailer *object.Dictionary
	// Version is the header version, "1.7" or "2.0".
	Version string
	// File returns the record of the file the document was read from, for the
	// byte-level file-structure rules: its bytes and what Read learned about
	// them (see FileRecord). It is nil for a document built in memory. It is a
	// function, and the record is built on its first call, so that operations
	// that never read the file's bytes do not pay for it; read it through
	// FileRecord.
	File func() *FileRecord
	// What Read found in this file. The byte-level and embedded-file rules read
	// these: a checker must be able to say "this object stream would not decode"
	// rather than silently reporting the objects it could not see as absent.
	//
	// BrokenObjStms lists object-stream containers whose contents did not decode.
	// DecryptFailures lists objects whose ciphertext did not decrypt under a
	// known-good key. UsedXRefStream records that the primary cross-reference
	// section was a stream. EmbeddedDepth is 0 for a top-level document and 1
	// inside the recursive embedded-PDF/A check, which is what stops it recursing.
	//
	// SkippedObjStms lists the containers Read did not unpack by request — over
	// the object-stream budget, a filter pdf0 does not implement, or ciphertext
	// it could not decrypt — each with the Reason. They are not malformed, and
	// no rule may say they are; the trip was recorded when Read met it.
	BrokenObjStms   []int
	SkippedObjStms  []SkippedObjStm
	DecryptFailures []int
	UsedXRefStream  bool
	EmbeddedDepth   int
	// Encrypted reports whether the file carried an /Encrypt dictionary. It is
	// the flag, not a question about whether the content is currently readable:
	// a file decrypted on Read keeps it set.
	Encrypted bool
	// Locked reports that the file is encrypted and was not decrypted: every
	// string and stream (bar the exemptions of ISO 32000-2 7.6.2) is
	// ciphertext. Producers answer ReasonLocked for such data and record the
	// trip; the string readers a check uses decline the same way (TextString).
	Locked bool
	// Limits is the resolved resource budget for this document — resolved, not
	// raw. Document.view fills it through Document.lim, which applies the
	// defaults.
	//
	// This differs from Limits elsewhere in the package, where the zero value
	// means "give me the defaults". Here the zero value is a budget of zero, and
	// a View built by hand without setting it refuses to decode anything while
	// reporting no error. Callers that read v.Limits directly would not be
	// protected by resolving it inside this type's methods, so the contract is
	// stated rather than half-defended: set it, or build the View from a
	// Document.
	Limits Limits
	// Cancel is this operation's cancellation signal, or the zero value when the
	// operation cannot be cancelled.
	Cancel Canceler
	// Alloc returns an object number nothing uses — no object in Objects and no
	// number the source file uses, including the cross-reference and object
	// streams Read removed from Objects — for a subsystem that adds objects to
	// the document. Document.view sets it to the Document's one allocator. It is
	// nil on a hand-built View, and a function that needs it refuses rather than
	// numbering objects itself: one past the highest key in Objects is a number
	// the source file may still use (audit 2026-09-22 C3).
	Alloc func() int
	// Run holds what the calls of one operation share. It is nil outside a run —
	// a bare Read, say — and every method here tolerates that.
	Run *Run
}

// Run is the state whose lifetime is exactly one operation: the trips recorded
// while it ran, and the memos that keep a traversal from being repeated.
//
// It is a pointer inside View precisely so that a View copied by value still
// shares it. Copying a View must never fork the memo table, or the second copy
// silently redoes work the first already did — which is invisible in the output
// and shows up only as time.
type Run struct {
	// Trips collects the guard trips of this operation. May be nil.
	Trips *Recorder

	// Meter is the operation's work budget (meter.go). Every walk, expansion,
	// tokenisation and evaluation of the operation charges it. May be nil, and
	// then nothing is metered.
	Meter *Meter

	// pages memoizes the flattened page tree per page-tree node, and content the
	// decoded bytes of each content stream, with contentBytes charging the
	// aggregate decode budget across the whole operation.
	//
	// These are document services rather than any one subsystem's: the page walk
	// and the decoded content feed PDF/A, PDF/UA and image extraction alike, and
	// the aggregate budget only means anything if all of them charge the same
	// counter.
	pages        map[int][]PageInfo
	content      map[*object.Stream]contentEntry
	contentBytes int64

	// psProgs memoizes parsed type-4 (PostScript calculator) function programs.
	// A tint transform is evaluated once per image pixel, and without this each
	// evaluation re-decoded and re-parsed the program stream, turning a small
	// image into minutes of work.
	psProgs map[*object.Stream]psProgEntry

	// usedNames memoizes, per content stream, the resource names it invokes.
	// The content interpreter (interp.go), which answers device colour and
	// font usage, keeps its memo in a slot.
	usedNames map[*object.Stream]UsedResourceNames

	// dictNum is a reverse index, dictionary value -> object number, backing
	// DictObjNum. It answers with the lowest number when a dictionary is the
	// value of more than one object, so a report is reproducible.
	dictNum map[*object.Dictionary]int

	// slots holds per-subsystem memos, keyed by a type the subsystem owns. See
	// Slot.
	slots map[any]any
}

// Slot returns the run's memo of type T, creating it on first use.
//
// It exists so that a subsystem can memoize across one operation without this
// package having to name its types. The memos above — pages, content, fonts —
// are here because several subsystems share them; a memo only one subsystem
// reads belongs to that subsystem, and this is how it keeps it while still
// living exactly as long as the run does.
//
// key identifies the slot and is conventionally an empty struct type declared
// by the caller, which makes collisions impossible: two packages cannot name
// the same unexported type.
//
// Outside a run — a nil Run — every call returns a fresh T. Callers therefore
// get correct answers and no memoization, which is what they got before this
// existed.
func Slot[T any](r *Run, key any) *T {
	if r == nil {
		return new(T)
	}
	if v, ok := r.slots[key]; ok {
		return v.(*T)
	}
	p := new(T)
	if r.slots == nil {
		r.slots = map[any]any{}
	}
	r.slots[key] = p
	return p
}

// NewRun builds the per-operation state. The memo tables are made here so that
// every entry point that starts an operation gets them, rather than each having
// to remember.
//
// The run it returns is not metered; an entry point that starts an operation
// on untrusted input uses NewMeteredRun.
func NewRun(trips *Recorder) *Run {
	return &Run{
		Trips:     trips,
		pages:     make(map[int][]PageInfo),
		content:   make(map[*object.Stream]contentEntry),
		psProgs:   make(map[*object.Stream]psProgEntry),
		usedNames: make(map[*object.Stream]UsedResourceNames),
		slots:     map[any]any{},
	}
}

// PageInfo is one page of the flattened page tree: the page dictionary and the
// object number it was reached through (0 for a direct dictionary).
type PageInfo struct {
	Dict   *object.Dictionary
	ObjNum int
}

// Resolve follows an indirect reference to the object it names, chasing
// reference-to-reference chains. It returns obj unchanged when obj is not a
// reference, and nil when the reference names an object the file does not
// contain.
//
// The hop count doubles as the cycle guard. A bounded loop costs nothing on
// what is the hottest path in the package, where a visited set would allocate
// on every call; real files chain a handful of hops at most, so exceeding the
// bound means a cycle or garbage either way.
func (v View) Resolve(obj object.Object) object.Object {
	for hops := 0; hops < 64; hops++ {
		ref, ok := obj.(object.IndirectRef)
		if !ok {
			return obj
		}
		iobj, exists := v.Objects[ref.Number]
		if !exists {
			return nil
		}
		obj = iobj.Value
	}
	return nil
}

// ResolveDict resolves obj and type-asserts to *object.Dictionary, returning nil
// when it resolves to anything else. The nil covers both "absent" and "present
// but the wrong type", because a caller that wanted a dictionary can do nothing
// useful with either.
func (v View) ResolveDict(obj object.Object) *object.Dictionary {
	d, _ := v.Resolve(obj).(*object.Dictionary)
	return d
}

// ResolveName, ResolveInt and ResolveBool are ResolveDict for the three scalar
// types a rule branches on, and they exist because asserting without resolving
// is how a rule gets skipped.
//
// Almost nothing in ISO 32000 requires an entry to be a direct object, so
// `/Subtype 5 0 R` naming `/Widget` is a legal way to write a widget
// annotation. A check that reads it as `dict.Get("Subtype").(object.Name)` gets
// the empty name back, decides the annotation is not a widget, and returns
// without looking — which turns "shall not" into "shall not, unless you write
// it indirectly". The same shape hides a transparency group behind `/S`, an
// action type behind `/S`, and an overprint mode behind `/OPM`.
//
// The second return distinguishes absent from present-but-wrong-type only
// where a caller acts on the difference; most read the value and compare it.
func (v View) ResolveName(obj object.Object) (object.Name, bool) {
	n, ok := v.Resolve(obj).(object.Name)
	return n, ok
}

// ResolveInt resolves obj and type-asserts to object.Integer.
func (v View) ResolveInt(obj object.Object) (object.Integer, bool) {
	i, ok := v.Resolve(obj).(object.Integer)
	return i, ok
}

// ResolveBool resolves obj and type-asserts to object.Boolean.
func (v View) ResolveBool(obj object.Object) (object.Boolean, bool) {
	b, ok := v.Resolve(obj).(object.Boolean)
	return b, ok
}

// ResolveNumber resolves obj and returns it as a float64 when it is an
// Integer or a Real — the two spellings of a PDF number, either of which a
// widths array, a rectangle or an opacity may use.
func (v View) ResolveNumber(obj object.Object) (float64, bool) {
	switch n := v.Resolve(obj).(type) {
	case object.Integer:
		return float64(n), true
	case object.Real:
		return float64(n), true
	}
	return 0, false
}

// Catalog returns the document catalog, or nil when the trailer names no /Root
// or names one that is not a dictionary.
func (v View) Catalog() *object.Dictionary {
	if v.Trailer == nil {
		return nil
	}
	root := v.Trailer.Get("Root")
	if root == nil {
		return nil
	}
	return v.ResolveDict(root)
}

// Note records that a guard stopped this operation short. It is a no-op outside
// a run, which is what lets a guard report unconditionally without first asking
// whether anyone is listening.
//
// What becomes of the trip afterwards — which rule identifier it carries, which
// finding type it turns into — is not decided here. That belongs with the
// findings, in the package that defines them.
func (v View) Note(guard, detail string, obj int) {
	if v.Run == nil {
		return
	}
	v.Run.Trips.Note(guard, detail, obj)
}

// Guard identifiers: the stable strings that name which budget stopped a check.
// They live here, in the package the budgets themselves live in, so that every
// package that raises one and every test that asserts on one names the same
// constant rather than repeating the string.
const (
	GuardContentStream = "content-stream-size"       // Limits.ContentStreamBytes, WithMaxContentStreamBytes
	GuardContentTotal  = "decoded-content-total"     // Limits.DecodedContentBytes, WithMaxDecodedContentBytes
	GuardCmapWork      = "cmap-work"                 // Limits.CmapWork, WithMaxCmapWork
	GuardGridFills     = "table-grid-fills"          // Limits.TableGridFills, WithMaxTableGridFills
	GuardEmbeddedPDFA  = "embedded-pdfa"             // no bound of its own; the recursive embedded check
	GuardObjStmTotal   = "objstm-decompressed-total" // Limits.ObjectStreamBytes, WithMaxObjectStreamBytes; the string predates metering materialised objects, and is kept because callers key on it
	GuardDecodedStream = "decoded-stream-size"       // Limits.DecodedStreamBytes, WithMaxDecodedStreamBytes
	GuardImagePixels   = "image-pixels"              // Limits.ImagePixels, WithMaxImagePixels

	// GuardUnsupportedFilter is not a resource guard: a stream the check
	// needed is encoded with a filter pdf0 does not implement. It is reported
	// the same way for the reason GuardPredefinedCMap is.
	GuardUnsupportedFilter = "unsupported-filter" // no bound

	// GuardPredefinedCMap is not a resource guard either. No budget stopped
	// anything: the code-to-CID data for the predefined CJK CMaps is not
	// carried by this module, so the checks that need it cannot run at all.
	// It is reported through the same mechanism because the consequence is the
	// same one a budget has — a check did not run, and a caller who is told
	// only "no violations" would read that as "checked and clean".
	GuardPredefinedCMap = "predefined-cmap" // no bound; LoadCMap reports it

	// GuardEmbeddedCMap is an embedded CMap that builds on a CMap which is
	// neither predefined nor embedded: the codes it leaves to that CMap have
	// no known CID, and the checks that need one are skipped for them, and
	// say so (LoadCMap).
	GuardEmbeddedCMap = "embedded-cmap" // no bound; see LoadCMap

	// GuardNoSourceFile is not a resource guard: the document was built in
	// memory, so there is no file for the byte-level rules to read (View.File
	// is nil). They did not run, and a caller told only "no violations" would
	// read that as "checked and clean".
	GuardNoSourceFile = "no-source-file" // no bound; see FileRecord
)

// Pages returns the page tree under ref flattened into document order,
// memoized per page-tree node for the operation.
//
// A page tree is a graph the file controls, so the walk carries a visited set:
// a /Kids cycle would otherwise recurse until the stack ran out.
func (v View) Pages(ref object.Object) []PageInfo {
	iref, isRef := ref.(object.IndirectRef)
	if v.Run != nil && isRef {
		if pages, ok := v.Run.pages[iref.Number]; ok {
			return pages
		}
	}
	var pages []PageInfo
	v.collectPages(ref, &pages, make(map[int]bool))
	if v.Run != nil && isRef {
		v.Run.pages[iref.Number] = pages
	}
	return pages
}

func (v View) collectPages(ref object.Object, pages *[]PageInfo, seen map[int]bool) {
	v.walkPageTree(ref, seen, InheritedAttrs{}, 0, func(p PageInfo, _ InheritedAttrs) {
		*pages = append(*pages, p)
	})
}

// InheritablePageAttrs are the page attributes a page takes from its ancestors
// in the page tree when it does not state them itself: ISO 32000-2 Table 31
// marks exactly these four inheritable, and 7.7.3.4 says no other attribute is.
var InheritablePageAttrs = [4]object.Name{"Resources", "MediaBox", "CropBox", "Rotate"}

// InheritedAttrs holds, for each of InheritablePageAttrs in order, the value
// that applies to a page: its own entry, or else the nearest ancestor's, as
// written (an indirect reference is not resolved). nil means neither the page
// nor any ancestor states it.
type InheritedAttrs [4]object.Object

// PageWithAttrs is a page of the flattened tree together with the inheritable
// attributes that apply to it.
type PageWithAttrs struct {
	PageInfo
	Attrs InheritedAttrs
}

// PagesWithAttrs is Pages with the inheritable attributes of each page resolved
// the way 7.7.3.4 defines them: from the page's position in the tree, walking
// down from the root, rather than by following /Parent up from the page. The
// two agree on a well-formed tree; where /Parent is missing or wrong — a direct
// /Pages root cannot be named by any /Parent at all — only the tree position is
// what the file says. It visits exactly the pages Pages does, in the same order.
func (v View) PagesWithAttrs(ref object.Object) []PageWithAttrs {
	var out []PageWithAttrs
	v.walkPageTree(ref, make(map[int]bool), InheritedAttrs{}, 0, func(p PageInfo, a InheritedAttrs) {
		out = append(out, PageWithAttrs{PageInfo: p, Attrs: a})
	})
	return out
}

// walkPageTree is the one page-tree traversal: nodes typed /Pages are
// descended, nodes typed /Page are visited, anything else is skipped, and a
// node reached through a reference already seen is not entered again (a /Kids
// cycle would otherwise recurse until the stack ran out). An acyclic tree
// deeper than MaxWalkDepth is refused by the depth guard, and every node
// visited is charged to the run's work meter.
func (v View) walkPageTree(ref object.Object, seen map[int]bool, inherited InheritedAttrs, depth int, visit func(PageInfo, InheritedAttrs)) {
	if !v.Descend(depth) {
		return
	}
	v.Charge(1)
	objNum := 0
	if iref, ok := ref.(object.IndirectRef); ok {
		objNum = iref.Number
		if seen[objNum] {
			return // cycle in the page tree
		}
		seen[objNum] = true
	}
	node := v.ResolveDict(ref)
	if node == nil {
		return
	}
	nodeType, _ := v.ResolveName(node.Get("Type"))
	if nodeType != "Pages" && nodeType != "Page" {
		return
	}
	for i, key := range InheritablePageAttrs {
		if val := node.Get(key); val != nil {
			inherited[i] = val
		}
	}
	if nodeType == "Page" {
		visit(PageInfo{Dict: node, ObjNum: objNum}, inherited)
		return
	}
	if kids, ok := v.Resolve(node.Get("Kids")).(object.Array); ok {
		for _, kid := range kids {
			v.walkPageTree(kid, seen, inherited, depth+1, visit)
		}
	}
}

// Resources returns a page's resource dictionary, following the /Parent chain
// when the page does not carry one itself.
func (v View) Resources(page *object.Dictionary) *object.Dictionary {
	return v.ResolveDict(v.InheritedPageAttr(page, "Resources"))
}

// InheritedPageAttr looks up an inheritable page attribute — /Resources,
// /MediaBox, /CropBox, /Rotate — walking up the /Parent chain when the page
// does not define it. Pages routinely inherit these from their Pages node,
// which a direct Get misses entirely.
func (v View) InheritedPageAttr(page *object.Dictionary, key object.Name) object.Object {
	node := page
	for hops := 0; node != nil && hops < 64; hops++ {
		if got := node.Get(key); got != nil {
			return got
		}
		node = v.ResolveDict(node.Get("Parent"))
	}
	return nil
}

// contentEntry is one memoized Content result. overTotal marks a refusal
// because the run's aggregate was spent, which MetadataContent does not honour.
type contentEntry struct {
	data      []byte
	reason    Reason
	overTotal bool
}

// Content returns a content stream's decoded bytes, memoized for the operation
// and charged against two budgets, with the Reason they are what they are.
//
// A stream over the per-stream scanning limit, and every stream once the
// aggregate has been spent, decode to nil with ReasonLimit — and the result is
// cached, so the decision is stable across the several checks that walk the
// same page rather than being re-taken as the budget moves. Every declined
// outcome is recorded here, by the producer (see Reason), because a check that
// sees nothing must not conclude the file contains nothing.
func (v View) Content(stream *object.Stream) ([]byte, Reason) {
	if v.Run != nil {
		// One unit a request, whether or not it is memoised: a stream asked
		// for once per referrer costs once per referrer.
		v.Charge(1)
		if e, ok := v.Run.content[stream]; ok {
			return e.data, e.reason
		}
		if v.Run.contentBytes >= v.Limits.DecodedContentBytes {
			v.Run.content[stream] = contentEntry{reason: ReasonLimit, overTotal: true}
			v.Note(GuardContentTotal, "this run reached the "+LimitBound(v.Limits.DecodedContentBytes, DefaultMaxDecodedContentBytes)+"-byte budget of decoded content for one run; the remaining content streams were not decoded, so no content-driven rule was applied to them", 0)
			return nil, ReasonLimit
		}
	}
	data, r := v.DecodeLimited(stream)
	if v.Run != nil {
		v.Run.content[stream] = contentEntry{data: data, reason: r}
		v.Run.contentBytes += int64(len(data))
	}
	return data, r
}

// DecodeLimited decodes stream and holds the result to the per-stream scanning
// limit (Limits.ContentStreamBytes), reporting either refusal. Unlike Content
// it is neither memoized nor charged to the run's aggregate: it is for data
// used once, such as image samples, that would only bloat the memo.
func (v View) DecodeLimited(stream *object.Stream) ([]byte, Reason) {
	data, r := v.Decode(stream)
	// Decoding is work in proportion to what it produces.
	v.ChargeScan(len(data))
	if r == ReasonOK && len(data) > v.Limits.ContentStreamBytes {
		v.noteDeclined(stream, ReasonLimit, GuardContentStream, "a stream decodes to "+itoa(int64(len(data)))+" bytes, over the "+LimitBound(int64(v.Limits.ContentStreamBytes), DefaultMaxContentStreamBytes)+"-byte scanning limit; it was not scanned")
		return nil, ReasonLimit
	}
	return data, r
}

// StreamFilters returns a stream's /Filter chain as a list of names, whether it
// was written as a single name or an array.
func (v View) StreamFilters(st *object.Stream) []object.Name {
	switch f := v.Resolve(st.Dict.Get("Filter")).(type) {
	case object.Name:
		return []object.Name{f}
	case object.Array:
		var out []object.Name
		for _, e := range f {
			if n, ok := v.Resolve(e).(object.Name); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// MetadataContent decodes a metadata stream, sharing Content's memo but exempt
// from the aggregate budget.
//
// The exemption is deliberate. /Metadata is the document's own identification,
// and a checker that cannot read it must conclude nothing rather than "this
// file is unidentified". Under the shared budget a flate-bombed document — whose
// page content exhausts the aggregate before the identification checks run — had
// its XMP read as empty, and the rules then reported a missing pdfaid:part, a
// missing dc:title and a non-compliant embedded file against a document that
// declares all three.
//
// It is still bounded per stream by the scanning limit, and the bytes are still
// charged, so the exemption does not unbound the operation: it only stops one
// stream being refused because of what other streams already cost.
func (v View) MetadataContent(stream *object.Stream) ([]byte, Reason) {
	if v.Run != nil {
		if e, ok := v.Run.content[stream]; ok && !e.overTotal {
			return e.data, e.reason
		}
	}
	data, r := v.DecodeLimited(stream)
	if v.Run != nil {
		v.Run.content[stream] = contentEntry{data: data, reason: r}
		v.Run.contentBytes += int64(len(data))
	}
	return data, r
}
