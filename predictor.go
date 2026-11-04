package zpaq

// Lookup tables shared by all predictors (built once at start-up).
var (
	squasht  [4096]uint16
	stretcht [32768]int16
)

func init() {
	for i := 1376; i < 2720; i++ {
		squasht[i] = ssquasht[i-1376]
	}
	for i := 2720; i < 4096; i++ {
		squasht[i] = 32767
	}
	k := 16384
	for i := 0; i < 712; i++ {
		for j := int(stdt[i]); j > 0; j-- {
			stretcht[k] = int16(i)
			k++
		}
	}
	for i := 0; i < 16384; i++ {
		stretcht[i] = -stretcht[32767-i]
	}
}

func squash(x int) int    { return int(squasht[x+2048]) }
func stretch(x int) int   { return int(stretcht[x]) }
func clamp2k(x int) int   { return min(max(x, -2048), 2047) }
func clamp512k(x int) int { return min(max(x, -(1<<19)), (1<<19)-1) }

// cminit is the initial probability (times 2^23) for a bit-history state.
func cminit(state int) uint32 {
	n0, n1 := int(sns[state*4+2]), int(sns[state*4+3])
	return uint32(((n1*2 + 1) << 22) / (n0 + n1 + 1))
}

// component is one element of the model; cm, ht and a16 are used as in libzpaq.
type component struct {
	typ   byte
	arg   [5]byte // the header bytes after the type
	limit uint
	cxt   uint
	a, b  uint
	c     uint
	cm    []uint32
	cmM   uint // len(cm)-1 for masked access
	ht    []byte
	htM   uint
	a16   []uint16
}

// predictor is the ZPAQ context-mixing predictor driven by a ZPAQL program.
type predictor struct {
	z     *zpaql
	n     int
	c8    uint32 // bits of the current byte with a leading 1
	hmap4 uint32 // bit-history context within a nibble
	h     [256]uint32
	p     [256]int
	comp  [256]component
}

func newPredictor(z *zpaql) *predictor {
	pr := &predictor{z: z, c8: 1, hmap4: 1}
	pr.init()
	return pr
}

func (pr *predictor) init() {
	z := pr.z
	z.inith()
	pr.n = int(z.header[6])
	cp := 7
	for i := 0; i < pr.n; i++ {
		cr := &pr.comp[i]
		hdr := z.header[cp : cp+compSize[z.header[cp]]]
		cr.typ = hdr[0]
		copy(cr.arg[:], hdr[1:])
		switch cr.typ {
		case compConst:
			pr.p[i] = (int(hdr[1]) - 128) * 4
		case compCM:
			if hdr[1] > 32 {
				formatPanic("max size for CM is 32")
			}
			cr.cm = make([]uint32, 1<<hdr[1])
			cr.cmM = uint(len(cr.cm) - 1)
			cr.limit = uint(hdr[2]) * 4
			for j := range cr.cm {
				cr.cm[j] = 0x80000000
			}
		case compICM:
			if hdr[1] > 26 {
				formatPanic("max size for ICM is 26")
			}
			cr.limit = 1023
			cr.cm = make([]uint32, 256)
			cr.cmM = 255
			cr.ht = make([]byte, 64<<hdr[1])
			for j := range cr.cm {
				cr.cm[j] = cminit(j)
			}
		case compMatch:
			if hdr[1] > 32 || hdr[2] > 32 {
				formatPanic("max size for MATCH is 32 32")
			}
			cr.cm = make([]uint32, 1<<hdr[1])
			cr.cmM = uint(len(cr.cm) - 1)
			cr.ht = make([]byte, 1<<hdr[2])
			cr.htM = uint(len(cr.ht) - 1)
			cr.ht[0] = 1
		case compAvg:
			if int(hdr[1]) >= i {
				formatPanic("AVG j >= i")
			}
			if int(hdr[2]) >= i {
				formatPanic("AVG k >= i")
			}
		case compMix2:
			if hdr[1] > 32 {
				formatPanic("max size for MIX2 is 32")
			}
			if int(hdr[3]) >= i {
				formatPanic("MIX2 k >= i")
			}
			if int(hdr[2]) >= i {
				formatPanic("MIX2 j >= i")
			}
			cr.c = uint(1) << hdr[1]
			cr.a16 = make([]uint16, 1<<hdr[1])
			for j := range cr.a16 {
				cr.a16[j] = 32768
			}
		case compMix:
			if hdr[1] > 32 {
				formatPanic("max size for MIX is 32")
			}
			if int(hdr[2]) >= i {
				formatPanic("MIX j >= i")
			}
			if hdr[3] < 1 || int(hdr[3]) > i-int(hdr[2]) {
				formatPanic("MIX m not in 1..i-j")
			}
			m := int(hdr[3])
			cr.c = uint(1) << hdr[1]
			cr.cm = make([]uint32, m<<hdr[1])
			for j := range cr.cm {
				cr.cm[j] = uint32(65536 / m)
			}
		case compISSE:
			if hdr[1] > 32 {
				formatPanic("max size for ISSE is 32")
			}
			if int(hdr[2]) >= i {
				formatPanic("ISSE j >= i")
			}
			cr.ht = make([]byte, 64<<hdr[1])
			cr.cm = make([]uint32, 512)
			for j := 0; j < 256; j++ {
				cr.cm[j*2] = 1 << 15
				cr.cm[j*2+1] = uint32(int32(clamp512k(stretch(int(cminit(j)>>8)) * 1024)))
			}
		case compSSE:
			if hdr[1] > 32 {
				formatPanic("max size for SSE is 32")
			}
			if int(hdr[2]) >= i {
				formatPanic("SSE j >= i")
			}
			if hdr[3] > hdr[4]*4 {
				formatPanic("SSE start > limit*4")
			}
			cr.cm = make([]uint32, 32<<hdr[1])
			cr.cmM = uint(len(cr.cm) - 1)
			cr.limit = uint(hdr[4]) * 4
			for j := range cr.cm {
				cr.cm[j] = uint32(squash((j&31)*64-992))<<17 | uint32(hdr[3])
			}
		default:
			formatPanic("unknown component type")
		}
		cp += compSize[cr.typ]
	}
}

// find returns the offset of the 16-byte bucket for context cxt in ht,
// replacing the least-used of three candidates if it is not present.
func find(ht []byte, sizebits uint, cxt uint32) uint {
	chk := byte(cxt >> sizebits & 255)
	h0 := uint(cxt*16) & uint(len(ht)-16)
	if ht[h0] == chk {
		return h0
	}
	h1 := h0 ^ 16
	if ht[h1] == chk {
		return h1
	}
	h2 := h0 ^ 32
	if ht[h2] == chk {
		return h2
	}
	var r uint
	switch {
	case ht[h0+1] <= ht[h1+1] && ht[h0+1] <= ht[h2+1]:
		r = h0
	case ht[h1+1] < ht[h2+1]:
		r = h1
	default:
		r = h2
	}
	clear(ht[r : r+16])
	ht[r] = chk
	return r
}

// predict returns P(next bit = 1) as a 16-bit number scaled by 2^16... exactly
// as libzpaq: a 12-bit probability (0..4095), which the coder doubles and adds 1.
func (pr *predictor) predict() int {
	c8, hmap4 := pr.c8, pr.hmap4
	for i := 0; i < pr.n; i++ {
		cr := &pr.comp[i]
		switch cr.typ {
		case compConst:
		case compCM:
			cr.cxt = uint(pr.h[i] ^ hmap4)
			pr.p[i] = stretch(int(cr.cm[cr.cxt&cr.cmM] >> 17))
		case compICM:
			if c8 == 1 || c8&0xf0 == 16 {
				cr.c = find(cr.ht, uint(cr.arg[0])+2, pr.h[i]+16*c8)
			}
			cr.cxt = uint(cr.ht[cr.c+uint(hmap4&15)])
			pr.p[i] = stretch(int(cr.cm[cr.cxt&cr.cmM] >> 8))
		case compMatch:
			if cr.a == 0 {
				pr.p[i] = 0
			} else {
				cr.c = uint(cr.ht[(cr.limit-cr.b)&cr.htM]>>(7-cr.cxt)) & 1
				pr.p[i] = stretch((int(sdt2k[cr.a]) * (1 - 2*int(cr.c))) & 32767)
			}
		case compAvg:
			j, k, wt := int(cr.arg[0]), int(cr.arg[1]), int(cr.arg[2])
			pr.p[i] = (pr.p[j]*wt + pr.p[k]*(256-wt)) >> 8
		case compMix2:
			cr.cxt = uint(pr.h[i]+(c8&uint32(cr.arg[4]))) & (cr.c - 1)
			w := int(cr.a16[cr.cxt])
			pr.p[i] = (w*pr.p[cr.arg[1]] + (65536-w)*pr.p[cr.arg[2]]) >> 16
		case compMix:
			m := int(cr.arg[2])
			cr.cxt = (uint(pr.h[i]+(c8&uint32(cr.arg[4]))) & (cr.c - 1)) * uint(m)
			wt := cr.cm[cr.cxt : cr.cxt+uint(m)]
			j := int(cr.arg[1])
			sum := 0
			for t := 0; t < m; t++ {
				sum += (int(int32(wt[t])) >> 8) * pr.p[j+t]
			}
			pr.p[i] = clamp2k(sum >> 8)
		case compISSE:
			if c8 == 1 || c8&0xf0 == 16 {
				cr.c = find(cr.ht, uint(cr.arg[0])+2, pr.h[i]+16*c8)
			}
			cr.cxt = uint(cr.ht[cr.c+uint(hmap4&15)])
			w0, w1 := int(int32(cr.cm[cr.cxt*2])), int(int32(cr.cm[cr.cxt*2+1]))
			pr.p[i] = clamp2k((w0*pr.p[cr.arg[1]] + w1*64) >> 16)
		case compSSE:
			cr.cxt = uint((pr.h[i] + c8) * 32)
			pq := pr.p[cr.arg[1]] + 992
			pq = min(max(pq, 0), 1983)
			wt := pq & 63
			pq >>= 6
			cr.cxt += uint(pq)
			pr.p[i] = stretch(((int(cr.cm[cr.cxt&cr.cmM]>>10))*(64-wt) + int(cr.cm[(cr.cxt+1)&cr.cmM]>>10)*wt) >> 13)
			cr.cxt += uint(wt >> 5)
		default:
			formatPanic("component predict not implemented")
		}
	}
	return squash(pr.p[pr.n-1])
}

func (pr *predictor) train(cr *component, y int) {
	pn := &cr.cm[cr.cxt&cr.cmM]
	count := uint(*pn & 0x3ff)
	e := y*32767 - int(*pn>>17)
	d := uint32((e * int(sdt[count])) & -1024)
	if count < cr.limit {
		d++
	}
	*pn += d
}

// update trains the model with bit y and runs the HCOMP program at byte ends.
func (pr *predictor) update(y int) {
	c8, hmap4 := pr.c8, pr.hmap4
	for i := 0; i < pr.n; i++ {
		cr := &pr.comp[i]
		switch cr.typ {
		case compConst, compAvg:
		case compCM, compSSE:
			pr.train(cr, y)
		case compICM:
			idx := cr.c + uint(hmap4&15)
			cr.ht[idx] = sns[int(cr.ht[idx])*4+y]
			pn := &cr.cm[cr.cxt&cr.cmM]
			*pn += uint32(int32(uint32(y*32767)-(*pn>>8)) >> 2)
		case compMatch:
			if int(cr.c) != y {
				cr.a = 0
			}
			cr.ht[cr.limit&cr.htM] += cr.ht[cr.limit&cr.htM] + byte(y)
			cr.cxt++
			if cr.cxt == 8 {
				cr.cxt = 0
				cr.limit++
				cr.limit &= (uint(1) << cr.arg[1]) - 1
				if cr.a == 0 {
					cr.b = cr.limit - uint(cr.cm[uint(pr.h[i])&cr.cmM])
					if cr.b&uint(len(cr.ht)-1) != 0 {
						for cr.a < 255 && cr.ht[(cr.limit-cr.a-1)&cr.htM] == cr.ht[(cr.limit-cr.a-cr.b-1)&cr.htM] {
							cr.a++
						}
					}
				} else if cr.a < 255 {
					cr.a++
				}
				cr.cm[uint(pr.h[i])&cr.cmM] = uint32(cr.limit)
			}
		case compMix2:
			e := ((y*32767 - squash(pr.p[i])) * int(cr.arg[3])) >> 5
			w := int(cr.a16[cr.cxt])
			w += (e*(pr.p[cr.arg[1]]-pr.p[cr.arg[2]]) + (1 << 12)) >> 13
			cr.a16[cr.cxt] = uint16(min(max(w, 0), 65535))
		case compMix:
			m := int(cr.arg[2])
			e := ((y*32767 - squash(pr.p[i])) * int(cr.arg[3])) >> 4
			wt := cr.cm[cr.cxt : cr.cxt+uint(m)]
			j := int(cr.arg[1])
			for t := 0; t < m; t++ {
				wt[t] = uint32(int32(clamp512k(int(int32(wt[t])) + ((e*pr.p[j+t] + (1 << 12)) >> 13))))
			}
		case compISSE:
			e := y*32767 - squash(pr.p[i])
			w0, w1 := int(int32(cr.cm[cr.cxt*2])), int(int32(cr.cm[cr.cxt*2+1]))
			cr.cm[cr.cxt*2] = uint32(int32(clamp512k(w0 + ((e*pr.p[cr.arg[1]] + (1 << 12)) >> 13))))
			cr.cm[cr.cxt*2+1] = uint32(int32(clamp512k(w1 + ((e + 16) >> 5))))
			cr.ht[cr.c+uint(hmap4&15)] = sns[int(cr.cxt)*4+y]
		}
	}
	c8 += c8 + uint32(y)
	if c8 >= 256 {
		pr.z.run(c8 - 256)
		pr.hmap4 = 1
		pr.c8 = 1
		for i := 0; i < pr.n; i++ {
			pr.h[i] = pr.z.h[uint32(i)&pr.z.hmask]
		}
	} else {
		pr.c8 = c8
		switch {
		case c8 >= 16 && c8 < 32:
			pr.hmap4 = (hmap4&0xf)<<5 | uint32(y)<<4 | 1
		default:
			pr.hmap4 = hmap4&0x1f0 | ((hmap4&0xf)*2+uint32(y))&0xf
		}
	}
}
