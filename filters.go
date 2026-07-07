package pdf0

import "fmt"

// predictorParms holds the /DecodeParms values that drive predictor reversal
// (ISO 32000-2:2020, 7.4.4.4 "LZW and Flate predictor functions").
type predictorParms struct {
	Predictor        int
	Colors           int
	BitsPerComponent int
	Columns          int
}

// parmsDictAt returns the decode-parms dictionary for the i-th filter in the
// chain, or nil if there is none. parms is the raw /DecodeParms value: a
// dictionary (single filter) or an array parallel to the /Filter array, whose
// elements are dictionaries or null.
func parmsDictAt(parms Object, i int) *Dictionary {
	switch p := parms.(type) {
	case *Dictionary:
		if i == 0 {
			return p
		}
	case Array:
		if i < len(p) {
			if d, ok := p[i].(*Dictionary); ok {
				return d
			}
		}
	}
	return nil
}

// predictorFromDict extracts predictor parameters from a decode-parms
// dictionary, applying the spec defaults (Predictor 1, Colors 1,
// BitsPerComponent 8, Columns 1).
func predictorFromDict(d *Dictionary) predictorParms {
	p := predictorParms{Predictor: 1, Colors: 1, BitsPerComponent: 8, Columns: 1}
	if d == nil {
		return p
	}
	getInt := func(key Name, def int) int {
		if v, ok := d.Get(key).(Integer); ok {
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

// applyPredictor reverses the predictor transformation on decoded filter
// output. Predictor 1 is the identity, 2 is TIFF horizontal differencing,
// and 10-15 are the PNG filters (the per-row filter byte decides which).
func applyPredictor(data []byte, p predictorParms) ([]byte, error) {
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

func (p predictorParms) validate() error {
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
func (p predictorParms) rowLength() int {
	return (p.Colors*p.BitsPerComponent*p.Columns + 7) / 8
}

// bytesPerPixel returns the byte distance between corresponding samples of
// adjacent pixels, as used by the PNG filters (minimum 1).
func (p predictorParms) bytesPerPixel() int {
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
func applyTIFFPredictor(data []byte, p predictorParms) ([]byte, error) {
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
		return nil, fmt.Errorf("TIFF predictor with BitsPerComponent %d not supported", p.BitsPerComponent)
	}
	return data, nil
}

// applyPNGPredictor reverses the PNG filters (predictors 10-15). Each row is
// prefixed with one filter-type byte; the Predictor value only declares that
// PNG filtering is in use.
func applyPNGPredictor(data []byte, p predictorParms) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	rowLen := p.rowLength()
	if len(data)%(rowLen+1) != 0 {
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
