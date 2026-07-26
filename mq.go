package pdf0

// MQ arithmetic decoder (ISO/IEC 14492 / ITU-T T.88 Annex E, identical to the
// JPEG 2000 / JBIG coder). It decodes a binary decision given an adaptive
// probability context. The implementation follows the software conventions in
// Annex E, keeping the code register split into a high and low half so all
// arithmetic stays within 32 bits.

// mqState is a single adaptive context: the current Qe-table index and the sense
// of the more-probable symbol (MPS), packed as (index<<1)|mps.
type mqState = uint8

// mqQe is one row of the Qe estimation-state table (Table E.1).
type mqQe struct {
	qe         uint32
	nmps, nlps uint8
	sw         uint8 // SWITCH: flip the MPS sense on an LPS in this state
}

var mqQeTable = [47]mqQe{
	{0x5601, 1, 1, 1}, {0x3401, 2, 6, 0}, {0x1801, 3, 9, 0}, {0x0AC1, 4, 12, 0},
	{0x0521, 5, 29, 0}, {0x0221, 38, 33, 0}, {0x5601, 7, 6, 1}, {0x5401, 8, 14, 0},
	{0x4801, 9, 14, 0}, {0x3801, 10, 14, 0}, {0x3001, 11, 17, 0}, {0x2401, 12, 18, 0},
	{0x1C01, 13, 20, 0}, {0x1601, 29, 21, 0}, {0x5601, 15, 14, 1}, {0x5401, 16, 14, 0},
	{0x5101, 17, 15, 0}, {0x4801, 18, 16, 0}, {0x3801, 19, 17, 0}, {0x3401, 20, 18, 0},
	{0x3001, 21, 19, 0}, {0x2801, 22, 19, 0}, {0x2401, 23, 20, 0}, {0x2201, 24, 21, 0},
	{0x1C01, 25, 22, 0}, {0x1801, 26, 23, 0}, {0x1601, 27, 24, 0}, {0x1401, 28, 25, 0},
	{0x1201, 29, 26, 0}, {0x1101, 30, 27, 0}, {0x0AC1, 31, 28, 0}, {0x09C1, 32, 29, 0},
	{0x08A1, 33, 30, 0}, {0x0521, 34, 31, 0}, {0x0441, 35, 32, 0}, {0x02A1, 36, 33, 0},
	{0x0221, 37, 34, 0}, {0x0141, 38, 35, 0}, {0x0111, 39, 36, 0}, {0x0085, 40, 37, 0},
	{0x0049, 41, 38, 0}, {0x0025, 42, 39, 0}, {0x0015, 43, 40, 0}, {0x0009, 44, 41, 0},
	{0x0005, 45, 42, 0}, {0x0001, 45, 43, 0}, {0x5601, 46, 46, 0},
}

type mqDecoder struct {
	data              []byte
	bp, end           int
	chigh, clow, a, c uint32
	ct                int
}

func newMQDecoder(data []byte, start, end int) *mqDecoder {
	d := &mqDecoder{data: data, bp: start, end: end}
	d.chigh = uint32(d.at(start))
	d.clow = 0
	d.byteIn()
	d.chigh = ((d.chigh << 7) & 0xFFFF) | ((d.clow >> 9) & 0x7F)
	d.clow = (d.clow << 7) & 0xFFFF
	d.ct -= 7
	d.a = 0x8000
	return d
}

func (d *mqDecoder) at(i int) byte {
	if i >= 0 && i < d.end {
		return d.data[i]
	}
	return 0xFF
}

func (d *mqDecoder) byteIn() {
	if d.at(d.bp) == 0xFF {
		if d.at(d.bp+1) > 0x8F {
			d.clow += 0xFF00
			d.ct = 8
		} else {
			d.bp++
			d.clow += uint32(d.at(d.bp)) << 9
			d.ct = 7
		}
	} else {
		d.bp++
		if d.bp < d.end {
			d.clow += uint32(d.data[d.bp]) << 8
		} else {
			d.clow += 0xFF00
		}
		d.ct = 8
	}
	if d.clow > 0xFFFF {
		d.chigh += d.clow >> 16
		d.clow &= 0xFFFF
	}
}

// decode returns the next binary decision for the given context slot in cx.
func (d *mqDecoder) decode(cx []mqState, pos int) int {
	idx := cx[pos] >> 1
	mps := cx[pos] & 1
	q := mqQeTable[idx]
	d.a -= q.qe
	var bit uint8
	if d.chigh < q.qe {
		// LPS exchange.
		if d.a < q.qe {
			d.a = q.qe
			bit = mps
			idx = q.nmps
		} else {
			d.a = q.qe
			bit = 1 ^ mps
			if q.sw == 1 {
				mps = bit
			}
			idx = q.nlps
		}
	} else {
		d.chigh -= q.qe
		if d.a&0x8000 != 0 {
			return int(mps)
		}
		// MPS exchange.
		if d.a < q.qe {
			bit = 1 ^ mps
			if q.sw == 1 {
				mps = bit
			}
			idx = q.nlps
		} else {
			bit = mps
			idx = q.nmps
		}
	}
	// Renormalize.
	for {
		if d.ct == 0 {
			d.byteIn()
		}
		d.a <<= 1
		d.chigh = ((d.chigh << 1) & 0xFFFF) | ((d.clow >> 15) & 1)
		d.clow = (d.clow << 1) & 0xFFFF
		d.ct--
		if d.a&0x8000 != 0 {
			break
		}
	}
	cx[pos] = (idx << 1) | mps
	return int(bit)
}
