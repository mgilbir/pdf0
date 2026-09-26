package simplefont

import "github.com/mgilbir/forme/font"

// forme's encoding tables. forme hands out a copy of each (its maps were
// exported once, and a map cannot be made read-only); tables reads each copy
// once.
func formeStandard() map[byte]string { return font.StandardEncodingNames() }
func formeMacRoman() map[byte]string { return font.MacRomanEncodingNames() }
func formeWinAnsi() map[byte]string  { return font.WinAnsiEncodingNames() }
