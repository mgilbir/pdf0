package simplefont

import "github.com/mgilbir/forme/font"

// forme's encoding tables, as the forme version in go.mod exposes them.
func formeStandard() map[byte]string { return font.StandardEncodingNames }
func formeMacRoman() map[byte]string { return font.MacRomanEncodingNames }
func formeWinAnsi() map[byte]string  { return font.WinAnsiEncodingNames }
