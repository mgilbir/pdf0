package core

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"github.com/mgilbir/pdf0/object"
	"github.com/mgilbir/pdf0/syntax"
)

// This file implements the general-purpose stream filters of ISO 32000-2 7.4 —
// FlateDecode, LZWDecode, ASCIIHexDecode, ASCII85Decode, RunLengthDecode and the
// /DecodeParms predictors shared by LZW and Flate (7.4.4.4), TIFF horizontal
// differencing and the PNG per-row filters — and the /Filter dispatch over them
// (FilterChain, DecodeStreamData). The image codecs live in the images package.
//
// Every entry point here consumes attacker-controlled bytes: output is capped at
// Limits.DecodedStreamBytes against decompression bombs, and predictor parameters are
// range-checked before any row arithmetic, since Colors, Columns and
// BitsPerComponent come straight from the file.

// LZW special codes (ISO 32000-1 7.4.4.2).
const (
	lzwClearTable = 256
	lzwEOD        = 257
	lzwFirstCode  = 258
)

// LZWDecode reverses the PDF LZWDecode filter. Codes are variable width, 9 to
// 12 bits, MSB-first; earlyChange (default 1, the PDF default) bumps the code
// width one entry early, matching the TIFF-style encoders PDF producers use.
//
// Output is capped (see WithMaxDecodedStreamBytes) to bound memory on hostile
// input.
func LZWDecode(cancel Canceler, data []byte, earlyChange int, lim Limits) ([]byte, error) {
	if earlyChange != 0 && earlyChange != 1 {
		return nil, fmt.Errorf("LZW: invalid EarlyChange %d", earlyChange)
	}

	// table maps a code to its byte string. The first 256 entries are the
	// literals; 256/257 are Clear/EOD; entries grow from 258.
	var table [][]byte
	reset := func() {
		table = make([][]byte, lzwFirstCode, 4096)
		for i := 0; i < 256; i++ {
			table[i] = []byte{byte(i)}
		}
	}
	reset()

	var out []byte
	var prev []byte
	codeWidth := 9

	var bitBuf uint32
	var bitCnt int
	pos := 0
	nextCode := func() (int, bool) {
		for bitCnt < codeWidth {
			if pos >= len(data) {
				return 0, false
			}
			bitBuf = bitBuf<<8 | uint32(data[pos])
			pos++
			bitCnt += 8
		}
		bitCnt -= codeWidth
		return int(bitBuf>>uint(bitCnt)) & ((1 << uint(codeWidth)) - 1), true
	}

	// The decode stops when cancel fires, checked every CancelReadChunk bytes of
	// output — the same granularity flate gets through CancelReader, expressed
	// against the output here because LZW's cost tracks what it produces, not
	// what it consumes (cancel.go).
	nextCancelCheck := math.MaxInt // never reached when cancel cannot fire
	if cancel.Cancellable() {
		nextCancelCheck = 0
	}
	for {
		if len(out) >= nextCancelCheck {
			if err := cancel.StopErr("decoding LZW stream"); err != nil {
				return nil, err
			}
			nextCancelCheck = len(out) + CancelReadChunk
		}
		code, ok := nextCode()
		if !ok {
			break
		}
		switch {
		case code == lzwEOD:
			return out, nil
		case code == lzwClearTable:
			reset()
			codeWidth = 9
			prev = nil
			continue
		}

		var entry []byte
		if code < len(table) {
			entry = table[code]
		} else if code == len(table) && prev != nil {
			// The KwKwK case: the code is the one about to be added.
			entry = append(append([]byte{}, prev...), prev[0])
		} else {
			return nil, fmt.Errorf("LZW: invalid code %d (table size %d)", code, len(table))
		}

		out = append(out, entry...)
		if len(out) > lim.DecodedStreamBytes {
			return nil, fmt.Errorf("LZW: %w (%d bytes)", ErrDecodeLimit, lim.DecodedStreamBytes)
		}

		if prev != nil {
			newEntry := append(append([]byte{}, prev...), entry[0])
			table = append(table, newEntry)
			// Widen the code once the next code to be read (len(table), the
			// index the next entry will take, allowing for the KwKwK case) no
			// longer fits. earlyChange (1 by default) widens one code sooner,
			// matching the encoder.
			if len(table)+earlyChange >= (1<<uint(codeWidth)) && codeWidth < 12 {
				codeWidth++
			}
		}
		prev = entry
	}
	return out, nil
}

// PredictorParms holds the /DecodeParms values that drive predictor reversal
// (ISO 32000-2:2020, 7.4.4.4 "LZW and Flate predictor functions").
type PredictorParms struct {
	Predictor        int
	Colors           int
	BitsPerComponent int
	Columns          int
}

// Resolver follows an indirect reference to the object it names, returning any
// other object unchanged. View.Resolve is one. A decode is given one because
// ISO 32000 lets /Filter, /DecodeParms, each element of either array and each
// value inside a decode-parms dictionary be indirect, and a decoder that only
// understands direct values silently skips a predictor and hands back garbage
// (audit 2026-09-22 C151). A nil Resolver means no object graph is available;
// a reference is then not followed, and a filter entry that is one makes the
// decode fail rather than guess.
type Resolver func(object.Object) object.Object

func (r Resolver) resolve(o object.Object) object.Object {
	if r == nil {
		return o
	}
	return r(o)
}

// FilterStep is one stage of a stream's filter chain, with its decode
// parameters resolved: Parms holds no indirect references at its top level.
type FilterStep struct {
	Name  object.Name
	Parms *object.Dictionary
}

// FilterChain returns a stream's filters in the order they are applied to
// decode it, each with its decode parameters, every indirect reference among
// them resolved. An absent /Filter is an empty chain.
//
// It is the one reading of /Filter and /DecodeParms: DecodeStreamData decodes
// by it, and every stage goes through ApplyFilter, which asks FilterSupported
// first — so "can pdf0 decode this stream?" and "what does decoding it do?"
// are answered by the same code. (StreamFiltersSupported, which answered the
// first question from a table of its own that knew nothing of indirect
// parameters, is gone: a caller gets the answer as the decode's Reason.)
func FilterChain(stream *object.Stream, resolve Resolver) ([]FilterStep, error) {
	if resolve == nil && filterEntriesIndirect(stream) {
		// Nothing to follow the reference with — a cross-reference stream is
		// decoded before there is an object table, and ISO 32000-2 7.5.8.2
		// requires its entries to be direct. Decoding without the parameters
		// would silently skip a predictor and return garbage.
		return nil, errors.New("/Filter or /DecodeParms holds an indirect reference, which cannot be resolved here")
	}
	filter := resolve.resolve(stream.Dict.Get("Filter"))
	parms := resolve.resolve(stream.Dict.Get("DecodeParms"))
	switch f := filter.(type) {
	case nil, object.Null:
		return nil, nil
	case object.Name:
		return []FilterStep{{Name: f, Parms: ParmsDictAt(parms, 0, resolve)}}, nil
	case object.Array:
		steps := make([]FilterStep, 0, len(f))
		for i, e := range f {
			name, ok := resolve.resolve(e).(object.Name)
			if !ok {
				return nil, fmt.Errorf("filter array element %d is not a name", i)
			}
			steps = append(steps, FilterStep{Name: name, Parms: ParmsDictAt(parms, i, resolve)})
		}
		return steps, nil
	default:
		return nil, fmt.Errorf("/Filter is a %T, not a name or an array", filter)
	}
}

// filterEntriesIndirect reports whether /Filter or /DecodeParms, an element of
// either, or a value in a decode-parms dictionary is an indirect reference.
func filterEntriesIndirect(stream *object.Stream) bool {
	isRef := func(o object.Object) bool { _, ok := o.(object.IndirectRef); return ok }
	dictHasRef := func(d *object.Dictionary) bool {
		for v := range d.Values() {
			if isRef(v) {
				return true
			}
		}
		return false
	}
	for _, key := range []object.Name{"Filter", "DecodeParms"} {
		switch v := stream.Dict.Get(key).(type) {
		case object.IndirectRef:
			return true
		case *object.Dictionary:
			if dictHasRef(v) {
				return true
			}
		case object.Array:
			for _, e := range v {
				switch x := e.(type) {
				case object.IndirectRef:
					return true
				case *object.Dictionary:
					if dictHasRef(x) {
						return true
					}
				}
			}
		}
	}
	return false
}

// ParmsDictAt returns the decode-parms dictionary for the i-th filter in the
// chain, or nil if there is none. parms is the /DecodeParms value: a
// dictionary (single filter) or an array parallel to the /Filter array, whose
// elements are dictionaries or null. Every indirect reference — the value
// itself, an array element, a value inside the dictionary — is resolved
// through resolve; when the dictionary holds one, a resolved copy is returned
// and the document's own dictionary is left alone.
func ParmsDictAt(parms object.Object, i int, resolve Resolver) *object.Dictionary {
	var d *object.Dictionary
	switch p := resolve.resolve(parms).(type) {
	case *object.Dictionary:
		if i == 0 {
			d = p
		}
	case object.Array:
		if i < len(p) {
			d, _ = resolve.resolve(p[i]).(*object.Dictionary)
		}
	}
	if d == nil || resolve == nil {
		return d
	}
	direct := true
	for v := range d.Values() {
		if _, isRef := v.(object.IndirectRef); isRef {
			direct = false
			break
		}
	}
	if direct {
		return d
	}
	out := d.Clone()
	for k, v := range d.All() {
		if _, isRef := v.(object.IndirectRef); isRef {
			out.Set(k, resolve(v))
		}
	}
	return out
}

// PredictorFromDict extracts predictor parameters from a decode-parms
// dictionary, applying the spec defaults (Predictor 1, Colors 1,
// BitsPerComponent 8, Columns 1). d is expected to come from ParmsDictAt, which
// has resolved its values.
func PredictorFromDict(d *object.Dictionary) PredictorParms {
	p := PredictorParms{Predictor: 1, Colors: 1, BitsPerComponent: 8, Columns: 1}
	if d == nil {
		return p
	}
	getInt := func(key object.Name, def int) int {
		if v, ok := d.Get(key).(object.Integer); ok {
			return int(v)
		}
		return def
	}
	p.Predictor = getInt("Predictor", 1)
	p.Colors = getInt("Colors", 1)
	p.BitsPerComponent = getInt("BitsPerComponent", 8)
	p.Columns = getInt("Columns", 1)
	return p
}

// ApplyPredictor reverses the predictor transformation on decoded filter
// output. Predictor 1 is the identity, 2 is TIFF horizontal differencing,
// and 10-15 are the PNG filters (the per-row filter byte decides which).
func ApplyPredictor(data []byte, p PredictorParms) ([]byte, error) {
	switch {
	case p.Predictor == 1:
		return data, nil
	case p.Predictor == 2:
		return applyTIFFPredictor(data, p)
	case p.Predictor >= 10 && p.Predictor <= 15:
		return applyPNGPredictor(data, p)
	default:
		return nil, fmt.Errorf("unsupported /Predictor %d", p.Predictor)
	}
}

func (p PredictorParms) validate() error {
	if p.Colors < 1 || p.Columns < 1 {
		return fmt.Errorf("invalid predictor parameters: Colors=%d Columns=%d", p.Colors, p.Columns)
	}
	switch p.BitsPerComponent {
	case 1, 2, 4, 8, 16:
	default:
		return fmt.Errorf("invalid predictor BitsPerComponent %d", p.BitsPerComponent)
	}
	// Guard against absurd row sizes on hostile input.
	if p.Colors > 64 || p.Columns > 1<<24 {
		return fmt.Errorf("predictor parameters out of range: Colors=%d Columns=%d", p.Colors, p.Columns)
	}
	return nil
}

// rowLength returns the number of bytes in one row of predictor output.
func (p PredictorParms) rowLength() int {
	return (p.Colors*p.BitsPerComponent*p.Columns + 7) / 8
}

// bytesPerPixel returns the byte distance between corresponding samples of
// adjacent pixels, as used by the PNG filters (minimum 1).
func (p PredictorParms) bytesPerPixel() int {
	bpp := p.Colors * p.BitsPerComponent / 8
	if bpp < 1 {
		bpp = 1
	}
	return bpp
}

// applyTIFFPredictor reverses TIFF Predictor 2 (horizontal differencing).
// Only 8- and 16-bit components are supported; sub-byte components are rare
// in practice and rejected so callers can distinguish "unsupported" from
// "corrupt".
func applyTIFFPredictor(data []byte, p PredictorParms) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	rowLen := p.rowLength()
	if rowLen == 0 || len(data)%rowLen != 0 {
		return nil, fmt.Errorf("TIFF predictor: data length %d is not a multiple of row length %d", len(data), rowLen)
	}
	switch p.BitsPerComponent {
	case 8:
		for row := 0; row < len(data); row += rowLen {
			for i := p.Colors; i < rowLen; i++ {
				data[row+i] += data[row+i-p.Colors]
			}
		}
	case 16:
		stride := p.Colors * 2
		for row := 0; row < len(data); row += rowLen {
			for i := stride; i+1 < rowLen; i += 2 {
				prev := uint16(data[row+i-stride])<<8 | uint16(data[row+i-stride+1])
				cur := uint16(data[row+i])<<8 | uint16(data[row+i+1])
				sum := cur + prev
				data[row+i] = byte(sum >> 8)
				data[row+i+1] = byte(sum)
			}
		}
	default:
		return nil, fmt.Errorf("%w: TIFF predictor with BitsPerComponent %d", ErrUnsupportedFilter, p.BitsPerComponent)
	}
	return data, nil
}

// applyPNGPredictor reverses the PNG filters (predictors 10-15). Each row is
// prefixed with one filter-type byte; the Predictor value only declares that
// PNG filtering is in use.
func applyPNGPredictor(data []byte, p PredictorParms) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	rowLen := p.rowLength()
	// Every allocation below is sized by rowLen, which the file chooses (up to
	// 64 colours x 16 bits x 2^24 columns: 2 GiB), so rowLen is bounded by the
	// data before anything is allocated. A row is rowLen+1 bytes of input, so
	// no row buffer can be larger than the decoded data it came from, and that
	// is already capped by the decode limit. Empty data is zero rows, not one
	// row of zeros: 0 mod (rowLen+1) is 0, which is how an empty stream used to
	// allocate the file's full row length (audit 2026-09-22 C8).
	if len(data) == 0 {
		return data, nil
	}
	if rowLen+1 > len(data) || len(data)%(rowLen+1) != 0 {
		return nil, fmt.Errorf("PNG predictor: data length %d is not a multiple of row length %d", len(data), rowLen+1)
	}
	bpp := p.bytesPerPixel()
	rows := len(data) / (rowLen + 1)
	out := make([]byte, 0, rows*rowLen)
	prev := make([]byte, rowLen) // zero-filled row above the first
	for r := 0; r < rows; r++ {
		rowStart := r * (rowLen + 1)
		ft := data[rowStart]
		row := data[rowStart+1 : rowStart+1+rowLen]
		switch ft {
		case 0: // None
		case 1: // Sub
			for i := bpp; i < rowLen; i++ {
				row[i] += row[i-bpp]
			}
		case 2: // Up
			for i := 0; i < rowLen; i++ {
				row[i] += prev[i]
			}
		case 3: // Average
			for i := 0; i < rowLen; i++ {
				left := 0
				if i >= bpp {
					left = int(row[i-bpp])
				}
				row[i] += byte((left + int(prev[i])) / 2)
			}
		case 4: // Paeth
			for i := 0; i < rowLen; i++ {
				var left, upLeft byte
				if i >= bpp {
					left = row[i-bpp]
					upLeft = prev[i-bpp]
				}
				row[i] += paeth(left, prev[i], upLeft)
			}
		default:
			return nil, fmt.Errorf("PNG predictor: invalid filter type %d in row %d", ft, r)
		}
		out = append(out, row...)
		prev = row
	}
	return out, nil
}

// paeth is the PNG Paeth prediction function (PNG spec 9.4).
func paeth(a, b, c byte) byte {
	pa := abs(int(b) - int(c))
	pb := abs(int(a) - int(c))
	pc := abs(int(a) + int(b) - 2*int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// DecodeStreamData decodes a stream through its filter chain (FilterChain),
// every stage capped at lim.DecodedStreamBytes of output. resolve follows the
// indirect references /Filter and /DecodeParms may contain; see Resolver.
//
// The error says why the data could not be produced, and callers classify it
// with ReasonOf: ErrDecodeLimit and ErrUnsupportedFilter mean pdf0 declined, a
// wrapped context error means the operation was cancelled, and anything else
// means the stream's data is not what its filters say it is.
func DecodeStreamData(cancel Canceler, stream *object.Stream, lim Limits, resolve Resolver) ([]byte, error) {
	chain, err := FilterChain(stream, resolve)
	if err != nil {
		return nil, err
	}
	data := stream.Data
	for _, step := range chain {
		if data, err = ApplyFilter(cancel, step.Name, data, step.Parms, lim); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// FilterSupported reports, as ErrUnsupportedFilter, a filter stage pdf0 does
// not implement, and nil for one ApplyFilter decodes. ApplyFilter asks it
// first, so the two cannot disagree: a stage this accepts never fails for
// being unsupported, and one it refuses is never attempted.
//
// The image codecs (DCTDecode, JPXDecode, CCITTFaxDecode, JBIG2Decode) are not
// here. They produce pixels rather than bytes, and the image package decodes
// them itself; a general-purpose decode of a stream that uses one is
// unsupported, which is the truth for every caller that wants the bytes.
func FilterSupported(name object.Name, parms *object.Dictionary) error {
	switch name {
	case "FlateDecode", "LZWDecode":
		// Predictor 2 (TIFF) is implemented for 8- and 16-bit components only.
		// 1, 2 and 4 are legal and rare; any other value is not legal at all,
		// which ApplyPredictor reports as malformed.
		if p := PredictorFromDict(parms); p.Predictor == 2 {
			switch p.BitsPerComponent {
			case 1, 2, 4:
				return fmt.Errorf("%w: TIFF predictor with BitsPerComponent %d", ErrUnsupportedFilter, p.BitsPerComponent)
			}
		}
		return nil
	case "ASCIIHexDecode", "ASCII85Decode", "RunLengthDecode", "Crypt":
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnsupportedFilter, name)
}

// ApplyFilter reverses one filter stage. parms must already be resolved, as
// ParmsDictAt and FilterChain return it. Output is capped at
// lim.DecodedStreamBytes, whatever the filter.
func ApplyFilter(cancel Canceler, name object.Name, data []byte, parms *object.Dictionary, lim Limits) ([]byte, error) {
	if err := FilterSupported(name, parms); err != nil {
		return nil, err
	}
	switch name {
	case "FlateDecode":
		decoded, err := FlateDecode(cancel, data, lim)
		if err != nil {
			return nil, err
		}
		return ApplyPredictor(decoded, PredictorFromDict(parms))
	case "LZWDecode":
		early := 1
		if parms != nil {
			if e, ok := parms.Get("EarlyChange").(object.Integer); ok {
				early = int(e)
			}
		}
		decoded, err := LZWDecode(cancel, data, early, lim)
		if err != nil {
			return nil, err
		}
		return ApplyPredictor(decoded, PredictorFromDict(parms))
	case "ASCIIHexDecode":
		return asciiHexDecode(data, lim.DecodedStreamBytes)
	case "ASCII85Decode":
		return ascii85Decode(data, lim.DecodedStreamBytes)
	case "RunLengthDecode":
		return runLengthDecode(data, lim.DecodedStreamBytes)
	default: // "Crypt", the one other stage FilterSupported accepts
		// A stream's own crypt filter (ISO 32000-2 7.4.10) is applied by the
		// security handler when Read decrypts the document, so the data here
		// is already past it. On a document that was not decrypted it is
		// ciphertext, but so is every other stream: that state is Locked, and
		// View.Decode reports it before any filter runs.
		return data, nil
	}
}

// Decode errors a caller must be able to tell from a corrupt stream: the
// stream may be perfectly good, and pdf0 declined (a size limit) or does not
// implement the filter. Reporting either as a defect of the file would be a
// false non-conformance; ReasonOf classifies them, and a validator reports them
// as "limit" instead.
var (
	ErrDecodeLimit       = errors.New("decompressed data exceeds maximum size")
	ErrUnsupportedFilter = errors.New("unsupported filter")
)

// errOverCap is the error for output that would pass max bytes.
func errOverCap(what string, max int) error {
	return fmt.Errorf("%s: %w (%d bytes)", what, ErrDecodeLimit, max)
}

// readCapped reads r to the end, refusing more than max bytes of it. It reads
// at most max+1 bytes, the one extra being how "exactly max" is told from
// "more than max" — computed without overflow, since max is a caller's option
// and math.MaxInt is the natural way to write "no practical limit". The
// unguarded int64(max)+1 wrapped negative there, and io.LimitReader with a
// negative limit reads nothing, so every stream decoded to empty with no error
// (audit 2026-09-22 C48).
func readCapped(r io.Reader, max int, what string) ([]byte, error) {
	n := int64(max)
	if n < math.MaxInt64 {
		n++
	}
	b, err := io.ReadAll(io.LimitReader(r, n))
	if err != nil {
		return nil, err
	}
	if len(b) > max {
		return nil, errOverCap(what, max)
	}
	return b, nil
}

// FlateDecode inflates a zlib stream (RFC 1950 wrapping RFC 1951), capped at
// lim.DecodedStreamBytes of output.
//
// The deflate data is decoded to its final block and the stream is its
// content. The Adler-32 checksum that follows is not required: a stream whose
// deflate data is complete decodes whether its checksum is present, truncated
// or wrong. That is what the readers PDF files are made for do — Acrobat,
// pdf.js, Poppler, MuPDF and PDFium all render such a stream — so a producer
// that writes one ships it, and a validator that dropped it would be judging a
// file nobody sees: its content vanished from every check with no finding at
// all, so a device-colour violation in it went unreported (audit 2026-09-22
// C46). A deflate stream that ends before its final block, or whose data does
// not decode, is malformed: what came out is a prefix of the content, and a
// prefix is not the content.
func FlateDecode(cancel Canceler, data []byte, lim Limits) ([]byte, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("zlib: %w", io.ErrUnexpectedEOF)
	}
	cmf, flg := data[0], data[1]
	if cmf&0x0f != 8 || cmf>>4 > 7 || (uint16(cmf)<<8|uint16(flg))%31 != 0 {
		return nil, fmt.Errorf("zlib: %w", zlib.ErrHeader)
	}
	if flg&0x20 != 0 {
		// A preset dictionary (FDICT) names data the stream does not carry.
		return nil, fmt.Errorf("zlib: %w", zlib.ErrDictionary)
	}
	fr := flate.NewReader(bytes.NewReader(data[2:]))
	defer fr.Close()
	decoded, err := readCapped(CancelReader(cancel, fr), lim.DecodedStreamBytes, "flate")
	if err != nil {
		if errors.Is(err, ErrDecodeLimit) {
			return nil, err
		}
		return nil, fmt.Errorf("zlib decompress: %w", err)
	}
	return decoded, nil
}

// asciiHexDecode reverses ASCIIHexDecode (ISO 32000-2 7.4.2). Output is half
// the input at most, but it is still held to max so that every filter honours
// the same cap.
func asciiHexDecode(data []byte, max int) ([]byte, error) {
	// Filter out whitespace and stop at '>'
	var hexDigits []byte
	for _, b := range data {
		if b == '>' {
			break
		}
		if syntax.IsWhitespace(b) {
			continue
		}
		hexDigits = append(hexDigits, b)
	}
	if (len(hexDigits)+1)/2 > max {
		return nil, errOverCap("ASCIIHex", max)
	}
	return syntax.DecodeHex(hexDigits)
}

// ascii85Decode reverses ASCII85Decode (ISO 32000-2 7.4.3): each group of five
// characters '!'..'u' is a base-85 number giving four bytes, 'z' stands for
// four zero bytes, white space is ignored, "~>" ends the data, and a final
// partial group of n characters gives n-1 bytes. 'z' expands one byte to four,
// so output is checked against max as it grows.
//
// The end-of-data marker is not required: the stream's own length already ends
// the data, and a producer that omitted the marker wrote every byte it meant
// to. Anything else outside the alphabet is malformed.
func ascii85Decode(data []byte, max int) ([]byte, error) {
	out := make([]byte, 0, min(len(data)/5*4+4, max))
	var group [5]byte
	n := 0
	emit := func(count int) error {
		var v uint64
		for _, c := range group {
			v = v*85 + uint64(c)
		}
		if v > math.MaxUint32 {
			return fmt.Errorf("ASCII85: group value %d exceeds 32 bits", v)
		}
		if len(out)+count > max {
			return errOverCap("ASCII85", max)
		}
		b := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		out = append(out, b[:count]...)
		return nil
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case syntax.IsWhitespace(c):
			continue
		case c == '~':
			// "~>" is end of data; a '~' followed by anything else is not
			// part of the alphabet.
			j := i + 1
			for j < len(data) && syntax.IsWhitespace(data[j]) {
				j++
			}
			if j < len(data) && data[j] != '>' {
				return nil, fmt.Errorf("ASCII85: '~' not followed by '>' at offset %d", i)
			}
			i = len(data)
		case c == 'z':
			if n != 0 {
				return nil, fmt.Errorf("ASCII85: 'z' inside a group at offset %d", i)
			}
			group = [5]byte{}
			if err := emit(4); err != nil {
				return nil, err
			}
		case c >= '!' && c <= 'u':
			group[n] = c - '!'
			n++
			if n == 5 {
				if err := emit(4); err != nil {
					return nil, err
				}
				n = 0
			}
		default:
			return nil, fmt.Errorf("ASCII85: invalid character %#x at offset %d", c, i)
		}
	}
	switch n {
	case 0:
	case 1:
		return nil, errors.New("ASCII85: a final group of one character encodes no byte")
	default:
		// A final group of n characters is padded with 'u' (84) to five and
		// yields its first n-1 bytes.
		for k := n; k < 5; k++ {
			group[k] = 84
		}
		if err := emit(n - 1); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// runLengthDecode reverses RunLengthDecode (ISO 32000-2 7.4.5): a length byte
// 0..127 is followed by that many plus one literal bytes, 129..255 by one byte
// repeated 257 minus the length times, and 128 ends the data.
//
// Two bytes of input can ask for 128 bytes of output, so the output size is
// computed in a first pass that allocates nothing and checked against max
// before the one allocation. A literal run cut short by the end of the data is
// malformed; a missing end-of-data byte is not, for the reason ascii85Decode
// gives.
func runLengthDecode(data []byte, max int) ([]byte, error) {
	size := 0
	i := 0
	for i < len(data) {
		l := int(data[i])
		i++
		switch {
		case l == 128:
			i = len(data) + 1
			continue
		case l < 128:
			if i+l+1 > len(data) {
				return nil, fmt.Errorf("RunLength: literal run of %d bytes at offset %d runs past the end of the data", l+1, i-1)
			}
			size += l + 1
			i += l + 1
		default:
			if i >= len(data) {
				return nil, fmt.Errorf("RunLength: repeat run at offset %d has no byte to repeat", i-1)
			}
			size += 257 - l
			i++
		}
		if size > max {
			return nil, errOverCap("RunLength", max)
		}
	}
	out := make([]byte, 0, size)
	for i = 0; i < len(data); {
		l := int(data[i])
		i++
		switch {
		case l == 128:
			return out, nil
		case l < 128:
			out = append(out, data[i:i+l+1]...)
			i += l + 1
		default:
			for k := 0; k < 257-l; k++ {
				out = append(out, data[i])
			}
			i++
		}
	}
	return out, nil
}

// flateWriters pools the zlib compressors FlateEncode uses.
//
// zlib.NewWriter allocates the deflate window and hash tables — about a
// megabyte — and a document compresses one stream per content stream, form,
// pattern, page, object stream and cross-reference stream. Building a
// single-page document allocated 150 MB, and a quarter of it was compressors
// that were used once and dropped.
//
// Reset is what makes this safe: it returns a writer to exactly the state
// NewWriter produces, so the bytes out are the bytes a fresh writer would have
// written. TestPooledFlateMatchesAFreshWriter holds that.
var flateWriters = sync.Pool{
	New: func() any { return zlib.NewWriter(io.Discard) },
}

// FlateEncode zlib-compresses data (the inverse of FlateDecode) for writing a
// FlateDecode stream such as a cross-reference stream.
func FlateEncode(data []byte) []byte {
	var buf bytes.Buffer
	w := flateWriters.Get().(*zlib.Writer)
	w.Reset(&buf)
	if _, err := w.Write(data); err != nil {
		// A bytes.Buffer does not fail, so this is unreachable; a writer that
		// has seen an error is not returned to the pool either way, since its
		// error state is sticky and would poison the next caller.
		w.Close()
		return buf.Bytes()
	}
	if err := w.Close(); err != nil {
		return buf.Bytes()
	}
	flateWriters.Put(w)
	return buf.Bytes()
}
