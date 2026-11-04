package zpaq

// encoder is the ZPAQ binary arithmetic coder (32-bit, carryless) driven by a
// predictor. With no model components (n == 0) it stores the data in chunks.
type encoder struct {
	low, high uint32
	pr        *predictor // nil when the block is stored
	out       []byte
	buf       []byte // stored mode: pending chunk
}

func newEncoder(pr *predictor) *encoder {
	e := &encoder{low: 1, high: 0xFFFFFFFF, pr: pr}
	if pr == nil || pr.n == 0 {
		e.pr = nil
		e.low = 0
	}
	return e
}

func (e *encoder) encode(y int, p int) {
	mid := e.low + uint32((uint64(e.high-e.low)*uint64(uint32(p)))>>16)
	if y != 0 {
		e.high = mid
	} else {
		e.low = mid + 1
	}
	for (e.high^e.low)&0xff000000 == 0 {
		e.out = append(e.out, byte(e.high>>24))
		e.high = e.high<<8 | 255
		e.low <<= 8
		if e.low == 0 {
			e.low = 1
		}
	}
}

// compress codes one byte, or -1 for the end of the segment.
func (e *encoder) compress(c int) {
	if e.pr != nil {
		if c == -1 {
			e.encode(1, 0)
			return
		}
		e.encode(0, 0)
		for i := 7; i >= 0; i-- {
			p := e.pr.predict()*2 + 1
			y := c >> uint(i) & 1
			e.encode(y, p)
			e.pr.update(y)
		}
		return
	}
	// stored: chunks of up to 64 KiB, each with a 4-byte big-endian length
	if len(e.buf) > 0 && (c < 0 || len(e.buf) == 1<<16) {
		n := len(e.buf)
		e.out = append(e.out, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		e.out = append(e.out, e.buf...)
		e.buf = e.buf[:0]
	}
	if c >= 0 {
		e.buf = append(e.buf, byte(c))
	}
}

// decoder reads arithmetic-coded (or stored) bytes from an in-memory segment.
type decoder struct {
	low, high, curr uint32
	pr              *predictor // nil when stored
	src             byteSource
}

// byteSource yields compressed bytes; get returns -1 at the end of input.
type byteSource interface{ get() int }

// sliceSource reads from a byte slice.
type sliceSource struct {
	b   []byte
	pos int
}

func (s *sliceSource) get() int {
	if s.pos < len(s.b) {
		c := s.b[s.pos]
		s.pos++
		return int(c)
	}
	return -1
}

func newDecoder(pr *predictor, src byteSource) *decoder {
	d := &decoder{pr: pr, src: src}
	if pr == nil || pr.n == 0 {
		d.pr = nil
	} else {
		d.low, d.high = 1, 0xFFFFFFFF
	}
	return d
}

func (d *decoder) get() int { return d.src.get() }

func (d *decoder) decode(p int) int {
	if d.curr < d.low || d.curr > d.high {
		formatPanic("archive corrupted")
	}
	mid := d.low + uint32((uint64(d.high-d.low)*uint64(uint32(p)))>>16)
	var y int
	if d.curr <= mid {
		y = 1
		d.high = mid
	} else {
		d.low = mid + 1
	}
	for (d.high^d.low)&0xff000000 == 0 {
		d.high = d.high<<8 | 255
		d.low <<= 8
		if d.low == 0 {
			d.low = 1
		}
		c := d.get()
		if c < 0 {
			formatPanic("unexpected end of file")
		}
		d.curr = d.curr<<8 | uint32(c)
	}
	return y
}

// decompress returns the next byte or -1 at the end of the segment.
func (d *decoder) decompress() int {
	if d.pr != nil {
		if d.curr == 0 {
			for i := 0; i < 4; i++ {
				c := d.get()
				if c < 0 {
					formatPanic("unexpected end of file")
				}
				d.curr = d.curr<<8 | uint32(c)
			}
		}
		if d.decode(0) != 0 {
			if d.curr != 0 {
				formatPanic("decoding end of stream")
			}
			return -1
		}
		c := 1
		for c < 256 {
			p := d.pr.predict()*2 + 1
			c += c + d.decode(p)
			d.pr.update(c & 1)
		}
		return c - 256
	}
	if d.curr == 0 {
		for i := 0; i < 4; i++ {
			c := d.get()
			if c < 0 {
				formatPanic("unexpected end of file")
			}
			d.curr = d.curr<<8 | uint32(c)
		}
		if d.curr == 0 {
			return -1
		}
	}
	d.curr--
	c := d.get()
	if c < 0 {
		formatPanic("unexpected end of file")
	}
	return c
}

// skipSegment consumes the rest of a segment's coded data without decoding it
// (as libzpaq's Decoder::skip does) and returns the byte that follows the
// end-of-segment zeros (the 253/254 checksum marker), or -1 at end of input.
// modeled tells whether the block has model components.
func skipSegment(src byteSource, modeled bool) int {
	var curr uint32
	c := -1
	if modeled {
		for curr == 0 { // at start?
			v := src.get()
			if v < 0 {
				return -1
			}
			curr = uint32(v)
		}
		for curr != 0 { // find 4 zero bytes
			if c = src.get(); c < 0 {
				break
			}
			curr = curr<<8 | uint32(c)
		}
		for { // there might be more than 4
			if c = src.get(); c != 0 {
				return c
			}
		}
	}
	for i := 0; i < 4; i++ {
		if c = src.get(); c < 0 {
			break
		}
		curr = curr<<8 | uint32(c)
	}
	for curr > 0 {
		for curr > 0 {
			curr--
			if src.get() < 0 {
				formatPanic("skipped to end of file")
			}
		}
		for i := 0; i < 4; i++ {
			if c = src.get(); c < 0 {
				break
			}
			curr = curr<<8 | uint32(c)
		}
	}
	if c >= 0 {
		c = src.get()
	}
	return c
}
