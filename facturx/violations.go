package facturx

// The finding types satisfy pdf0.Violation structurally: the interface is
// declared where it is consumed, so this package names nothing from root.

// RuleID returns the Factur-X container rule identifier, or the identifier the
// invoice rule engine minted for an adopted finding. It is unique only within
// FacturXViolation.Source, which names the authority.
func (v FacturXViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v FacturXViolation) ObjectNum() int { return v.Object }

// RuleID returns the Order-X container rule identifier, or the identifier the
// order rule engine minted for an adopted finding.
func (v OrderXViolation) RuleID() string { return v.Rule }

// ObjectNum returns the anchoring object number, 0 if N/A.
func (v OrderXViolation) ObjectNum() int { return v.Object }
