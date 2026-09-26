package pdf0

// The answer every validator gives a nil *Document.
//
// A nil document is a caller mistake, and the useful answer is a finding
// rather than a panic: a validator is what a caller reaches for after a failed
// Read, where the natural shape of the code leaves doc nil and the error
// unchecked. Every validator answers it the same way, before it touches the
// document — one finding under the reserved rule "limit", which
// IsCheckerFinding reports as a checker finding, so the result can never be
// mistaken for a clean bill of health. ValidatePDFA always did; the other
// nine dereferenced the document first and panicked, outside their recover
// boundary (audit 2026-09-22 C144). TestEveryValidatorAnswersANilDocument
// enumerates the package's validators from its source, so a new one cannot
// forget.
const nilDocumentMessage = "no document to validate"
