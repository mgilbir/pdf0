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
// page-triggered annotation events in forbiddenAAEvents are.
func TriggerEventForbidden(level Level, event object.Name) (forbidden, ok bool) {
	switch level {
	case PDFA1b, PDFA2b, PDFA3b, PDFA1a, PDFA2a, PDFA3a:
		return true, true
	case PDFA4, PDFA4E, PDFA4F:
		return forbiddenAAEvents[event], true
	}
	return false, false
}
