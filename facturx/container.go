package facturx

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/mgilbir/pdf0/internal/core"
	"github.com/mgilbir/pdf0/internal/finding"
	"github.com/mgilbir/pdf0/object"
)

// The container half both validators share: finding the embedded document,
// reading it, and reading the metadata that identifies it. Factur-X and
// Order-X used to carry a copy each, and the copies had drifted — Order-X did
// not check fx:Version — which is the asymmetry this file removes (audit
// C148).

// attachment is one embedded file of a family, as listed in /AF.
type attachment struct {
	fs    *object.Dictionary
	name  string
	num   int  // object number of the file specification, 0 when direct
	exact bool // the name is spelled exactly as the specification spells it
}

// familyAttachments returns the family's file specifications listed in the
// catalog /AF, in /AF order, and how many distinct ones the document carries
// across /AF and the EmbeddedFiles name tree together.
//
// A name matches ignoring case, so an attachment called Factur-X.xml is still
// found and checked — and then reported for its spelling — rather than being
// reported absent, which would hide everything else wrong with it.
func familyAttachments(doc core.View, cat *object.Dictionary, f family) (listed []attachment, distinct int) {
	seen := map[*object.Dictionary]bool{}
	if af, ok := doc.Resolve(cat.Get("AF")).(object.Array); ok {
		for _, e := range af {
			fs := doc.ResolveDict(e)
			if fs == nil {
				continue
			}
			name := fileSpecName(doc, fs)
			ours, exact := f.matchName(name)
			if !ours {
				continue
			}
			if !seen[fs] {
				seen[fs] = true
				listed = append(listed, attachment{fs: fs, name: name, num: object.RefNum(e), exact: exact})
			}
		}
	}
	if names := doc.ResolveDict(cat.Get("Names")); names != nil {
		entries, _ := doc.NameTreeEntries(names.Get("EmbeddedFiles"))
		for _, e := range entries {
			fs := doc.ResolveDict(e.Value)
			if fs == nil || seen[fs] {
				continue
			}
			byKey, _ := f.matchName(core.DecodePDFTextString(e.Key))
			byName, _ := f.matchName(fileSpecName(doc, fs))
			if byKey || byName {
				seen[fs] = true
			}
		}
	}
	return listed, len(seen)
}

// designated picks the attachment the /AF relationship designates as the
// document's structured data: the first whose /AFRelationship is one the
// specification allows, or the first listed when none is.
func designated(doc core.View, listed []attachment) attachment {
	for _, a := range listed {
		if rel, ok := doc.ResolveName(a.fs.Get("AFRelationship")); ok && facturxRelationships[rel] {
			return a
		}
	}
	return listed[0]
}

// checkAttachment finds the family's embedded XML, reports what is wrong with
// how it is attached, and returns its name, its decoded bytes and the object
// its findings anchor to. The bytes are nil whenever there is nothing the
// rule engine can be given, and in every such case something has been
// reported: a finding when the file is absent, empty or corrupt, a limit trip
// when pdf0 declined to decode it. An attachment that is present and yields no
// XML used to be skipped without a word, so the container validated clean
// (audit C43).
func checkAttachment(doc core.View, cat *object.Dictionary, f family, what, missing, xmlRule string, add func(rule, msg string, obj int)) (name string, data []byte, num int) {
	listed, distinct := familyAttachments(doc, cat, f)
	if len(listed) == 0 {
		add("attachment", missing, 0)
		if distinct > 0 {
			add("attachment", fmt.Sprintf("the %s XML is in the EmbeddedFiles name tree but not associated with the document through the catalog /AF", what), 0)
		}
		return "", nil, 0
	}
	a := designated(doc, listed)
	name, num = a.name, a.num
	if distinct > 1 {
		add("attachment", fmt.Sprintf("the document carries %d %s XML attachments; a %s container carries exactly one (the one checked is %q, object %d)", distinct, what, f.name, a.name, a.num), 0)
	}
	if !a.exact {
		add("attachment", fmt.Sprintf("the %s XML attachment is named %q; the %s specifications spell the name exactly (%v)", what, a.name, f.name, f.fileNames), num)
	}
	if rel, ok := doc.ResolveName(a.fs.Get("AFRelationship")); !ok || !facturxRelationships[rel] {
		add("attachment", fmt.Sprintf("the %s XML /AFRelationship shall be /Data, /Alternative or /Source", what), num)
	}
	ef := doc.ResolveDict(a.fs.Get("EF"))
	if ef == nil {
		add("attachment", fmt.Sprintf("the %s file specification has no /EF entry", what), num)
		return name, nil, num
	}
	st, ok := doc.Resolve(ef.Get("F")).(*object.Stream)
	if !ok {
		add("attachment", fmt.Sprintf("the %s file specification has no embedded file stream (/EF /F)", what), num)
		return name, nil, num
	}
	if sub, _ := doc.ResolveName(st.Dict.Get("Subtype")); !facturxIsXMLSubtype(sub) {
		add("attachment", fmt.Sprintf("the %s embedded-file /Subtype should be text/xml, got %s", what, sub), num)
	}
	stNum := object.RefNum(ef.Get("F"))
	decoded, err := core.DecodeStreamData(doc.Cancel, st, doc.Limits)
	switch {
	case err != nil && doc.Cancel.Stopped():
		// The run is over; the cancellation finding says so.
		return name, nil, num
	case errors.Is(err, core.ErrDecodeLimit):
		doc.Note(core.GuardDecodedStream, fmt.Sprintf("the embedded %s XML decodes to more than the per-stream limit, so it was not validated", what), stNum)
		return name, nil, num
	case errors.Is(err, core.ErrUnsupportedFilter):
		doc.Note(core.GuardUnsupportedFilter, fmt.Sprintf("the embedded %s XML is encoded with a filter pdf0 does not implement (%v), so it was not validated", what, err), stNum)
		return name, nil, num
	case err != nil:
		add(xmlRule, fmt.Sprintf("the embedded %s XML could not be decoded: %v", what, err), num)
		return name, nil, num
	case len(bytes.TrimSpace(decoded)) == 0:
		add(xmlRule, fmt.Sprintf("the embedded %s XML file is empty", what), num)
		return name, nil, num
	}
	return name, decoded, num
}

// checkCommonMetadata reports the metadata findings Factur-X and Order-X share:
// a readable packet, the family's namespace, fx:DocumentFileName agreeing with
// the attachment, and fx:Version. It reports whether the family-specific
// property checks should run.
func checkCommonMetadata(m containerMetadata, f, other family, attachedName string, add func(rule, msg string, obj int)) bool {
	if !reportMetadataReadability(m, f, other, add) {
		return false
	}
	if m.fileName == "" {
		add("metadata", "missing XMP fx:DocumentFileName", 0)
	} else if attachedName != "" && m.fileName != attachedName {
		add("metadata", fmt.Sprintf("XMP fx:DocumentFileName %q does not match the embedded file name %q", m.fileName, attachedName), 0)
	}
	if m.version == "" {
		add("metadata", "missing XMP fx:Version", 0)
	}
	return true
}

// flushTrips reports the container run's own guard trips. The PDF/A-3 half is
// a separate run whose trips arrive with its findings; these are the ones the
// container checks noted — an XMP packet over the limit, an attachment pdf0
// declined to decode — and dropping them would leave a container that was not
// fully checked looking as if it had been.
func flushTrips(doc core.View, add func(rule, msg string, obj int)) {
	if doc.Run == nil || doc.Run.Trips == nil {
		return
	}
	for _, t := range doc.Run.Trips.Snapshot() {
		add(finding.LimitRule, t.Message(), t.Obj)
	}
}

// fileSpecName returns a file specification's name, preferring the Unicode
// /UF entry (decoded from its UTF-16 or PDFDoc encoding) over /F.
func fileSpecName(doc core.View, fs *object.Dictionary) string {
	for _, key := range []object.Name{"UF", "F"} {
		if s, ok := doc.Resolve(fs.Get(key)).(object.String); ok {
			if name := core.DecodePDFTextString(s.Value); name != "" {
				return name
			}
		}
	}
	return ""
}
