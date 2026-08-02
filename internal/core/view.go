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
	// Offsets records the absolute byte offset of each uncompressed indirect
	// object, for the byte-level file-structure rules. Objects materialised from
	// object streams are absent.
	Offsets map[int]int64
	// What Read found in this file. The byte-level and embedded-file rules read
	// these: a checker must be able to say "this object stream would not decode"
	// rather than silently reporting the objects it could not see as absent.
	//
	// BrokenObjStms lists object-stream containers whose contents did not decode.
	// DecryptFailures lists objects whose ciphertext did not decrypt under a
	// known-good key. UsedXRefStream records that the primary cross-reference
	// section was a stream. EmbeddedDepth is 0 for a top-level document and 1
	// inside the recursive embedded-PDF/A check, which is what stops it recursing.
	BrokenObjStms   []int
	DecryptFailures []int
	UsedXRefStream  bool
	EmbeddedDepth   int
	// Encrypted reports whether the file carried an /Encrypt dictionary. It is
	// the flag, not a question about whether the content is currently readable:
	// a file decrypted on Read keeps it set.
	Encrypted bool
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

	// pages memoizes the flattened page tree per page-tree node, and content the
	// decoded bytes of each content stream, with contentBytes charging the
	// aggregate decode budget across the whole operation.
	//
	// These are document services rather than any one subsystem's: the page walk
	// and the decoded content feed PDF/A, PDF/UA and image extraction alike, and
	// the aggregate budget only means anything if all of them charge the same
	// counter.
	pages        map[int][]PageInfo
	content      map[*object.Stream][]byte
	contentBytes int64

	// psProgs memoizes parsed type-4 (PostScript calculator) function programs.
	// A tint transform is evaluated once per image pixel, and without this each
	// evaluation re-decoded and re-parsed the program stream, turning a small
	// image into minutes of work.
	psProgs map[*object.Stream]psProgEntry

	// Font usage: which fonts the document shows text in, and the per-stream
	// skeletons the walk replays. PDF/A and PDF/UA both consume these, which is
	// why they are here rather than under either.
	fontUsage      map[*object.Dictionary]*FontTextUsage
	fontUsageValid bool
	fontEvents     map[*object.Stream][]FontEvent
	usedNames      map[*object.Stream]UsedResourceNames

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
func NewRun(trips *Recorder) *Run {
	return &Run{
		Trips:      trips,
		pages:      make(map[int][]PageInfo),
		content:    make(map[*object.Stream][]byte),
		psProgs:    make(map[*object.Stream]psProgEntry),
		fontEvents: make(map[*object.Stream][]FontEvent),
		usedNames:  make(map[*object.Stream]UsedResourceNames),
		slots:      map[any]any{},
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

// Guard identifiers for the two budgets this file charges. The rest live with
// the guards that raise them, in the root package; these two are here because
// Content is what trips them.
const (
	GuardContentStream = "content-stream-size"   // Limits.ContentStreamBytes, WithMaxContentStreamBytes
	GuardContentTotal  = "decoded-content-total" // Limits.DecodedContentBytes, WithMaxDecodedContentBytes
	GuardCmapWork      = "cmap-work"             // Limits.CmapWork, WithMaxCmapWork
	GuardGridFills     = "table-grid-fills"      // Limits.TableGridFills, WithMaxTableGridFills
	GuardRoleMapWork   = "rolemap-work"          // Limits.RoleMapSteps, WithMaxRoleMapSteps
	GuardCIDWidthRange = "cid-width-range"       // Limits.CIDRangeSpan, WithMaxCIDRangeSpan
	GuardEmbeddedPDFA  = "embedded-pdfa"         // no bound of its own; the recursive embedded check
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
	switch nodeType, _ := node.Get("Type").(object.Name); nodeType {
	case "Pages":
		if kids, ok := v.Resolve(node.Get("Kids")).(object.Array); ok {
			for _, kid := range kids {
				v.collectPages(kid, pages, seen)
			}
		}
	case "Page":
		*pages = append(*pages, PageInfo{Dict: node, ObjNum: objNum})
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

// Content returns a content stream's decoded bytes, memoized for the operation
// and charged against two budgets.
//
// A stream over the per-stream scanning limit, and every stream once the
// aggregate has been spent, decode to nil — and the nil is cached, so the
// decision is stable across the several checks that walk the same page rather
// than being re-taken as the budget moves. Both refusals are reported, because
// a check that sees nothing here must not conclude the file contains nothing.
func (v View) Content(stream *object.Stream) []byte {
	if v.Run != nil {
		if data, ok := v.Run.content[stream]; ok {
			return data
		}
		if v.Run.contentBytes >= v.Limits.DecodedContentBytes {
			v.Run.content[stream] = nil
			v.Note(GuardContentTotal, "this run has already decoded "+itoa(v.Run.contentBytes)+" bytes of content, reaching the "+LimitBound(v.Limits.DecodedContentBytes, DefaultMaxDecodedContentBytes)+"-byte budget for one run; the remaining content streams were not decoded, so no content-driven rule was applied to them", 0)
			return nil
		}
	}
	var data []byte
	decoded, err := DecodeStreamData(v.Cancel, stream, v.Limits)
	switch {
	case err == nil && len(decoded) <= v.Limits.ContentStreamBytes:
		data = decoded
	case err == nil:
		v.Note(GuardContentStream, "a content stream decodes to "+itoa(int64(len(decoded)))+" bytes, over the "+LimitBound(int64(v.Limits.ContentStreamBytes), DefaultMaxContentStreamBytes)+"-byte scanning limit; it was not scanned", 0)
	}
	if v.Run != nil {
		v.Run.content[stream] = data
		v.Run.contentBytes += int64(len(data))
	}
	return data
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
func (v View) MetadataContent(stream *object.Stream) []byte {
	if v.Run != nil {
		if data, ok := v.Run.content[stream]; ok {
			return data
		}
	}
	var data []byte
	if decoded, err := DecodeStreamData(v.Cancel, stream, v.Limits); err == nil && len(decoded) <= v.Limits.ContentStreamBytes {
		data = decoded
	}
	if v.Run != nil {
		v.Run.content[stream] = data
		v.Run.contentBytes += int64(len(data))
	}
	return data
}

// FontEventsMemoSize reports how many content streams the font-usage walk has
// tokenized. It exists for the test that pins the sharing: a stream referenced
// by thousands of pages must be tokenized once, and the only way to see that
// from outside is to count what the memo holds.
func (r *Run) FontEventsMemoSize() int {
	if r == nil {
		return 0
	}
	return len(r.fontEvents)
}
