package pdfa

import "github.com/mgilbir/pdf0/object"

// TriggerEventForbidden reports whether a PDF/A document at level may not carry
// the trigger event of an additional-actions (/AA) dictionary on its catalog, a
// page, an annotation or a form field. ok is false for a level this package
// does not define.
//
// It is the classification the validator applies (checkNoCatalogAA,
// checkAnnotationAA, checkA4TriggerEvents), stated once for the code that
// repairs what the validator reports: before part 4 an /AA is forbidden
// outright on all of them, so every event is; at part 4 the rule is per event
// (ISO 19005-4 6.6.3), and only the document lifecycle, page navigation and
// page-triggered annotation events in forbiddenAAEvents are. The rule is the
// part's, whatever the conformance level or variant, so every level of a part
// answers alike, 2u and 3u included.
func TriggerEventForbidden(level Level, event object.Name) (forbidden, ok bool) {
	if !level.Valid() {
		return false, false
	}
	if level.Part() == 4 {
		return forbiddenAAEvents[event], true
	}
	return true, true
}
