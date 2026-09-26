package core

// predefinedCodespaces is the codespace of every CMap in PredefinedCMaps.
//
// A predefined CMap's code-to-CID data is megabytes this module does not carry
// (LoadCMap reports it as ReasonUnsupported), but its codespace is a line or two, and the
// codespace alone is what cuts a string into codes. That is all text
// extraction needs from it: the codes are then looked up in the font's
// ToUnicode map, or — for the Uni* CMaps, whose codes are UCS-2 or UTF-16 —
// read as Unicode directly.
//
// Transcribed from Adobe's published CMaps (github.com/adobe-type-tools/
// cmap-resources, as poppler-data 0.4.12 ships them in /usr/share/poppler/cMap),
// following usecmap to the CMap that declares the ranges.
// TestPredefinedCodespacesMatchAdobe checks the table against those files when
// they are installed.
var predefinedCodespaces = func() map[string][]codespaceRange {
	m := map[string][]codespaceRange{}
	for _, name := range []string{"GB-EUC-H", "GB-EUC-V", "KSC-EUC-H", "KSC-EUC-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0xA1A1, hi: 0xFEFE}}
	}
	for _, name := range []string{"GBpc-EUC-H", "GBpc-EUC-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0xA1A1, hi: 0xFCFE}, {bytes: 1, lo: 0xFD, hi: 0xFF}}
	}
	for _, name := range []string{"GBK-EUC-H", "GBK-EUC-V", "GBKp-EUC-H", "GBKp-EUC-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8140, hi: 0xFEFE}}
	}
	for _, name := range []string{"GBK2K-H", "GBK2K-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x7F}, {bytes: 4, lo: 0x81308130, hi: 0xFE39FE39}, {bytes: 2, lo: 0x8140, hi: 0xFEFE}}
	}
	for _, name := range []string{"UniGB-UCS2-H", "UniGB-UCS2-V", "UniCNS-UCS2-H", "UniCNS-UCS2-V", "UniJIS-UCS2-H", "UniJIS-UCS2-V", "UniJIS-UCS2-HW-H", "UniJIS-UCS2-HW-V", "UniKS-UCS2-H", "UniKS-UCS2-V"} {
		m[name] = []codespaceRange{{bytes: 2, lo: 0x0000, hi: 0xD7FF}, {bytes: 2, lo: 0xE000, hi: 0xFFFF}}
	}
	for _, name := range []string{"UniGB-UTF16-H", "UniGB-UTF16-V", "UniCNS-UTF16-H", "UniCNS-UTF16-V", "UniJIS-UTF16-H", "UniJIS-UTF16-V", "UniKS-UTF16-H", "UniKS-UTF16-V"} {
		m[name] = []codespaceRange{{bytes: 2, lo: 0x0000, hi: 0xD7FF}, {bytes: 4, lo: 0xD800DC00, hi: 0xDBFFDFFF}, {bytes: 2, lo: 0xE000, hi: 0xFFFF}}
	}
	for _, name := range []string{"B5pc-H", "B5pc-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0xA140, hi: 0xFCFE}, {bytes: 1, lo: 0xFD, hi: 0xFF}}
	}
	for _, name := range []string{"HKscs-B5-H", "HKscs-B5-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8740, hi: 0xFEFE}}
	}
	for _, name := range []string{"ETen-B5-H", "ETen-B5-V", "ETenms-B5-H", "ETenms-B5-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0xA140, hi: 0xFEFE}}
	}
	for _, name := range []string{"CNS-EUC-H", "CNS-EUC-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 4, lo: 0x8EA1A1A1, hi: 0x8EA1FEFE}, {bytes: 4, lo: 0x8EA2A1A1, hi: 0x8EA2FEFE}, {bytes: 4, lo: 0x8EA3A1A1, hi: 0x8EA3FEFE}, {bytes: 2, lo: 0xA1A1, hi: 0xFEFE}}
	}
	for _, name := range []string{"83pv-RKSJ-H", "90pv-RKSJ-H"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8140, hi: 0x9FFC}, {bytes: 1, lo: 0xA0, hi: 0xDF}, {bytes: 2, lo: 0xE040, hi: 0xFCFC}, {bytes: 1, lo: 0xFD, hi: 0xFF}}
	}
	for _, name := range []string{"90ms-RKSJ-H", "90ms-RKSJ-V", "90msp-RKSJ-H", "90msp-RKSJ-V", "Add-RKSJ-H", "Add-RKSJ-V", "Ext-RKSJ-H", "Ext-RKSJ-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8140, hi: 0x9FFC}, {bytes: 1, lo: 0xA0, hi: 0xDF}, {bytes: 2, lo: 0xE040, hi: 0xFCFC}}
	}
	for _, name := range []string{"EUC-H", "EUC-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8EA0, hi: 0x8EDF}, {bytes: 2, lo: 0xA1A1, hi: 0xFEFE}}
	}
	for _, name := range []string{"H", "V"} {
		m[name] = []codespaceRange{{bytes: 2, lo: 0x2121, hi: 0x7E7E}}
	}
	for _, name := range []string{"KSCms-UHC-H", "KSCms-UHC-V", "KSCms-UHC-HW-H", "KSCms-UHC-HW-V"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x80}, {bytes: 2, lo: 0x8141, hi: 0xFEFE}}
	}
	for _, name := range []string{"KSCpc-EUC-H"} {
		m[name] = []codespaceRange{{bytes: 1, lo: 0x00, hi: 0x84}, {bytes: 2, lo: 0xA141, hi: 0xFDFE}, {bytes: 1, lo: 0xFE, hi: 0xFF}}
	}
	for _, name := range []string{"Identity-H", "Identity-V"} {
		m[name] = []codespaceRange{{bytes: 2, lo: 0x0000, hi: 0xFFFF}}
	}
	return m
}()
