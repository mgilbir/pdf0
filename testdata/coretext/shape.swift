// Shapes a line of text with CoreText and prints the glyphs, so that a question
// this repository cannot answer on Linux can be answered on a Mac.
//
// The question is one string, in thirty-seven spellings: a character nothing is
// drawn for, written inside a syllable. This package removes it before shaping,
// so the conjunct forms; HarfBuzz keeps it, so the syllable breaks and the
// orphaned virama gets a dotted circle. Unicode permits both — see
// deliberateDifferences in fonts/harfbuzz_test.go — so what is worth knowing is
// whether anything else agrees with this package or whether it stands alone.
//
//	swiftc -O -o shape shape.swift
//	./shape ../../fonts/notosans/NotoSans-Variable.ttf < ignorables.txt
//
// It prints one line per input line: the glyph ids and advances, then DOTTED if
// U+25CC is among them. A syllable that survived the ignorable is one glyph and
// no dotted circle; one that broke is several, with it.
import CoreText
import Foundation

let args = CommandLine.arguments
guard args.count == 2 else {
    FileHandle.standardError.write("usage: shape <font.ttf> < lines\n".data(using: .utf8)!)
    exit(2)
}
guard let fontData = FileManager.default.contents(atPath: args[1]) else {
    FileHandle.standardError.write("cannot read \(args[1])\n".data(using: .utf8)!)
    exit(1)
}
guard let base = CTFontManagerCreateFontDescriptorFromData(fontData as CFData) else {
    FileHandle.standardError.write("not a font CoreText will take\n".data(using: .utf8)!)
    exit(1)
}
// An empty cascade list, so that a glyph this font lacks is a notdef rather than
// a glyph from some other font — which would make the ids meaningless, since
// they are only comparable against the file they came from.
let desc = CTFontDescriptorCreateCopyWithAttributes(
    base, [kCTFontCascadeListAttribute: [] as CFArray] as CFDictionary)

// The em as the point size, so advances come out in font units and can be
// compared with what shape.py writes for HarfBuzz.
let probe = CTFontCreateWithFontDescriptor(desc, 0, nil)
let upem = CGFloat(CTFontGetUnitsPerEm(probe))
let font = CTFontCreateWithFontDescriptor(desc, upem, nil)
let wanted = CTFontCopyPostScriptName(font) as String

var circle: CGGlyph = 0
var circleChars = Array("\u{25CC}".utf16)
CTFontGetGlyphsForCharacters(font, &circleChars, &circle, 1)

while let line = readLine(strippingNewline: true) {
    if line.isEmpty { print(""); continue }
    let attributed = CFAttributedStringCreateMutable(kCFAllocatorDefault, 0)!
    CFAttributedStringReplaceString(attributed, CFRangeMake(0, 0), line as CFString)
    CFAttributedStringSetAttribute(
        attributed, CFRangeMake(0, CFStringGetLength(line as CFString)),
        kCTFontAttributeName, font)
    let ctLine = CTLineCreateWithAttributedString(attributed)
    var fields: [String] = []
    var substituted = false
    var sawCircle = false
    for run in (CTLineGetGlyphRuns(ctLine) as! [CTRun]) {
        if let attrs = CTRunGetAttributes(run) as? [CFString: Any],
           let runFont = attrs[kCTFontAttributeName] {
            let name = CTFontCopyPostScriptName(runFont as! CTFont) as String
            if name != wanted { substituted = true }
        }
        let n = CTRunGetGlyphCount(run)
        if n == 0 { continue }
        var glyphs = [CGGlyph](repeating: 0, count: n)
        var advances = [CGSize](repeating: .zero, count: n)
        CTRunGetGlyphs(run, CFRangeMake(0, 0), &glyphs)
        CTRunGetAdvances(run, CFRangeMake(0, 0), &advances)
        for i in 0..<n {
            if glyphs[i] == circle && circle != 0 { sawCircle = true }
            fields.append("\(glyphs[i]),\(Int(advances[i].width.rounded()))")
        }
    }
    print(fields.joined(separator: " ")
          + (sawCircle ? "  DOTTED" : "")
          + (substituted ? "  SUBSTITUTED-FONT" : ""))
}
