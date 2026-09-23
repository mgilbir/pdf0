package sign

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/object"
)

// This file decides whether the changes made to a file after a signature are
// permitted: the allowed-changes analysis ISO 32000-2 12.8.2.2.2 describes
// ("PDF processors may compare the signed and current versions of the document
// to see whether there have been modifications to any objects that are not
// permitted").
//
// It replaces a relaxation that treated any document time-stamp covering the
// whole file as making every earlier change acceptable (audit 2026-09-22 C1).
// A time-stamp proves when bytes existed, not that a change was permitted: a
// public time-stamp authority stamps any hash it is sent, so a page tampered
// with after signing and then time-stamped was reported as a conformant PAdES
// signature. Here every changed object is classified on its own, and a
// time-stamp is simply one more change that must itself be permitted.
//
// Three kinds of change are permitted:
//
//   - Archival additions (ETSI EN 319 142-1 5.4, ISO 32000-2 12.8.4.3 and
//     12.8.5), which "shall not be considered as changes to the document"
//     whatever the certification level (ISO 32000-2 Table 257): a Document
//     Security Store and what it holds, and document time-stamp fields with
//     their signature dictionaries and widgets.
//   - Signing, unless a certification signature in the signed revision forbids
//     it (DocMDP P 1): a new signature field and its value, or a value given
//     to a signature field the signed revision left empty and no FieldMDP
//     transform of an earlier signature locked. Without a certification
//     signature no modification-detection restriction applies (ISO 32000-2
//     12.8.2.2), and adding signatures in later revisions is the ordinary
//     multi-signer workflow ETSI EN 319 142-1 and the reference validators
//     accept.
//   - The edits to existing objects that attaching those requires, and nothing
//     else in them: the catalog gains or changes /DSS and /AcroForm; the
//     interactive form gains fields at the end of /Fields and signature bits
//     in /SigFlags; a page gains widgets at the end of /Annots.
//
// Everything else is not permitted, and is reported. That includes, as a
// documented gap, the form filling and annotation changes DocMDP P 2 and 3
// also permit: recognising them faithfully means judging every field type's
// value and appearance, which this analysis does not do, so they are reported
// as not permitted rather than waved through.
//
// The comparison is between objects as the file stores them (see
// core.RevisionDiff), and it is conservative: an edit to an existing object is
// permitted only when the object's before and after differ in nothing but the
// keys the attachment requires, with the values those keys may take.

// maxChangeDescriptions caps the descriptions of disallowed changes one
// signature reports; a crafted update can change every object in the file.
const maxChangeDescriptions = 50

// changeState reads one side of a revision diff.
type changeState struct {
	r   core.ObjectReader
	err error
}

// get returns object num, remembering the first read error.
func (s *changeState) get(num int) object.Object {
	o, err := s.r.Object(num)
	if err != nil && s.err == nil {
		s.err = fmt.Errorf("object %d: %w", num, err)
	}
	return o
}

// resolve follows a chain of references, bounded as core.View.Resolve is.
func (s *changeState) resolve(o object.Object) object.Object {
	for hops := 0; hops < 32; hops++ {
		ref, ok := o.(object.IndirectRef)
		if !ok {
			return o
		}
		o = s.get(ref.Number)
	}
	return nil
}

func (s *changeState) dict(o object.Object) *object.Dictionary {
	switch v := s.resolve(o).(type) {
	case *object.Dictionary:
		return v
	case *object.Stream:
		return &v.Dict
	}
	return nil
}

func (s *changeState) array(o object.Object) object.Array {
	a, _ := s.resolve(o).(object.Array)
	return a
}

func (s *changeState) name(o object.Object) object.Name {
	n, _ := s.resolve(o).(object.Name)
	return n
}

// refTarget returns the object number o refers to, or 0 for a direct value.
func refTarget(o object.Object) int { return object.RefNum(o) }

// changeClassifier holds one analysis.
type changeClassifier struct {
	old, cur *changeState
	changes  map[int]core.ObjectChange
	// docMDP is the P value of the signed revision's certification signature,
	// or 0 when no certification signature governs it.
	docMDP int
	// locks are the FieldMDP transforms of the signed revision's signatures,
	// and names the qualified name of each field of its form.
	locks []fieldLock
	names map[int]string

	// explained records the changes already found permitted.
	explained map[int]bool
	// fields and widgets are the permitted fields added to the form and their
	// widget annotations.
	fields, widgets map[int]bool
	// notes are reasons attached to changes that were recognised but are not
	// permitted, used in their descriptions.
	notes map[int]string
}

// disallowedChanges returns a description of every change in diff that is not
// permitted, or nil when all are. An error reading the states is itself
// reported: the changes are then unknown, which is never "permitted".
func disallowedChanges(diff *core.RevisionDiff) []string {
	c := &changeClassifier{
		old:       &changeState{r: diff.Old},
		cur:       &changeState{r: diff.New},
		changes:   make(map[int]core.ObjectChange, len(diff.Changes)),
		explained: map[int]bool{},
		fields:    map[int]bool{},
		widgets:   map[int]bool{},
		notes:     map[int]string{},
	}
	for _, ch := range diff.Changes {
		c.changes[ch.Number] = ch
	}
	var bad []string
	oldT, newT := diff.Old.Trailer(), diff.New.Trailer()
	for _, k := range []object.Name{"Root", "Encrypt", "Info"} {
		if !object.Equal(oldT.Get(k), newT.Get(k)) {
			bad = append(bad, fmt.Sprintf("the trailer's %v entry changed", k))
		}
	}
	oldCat := c.old.dict(oldT.Get("Root"))
	newCat := c.cur.dict(newT.Get("Root"))
	if oldCat == nil || newCat == nil {
		bad = append(bad, "the document catalog cannot be read in both revisions")
	} else {
		c.docMDP = docMDPLevel(c.old, oldCat)
		c.collectFieldLocks(oldCat)
		c.classify(oldCat, newCat, refTarget(newT.Get("Root")))
	}

	nums := make([]int, 0, len(c.changes))
	for n := range c.changes {
		if !c.explained[n] {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	for i, n := range nums {
		if i == maxChangeDescriptions {
			bad = append(bad, fmt.Sprintf("and %d more objects changed in ways that are not permitted", len(nums)-i))
			break
		}
		bad = append(bad, c.describe(c.changes[n]))
	}
	for _, s := range []*changeState{c.old, c.cur} {
		if s.err != nil {
			bad = append(bad, "the changes could not be read: "+s.err.Error())
			break
		}
	}
	return bad
}

// classify marks every permitted change explained.
func (c *changeClassifier) classify(oldCat, newCat *object.Dictionary, rootNum int) {
	c.fieldsAdded(oldCat, newCat)
	c.dssChanges(oldCat, newCat)
	c.structuralAdditions()

	// The catalog.
	if ch, ok := c.changes[rootNum]; ok && ch.Old != nil && ch.New != nil {
		if c.catalogEdit(oldCat, newCat) {
			c.explained[rootNum] = true
		}
	}
	// The interactive form, stored as an object of its own in both revisions.
	oldFormRef, newFormRef := refTarget(oldCat.Get("AcroForm")), refTarget(newCat.Get("AcroForm"))
	if oldFormRef != 0 && oldFormRef == newFormRef {
		if ch, ok := c.changes[oldFormRef]; ok && ch.Old != nil && ch.New != nil {
			if c.formEdit(c.old.dict(ch.Old), c.cur.dict(ch.New)) {
				c.explained[oldFormRef] = true
			}
		}
	}
	// The form's /Fields array, stored as an object of its own.
	if form := c.cur.dict(newCat.Get("AcroForm")); form != nil {
		if n := refTarget(form.Get("Fields")); n != 0 {
			if ch, ok := c.changes[n]; ok && ch.Old != nil && ch.New != nil {
				if c.appendedOnly(c.old.array(ch.Old), c.cur.array(ch.New), c.fields) {
					c.explained[n] = true
				}
			}
		}
	}
	// Pages that gained widgets, and their /Annots arrays.
	for n, ch := range c.changes {
		if c.explained[n] || ch.Old == nil || ch.New == nil {
			continue
		}
		switch ov := ch.Old.(type) {
		case *object.Dictionary:
			nv, ok := ch.New.(*object.Dictionary)
			if !ok {
				continue
			}
			if c.old.name(ov.Get("Type")) == "Page" && c.pageEdit(ov, nv) {
				c.explained[n] = true
			} else if c.signedExistingField(n, ov, nv) {
				c.explained[n] = true
			}
		case object.Array:
			if c.annotsArray(n) && c.appendedOnly(ov, c.cur.array(ch.New), c.widgets) {
				c.explained[n] = true
			}
		}
	}
}

// docMDPLevel returns the P of the certification signature the catalog's
// /Perms /DocMDP names (ISO 32000-2 12.8.2.2, Table 257): 1, 2 or 3, with 2
// the default. It is 0 when there is no certification signature. A DocMDP
// entry whose value cannot be read is taken as the most restrictive level.
func docMDPLevel(s *changeState, cat *object.Dictionary) int {
	perms := s.dict(cat.Get("Perms"))
	if perms == nil || perms.Get("DocMDP") == nil {
		return 0
	}
	sig := s.dict(perms.Get("DocMDP"))
	if sig == nil {
		return 1
	}
	for _, r := range s.array(sig.Get("Reference")) {
		ref := s.dict(r)
		if ref == nil || s.name(ref.Get("TransformMethod")) != "DocMDP" {
			continue
		}
		params := s.dict(ref.Get("TransformParams"))
		if params == nil || params.Get("P") == nil {
			return 2
		}
		switch p, _ := s.resolve(params.Get("P")).(object.Integer); p {
		case 1, 2, 3:
			return int(p)
		}
		return 1
	}
	return 1
}

// isNew reports whether num is an object the newest state added.
func (c *changeClassifier) isNew(num int) bool {
	ch, ok := c.changes[num]
	return ok && ch.Old == nil && ch.New != nil
}

// fieldsAdded finds the fields the newest state's form lists and the signed
// one's does not, and explains those that are document time-stamps, or
// signatures a DocMDP level permits, with their values and widgets.
func (c *changeClassifier) fieldsAdded(oldCat, newCat *object.Dictionary) {
	had := map[int]bool{}
	if form := c.old.dict(oldCat.Get("AcroForm")); form != nil {
		for _, f := range c.old.array(form.Get("Fields")) {
			had[refTarget(f)] = true
		}
	}
	form := c.cur.dict(newCat.Get("AcroForm"))
	if form == nil {
		return
	}
	for _, f := range c.cur.array(form.Get("Fields")) {
		n := refTarget(f)
		if n == 0 || had[n] || !c.isNew(n) {
			continue
		}
		c.addedSignatureField(n)
	}
}

// addedSignatureField explains a new top-level field if it is a signature
// field holding a new document time-stamp — or a new signature, where DocMDP
// permits signing — together with its widgets and, where allowed, their
// appearances.
func (c *changeClassifier) addedSignatureField(n int) {
	fd := c.cur.dict(c.changes[n].New)
	if fd == nil || c.cur.name(fd.Get("FT")) != "Sig" {
		return
	}
	v := refTarget(fd.Get("V"))
	if v == 0 || !c.isNew(v) {
		return
	}
	sig := c.cur.dict(c.changes[v].New)
	if sig == nil || sig.Get("ByteRange") == nil || sig.Get("Contents") == nil {
		return
	}
	var archival bool
	switch c.cur.name(sig.Get("Type")) {
	case "DocTimeStamp":
		archival = true
	case "", "Sig":
		if c.docMDP == 1 {
			c.notes[n] = "a signature added after a certification signature whose DocMDP permission level (P 1) forbids every change"
			return
		}
	default:
		return
	}
	widgets := []int{}
	if c.cur.name(fd.Get("Subtype")) == "Widget" {
		widgets = append(widgets, n)
	}
	for _, k := range c.cur.array(fd.Get("Kids")) {
		kn := refTarget(k)
		kd := c.cur.dict(k)
		if kn == 0 || !c.isNew(kn) || kd == nil || c.cur.name(kd.Get("Subtype")) != "Widget" {
			return
		}
		widgets = append(widgets, kn)
	}
	for _, w := range widgets {
		wd := c.cur.dict(c.changes[w].New)
		if ap := wd.Get("AP"); ap != nil {
			// A document time-stamp is not meant to be seen: its widget may
			// carry an appearance only when it has no area to draw it in. A
			// signature's appearance is part of signing.
			if archival && !zeroArea(c.cur, wd.Get("Rect")) {
				c.notes[w] = "a document time-stamp widget with a visible appearance"
				return
			}
			c.explainNewSubgraph(ap, map[int]bool{})
		}
	}
	c.explained[n], c.explained[v] = true, true
	c.fields[n] = true
	for _, w := range widgets {
		c.explained[w], c.widgets[w] = true, true
	}
}

// explainNewSubgraph explains every new object reachable from o through new
// objects only: an appearance stream and the resources it alone uses. It stops
// at any object the signed revision already had.
func (c *changeClassifier) explainNewSubgraph(o object.Object, seen map[int]bool) {
	var walk func(o object.Object, depth int)
	walk = func(o object.Object, depth int) {
		if depth > 64 {
			return
		}
		switch v := o.(type) {
		case object.IndirectRef:
			if seen[v.Number] || !c.isNew(v.Number) {
				return
			}
			seen[v.Number] = true
			c.explained[v.Number] = true
			walk(c.changes[v.Number].New, depth+1)
		case object.Array:
			for _, e := range v {
				walk(e, depth+1)
			}
		case *object.Dictionary:
			for _, e := range v.All() {
				walk(e, depth+1)
			}
		case *object.Stream:
			for _, e := range v.Dict.All() {
				walk(e, depth+1)
			}
		}
	}
	walk(o, 0)
}

// zeroArea reports whether a /Rect has no width or no height.
func zeroArea(s *changeState, rect object.Object) bool {
	r := s.array(rect)
	if len(r) != 4 {
		return false
	}
	var v [4]float64
	for i, e := range r {
		switch n := s.resolve(e).(type) {
		case object.Integer:
			v[i] = float64(n)
		case object.Real:
			v[i] = float64(n)
		default:
			return false
		}
	}
	return v[0] == v[2] || v[1] == v[3]
}

// dssObjects returns the objects making up a catalog's Document Security Store
// (ISO 32000-2 12.8.4.3): the containers — the DSS dictionary, its arrays, the
// VRI dictionary and its entries — and the leaves they list (certificate, CRL,
// OCSP and time-stamp streams). Only the keys the DSS defines are followed.
func dssObjects(s *changeState, cat *object.Dictionary) (containers, leaves map[int]bool) {
	containers, leaves = map[int]bool{}, map[int]bool{}
	add := func(set map[int]bool, o object.Object) {
		if n := refTarget(o); n != 0 {
			set[n] = true
		}
	}
	list := func(d *object.Dictionary, key object.Name) {
		v := d.Get(key)
		add(containers, v)
		for _, e := range s.array(v) {
			add(leaves, e)
		}
	}
	v := cat.Get("DSS")
	add(containers, v)
	dss := s.dict(v)
	if dss == nil {
		return
	}
	for _, k := range []object.Name{"Certs", "OCSPs", "CRLs"} {
		list(dss, k)
	}
	vv := dss.Get("VRI")
	add(containers, vv)
	vri := s.dict(vv)
	if vri == nil {
		return
	}
	for _, e := range vri.All() {
		add(containers, e)
		entry := s.dict(e)
		if entry == nil {
			continue
		}
		for _, k := range []object.Name{"Cert", "OCSP", "CRL"} {
			list(entry, k)
		}
		add(leaves, entry.Get("TS"))
	}
	return
}

// dssChanges explains the new Document Security Store material, and changes to
// DSS containers that were already DSS containers when signed.
func (c *changeClassifier) dssChanges(oldCat, newCat *object.Dictionary) {
	newC, newL := dssObjects(c.cur, newCat)
	oldC, _ := dssObjects(c.old, oldCat)
	for n := range newL {
		if c.isNew(n) {
			c.explained[n] = true
		}
	}
	for n := range newC {
		ch, ok := c.changes[n]
		if !ok {
			continue
		}
		if ch.Old == nil || oldC[n] {
			c.explained[n] = true
		}
	}
}

// structuralAdditions explains new cross-reference streams and object
// streams: file structure, not document content. Their contents are compared
// object by object.
func (c *changeClassifier) structuralAdditions() {
	for n, ch := range c.changes {
		if ch.Old != nil {
			continue
		}
		if st, ok := ch.New.(*object.Stream); ok {
			if t, _ := st.Dict.Get("Type").(object.Name); t == "XRef" || t == "ObjStm" {
				c.explained[n] = true
			}
		}
	}
}

// catalogEdit reports whether the catalog changed only in /DSS and in an
// /AcroForm edit formEdit permits. A new form object that replaces a form
// stored in the catalog itself is explained with it.
func (c *changeClassifier) catalogEdit(oldCat, newCat *object.Dictionary) bool {
	for _, k := range unionKeys(oldCat, newCat) {
		ov, nv := oldCat.Get(k), newCat.Get(k)
		if object.Equal(ov, nv) || k == "DSS" {
			continue
		}
		if k != "AcroForm" {
			return false
		}
		newForm := c.cur.dict(nv)
		if newForm == nil {
			return false
		}
		oldForm := c.old.dict(ov)
		if ov == nil {
			oldForm = &object.Dictionary{}
		}
		if oldForm == nil || !c.formEdit(oldForm, newForm) {
			return false
		}
		if n := refTarget(nv); n != 0 && c.isNew(n) {
			c.explained[n] = true
		}
	}
	return true
}

// formEdit reports whether an interactive form changed only by appending
// permitted fields to /Fields and setting signature bits in /SigFlags (ISO
// 32000-2 Table 225: bit 1 SignaturesExist, bit 2 AppendOnly).
func (c *changeClassifier) formEdit(oldForm, newForm *object.Dictionary) bool {
	if oldForm == nil || newForm == nil {
		return false
	}
	for _, k := range unionKeys(oldForm, newForm) {
		ov, nv := oldForm.Get(k), newForm.Get(k)
		if object.Equal(ov, nv) {
			continue
		}
		switch k {
		case "Fields":
			// An indirect array that kept its number is judged as an object
			// of its own; here the value itself changed.
			if !c.appendedOnly(c.old.array(ov), c.cur.array(nv), c.fields) {
				return false
			}
			if n := refTarget(nv); n != 0 && c.isNew(n) {
				c.explained[n] = true
			}
		case "SigFlags":
			o, _ := c.old.resolve(ov).(object.Integer)
			n, ok := c.cur.resolve(nv).(object.Integer)
			if !ok || n&o != o || n&^o&^3 != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// pageEdit reports whether a page changed only by appending permitted widgets
// to its /Annots.
func (c *changeClassifier) pageEdit(oldPage, newPage *object.Dictionary) bool {
	for _, k := range unionKeys(oldPage, newPage) {
		ov, nv := oldPage.Get(k), newPage.Get(k)
		if object.Equal(ov, nv) {
			continue
		}
		if k != "Annots" || !c.appendedOnly(c.old.array(ov), c.cur.array(nv), c.widgets) {
			return false
		}
		if n := refTarget(nv); n != 0 && c.isNew(n) {
			c.explained[n] = true
		}
	}
	return true
}

// annotsArray reports whether object n is the /Annots array of a page one of
// the permitted widgets names as its /P.
func (c *changeClassifier) annotsArray(n int) bool {
	for w := range c.widgets {
		wd := c.cur.dict(c.changes[w].New)
		if wd == nil {
			continue
		}
		if page := c.cur.dict(wd.Get("P")); page != nil && refTarget(page.Get("Annots")) == n {
			return true
		}
	}
	return false
}

// appendedOnly reports whether after is before with entries appended, each a
// reference to an object in allowed.
func (c *changeClassifier) appendedOnly(before, after object.Array, allowed map[int]bool) bool {
	if len(after) < len(before) {
		return false
	}
	for i := range before {
		if !object.Equal(before[i], after[i]) {
			return false
		}
	}
	for _, e := range after[len(before):] {
		if n := refTarget(e); n == 0 || !allowed[n] {
			return false
		}
	}
	return true
}

// signedExistingField reports whether object n is a signature field the
// signed revision left without a value, now given a new signature — signing,
// which only a DocMDP P 1 certification forbids — changing nothing but its /V
// and appearance, in a field no earlier signature's FieldMDP transform locked.
func (c *changeClassifier) signedExistingField(n int, oldField, newField *object.Dictionary) bool {
	if c.old.name(oldField.Get("FT")) != "Sig" || oldField.Get("V") != nil {
		return false
	}
	if c.docMDP == 1 {
		c.notes[n] = "a signature added after a certification signature whose DocMDP permission level (P 1) forbids every change"
		return false
	}
	if name := c.names[n]; name != "" && c.locked(name) {
		c.notes[n] = "field " + name + " is locked by an earlier signature's FieldMDP transform"
		return false
	}
	v := refTarget(newField.Get("V"))
	if v == 0 || !c.isNew(v) {
		return false
	}
	sig := c.cur.dict(c.changes[v].New)
	if sig == nil || sig.Get("ByteRange") == nil || sig.Get("Contents") == nil {
		return false
	}
	if t := c.cur.name(sig.Get("Type")); t != "" && t != "Sig" {
		return false
	}
	for _, k := range unionKeys(oldField, newField) {
		if object.Equal(oldField.Get(k), newField.Get(k)) {
			continue
		}
		if k != "V" && k != "AP" {
			return false
		}
	}
	c.explained[v] = true
	c.explainNewSubgraph(newField.Get("AP"), map[int]bool{})
	return true
}

// unionKeys returns the keys of a and b, sorted.
func unionKeys(a, b *object.Dictionary) []object.Name {
	seen := map[object.Name]bool{}
	var out []object.Name
	for _, d := range []*object.Dictionary{a, b} {
		for k := range d.Keys() {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// describe says what a change not permitted was.
func (c *changeClassifier) describe(ch core.ObjectChange) string {
	what := fmt.Sprintf("object %d", ch.Number)
	var desc string
	switch {
	case ch.Old == nil:
		desc = fmt.Sprintf("%s (%s) was added and is not signature, time-stamp or DSS material", what, kindOf(c.cur, ch.New))
	case ch.New == nil:
		desc = fmt.Sprintf("%s (%s) was deleted", what, kindOf(c.old, ch.Old))
	default:
		desc = fmt.Sprintf("%s (%s) was changed", what, kindOf(c.old, ch.Old))
		if keys := changedKeys(ch.Old, ch.New); keys != "" {
			desc += ": " + keys
		}
	}
	if note := c.notes[ch.Number]; note != "" {
		desc += " — " + note
	}
	return desc
}

// kindOf names what an object is, for a description.
func kindOf(s *changeState, o object.Object) string {
	var d *object.Dictionary
	switch v := o.(type) {
	case *object.Dictionary:
		d = v
	case *object.Stream:
		d = &v.Dict
		if t, _ := d.Get("Type").(object.Name); t != "" {
			return string(t) + " stream"
		}
		if st, _ := d.Get("Subtype").(object.Name); st != "" {
			return string(st) + " stream"
		}
		return "stream"
	case object.Array:
		return "array"
	case nil:
		return "nothing"
	default:
		return fmt.Sprintf("%T", o)
	}
	if t, _ := d.Get("Type").(object.Name); t != "" {
		return string(t) + " dictionary"
	}
	if ft, _ := d.Get("FT").(object.Name); ft != "" {
		return string(ft) + " field"
	}
	return "dictionary"
}

// changedKeys lists the dictionary keys whose values differ, and whether a
// stream's data changed.
func changedKeys(a, b object.Object) string {
	da, sa := dictOf(a)
	db, sb := dictOf(b)
	if da == nil || db == nil {
		return ""
	}
	var keys []string
	for _, k := range unionKeys(da, db) {
		if !object.Equal(da.Get(k), db.Get(k)) {
			keys = append(keys, k.String())
		}
	}
	if sa != nil && sb != nil && string(sa.Data) != string(sb.Data) {
		keys = append(keys, "stream data")
	}
	return strings.Join(keys, ", ")
}

func dictOf(o object.Object) (*object.Dictionary, *object.Stream) {
	switch v := o.(type) {
	case *object.Dictionary:
		return v, nil
	case *object.Stream:
		return &v.Dict, v
	}
	return nil, nil
}

// fieldLock is one FieldMDP transform (ISO 32000-2 12.8.2.4, Table 258): the
// fields a signature locked when it was applied.
type fieldLock struct {
	action object.Name // All, Include or Exclude
	fields map[string]bool
}

// locked reports whether any FieldMDP transform of the signed revision locks
// the field with this qualified name.
func (c *changeClassifier) locked(name string) bool {
	for _, l := range c.locks {
		switch l.action {
		case "All":
			return true
		case "Include":
			if l.fields[name] {
				return true
			}
		case "Exclude":
			if !l.fields[name] {
				return true
			}
		default:
			return true // an action this reader does not know locks everything
		}
	}
	return false
}

// collectFieldLocks walks the signed revision's form, recording each field's
// qualified name and the FieldMDP transforms of the signatures its fields
// hold. The walk is bounded like the verifier's field walks.
func (c *changeClassifier) collectFieldLocks(cat *object.Dictionary) {
	c.names = map[int]string{}
	form := c.old.dict(cat.Get("AcroForm"))
	if form == nil {
		return
	}
	seen := map[int]bool{}
	var walk func(node object.Object, prefix string, depth int)
	walk = func(node object.Object, prefix string, depth int) {
		if depth > MaxFieldTreeDepth {
			return
		}
		n := refTarget(node)
		if n != 0 {
			if seen[n] {
				return
			}
			seen[n] = true
		}
		fd := c.old.dict(node)
		if fd == nil {
			return
		}
		part := ""
		if t, ok := c.old.resolve(fd.Get("T")).(object.String); ok {
			part = core.DecodePDFTextString(t.Value)
		}
		name := JoinFieldName(prefix, part)
		if n != 0 {
			c.names[n] = name
		}
		if sig := c.old.dict(fd.Get("V")); sig != nil && sig.Get("ByteRange") != nil {
			for _, r := range c.old.array(sig.Get("Reference")) {
				ref := c.old.dict(r)
				if ref == nil || c.old.name(ref.Get("TransformMethod")) != "FieldMDP" {
					continue
				}
				params := c.old.dict(ref.Get("TransformParams"))
				l := fieldLock{fields: map[string]bool{}}
				if params != nil {
					l.action = c.old.name(params.Get("Action"))
					for _, f := range c.old.array(params.Get("Fields")) {
						if s, ok := c.old.resolve(f).(object.String); ok {
							l.fields[core.DecodePDFTextString(s.Value)] = true
						}
					}
				}
				c.locks = append(c.locks, l)
			}
		}
		for _, k := range c.old.array(fd.Get("Kids")) {
			walk(k, name, depth+1)
		}
	}
	for _, f := range c.old.array(form.Get("Fields")) {
		walk(f, "", 0)
	}
}
