package zpaq

import (
	"errors"
	"fmt"
)

// Component types of the ZPAQ level 2 format.
const (
	compNone  = 0
	compConst = 1
	compCM    = 2
	compICM   = 3
	compMatch = 4
	compAvg   = 5
	compMix2  = 6
	compMix   = 7
	compISSE  = 8
	compSSE   = 9
)

// compSize is the number of header bytes of each component type, including
// the type byte.
var compSize = [256]int{0, 2, 3, 2, 3, 4, 6, 6, 3, 5}

// errFormat is the panic value used inside the VM and predictor to unwind on
// invalid input; the public entry points recover it and return an error
// wrapping ErrCorrupt.
type errFormat struct{ msg string }

func (e errFormat) Error() string { return e.msg }

func formatPanic(format string, args ...any) {
	panic(errFormat{fmt.Sprintf(format, args...)})
}

// zpaql is a ZPAQL virtual machine together with the program (header) it runs.
//
// header holds, as in libzpaq: the 2-byte size, hh hm ph pm n, the component
// list and its END byte (up to cend), then 128 unused bytes, then the program
// bytes in [hbegin, hend) ending with a 0 END byte.
type zpaql struct {
	header []byte
	cend   int
	hbegin int
	hend   int

	h     []uint32
	hmask uint32
	m     []byte
	mmask uint32
	r     [256]uint32
	a, b  uint32
	c, d  uint32
	f     bool
	pc    int

	steps uint64 // ZPAQL instructions executed so far
	calls uint64 // calls of run so far

	out func(c int) // receives OUT bytes and -1 at end of segment; may be nil
}

func (z *zpaql) fail() { formatPanic("ZPAQL execution error") }

func (z *zpaql) div(x uint32) {
	if x != 0 {
		z.a /= x
	} else {
		z.a = 0
	}
}

func (z *zpaql) mod(x uint32) {
	if x != 0 {
		z.a %= x
	} else {
		z.a = 0
	}
}

func (z *zpaql) outc(c int) {
	if z.out != nil {
		z.out(c)
	}
}

// readHeader parses a COMP/HCOMP header (starting at the 2-byte size) from get,
// which returns the next byte or -1 at EOF.
func (z *zpaql) readHeader(get func() int) {
	hsize := get()
	hsize += get() * 256
	if hsize < 0 {
		formatPanic("unexpected end of file")
	}
	z.header = make([]byte, hsize+300)
	z.cend, z.hbegin, z.hend = 0, 0, 0
	z.header[z.cend] = byte(hsize)
	z.cend++
	z.header[z.cend] = byte(hsize >> 8)
	z.cend++
	for z.cend < 7 {
		c := get()
		if c < 0 {
			formatPanic("unexpected end of file")
		}
		z.header[z.cend] = byte(c)
		z.cend++
	}
	n := int(z.header[z.cend-1])
	for i := 0; i < n; i++ {
		typ := get()
		if typ < 0 || typ > 255 {
			formatPanic("unexpected end of file")
		}
		z.header[z.cend] = byte(typ)
		z.cend++
		size := compSize[typ]
		if size < 1 {
			formatPanic("invalid component type")
		}
		if z.cend+size > hsize {
			formatPanic("COMP overflows header")
		}
		for j := 1; j < size; j++ {
			c := get()
			if c < 0 {
				formatPanic("unexpected end of file")
			}
			z.header[z.cend] = byte(c)
			z.cend++
		}
	}
	if c := get(); c != 0 {
		formatPanic("missing COMP END")
	}
	z.cend++
	z.hbegin = z.cend + 128
	z.hend = z.hbegin
	if z.hend > hsize+129 {
		formatPanic("missing HCOMP")
	}
	for z.hend < hsize+129 {
		op := get()
		if op < 0 {
			formatPanic("unexpected end of file")
		}
		z.header[z.hend] = byte(op)
		z.hend++
	}
	if c := get(); c != 0 {
		formatPanic("missing HCOMP END")
	}
	z.hend++
}

// writeHeader appends the COMP section (unless pp) and the program bytes.
func (z *zpaql) writeHeader(dst []byte, pp bool) []byte {
	if !pp {
		dst = append(dst, z.header[:z.cend]...)
	} else {
		n := z.hend - z.hbegin
		dst = append(dst, byte(n), byte(n>>8))
	}
	return append(dst, z.header[z.hbegin:z.hend]...)
}

// memory estimates the bytes needed by the model described by the header.
func (z *zpaql) memory() float64 {
	pow2 := func(x int) float64 {
		r := 1.0
		for ; x > 0; x-- {
			r += r
		}
		return r
	}
	mem := pow2(int(z.header[2])+2) + pow2(int(z.header[3])) + pow2(int(z.header[4])+2) + pow2(int(z.header[5])) + float64(len(z.header))
	cp := 7
	for i := 0; i < int(z.header[6]); i++ {
		size := pow2(int(z.header[cp+1]))
		switch z.header[cp] {
		case compCM:
			mem += 4 * size
		case compICM:
			mem += 64*size + 1024
		case compMatch:
			mem += 4*size + pow2(int(z.header[cp+2]))
		case compMix2:
			mem += 2 * size
		case compMix:
			mem += 4 * size * float64(z.header[cp+3])
		case compISSE:
			mem += 64*size + 2048
		case compSSE:
			mem += 128 * size
		}
		cp += compSize[z.header[cp]]
	}
	return mem
}

// init allocates H and M (2^hbits and 2^mbits elements) and clears the registers.
func (z *zpaql) init(hbits, mbits int) {
	if hbits > 32 {
		formatPanic("H too big")
	}
	if mbits > 32 {
		formatPanic("M too big")
	}
	z.h = make([]uint32, 1<<uint(hbits))
	z.hmask = uint32(len(z.h) - 1)
	z.m = make([]byte, 1<<uint(mbits))
	z.mmask = uint32(len(z.m) - 1)
	z.r = [256]uint32{}
	z.a, z.b, z.c, z.d, z.pc, z.f = 0, 0, 0, 0, 0, false
	z.steps, z.calls = 0, 0
}

func (z *zpaql) inith() { z.init(int(z.header[2]), int(z.header[3])) }
func (z *zpaql) initp() { z.init(int(z.header[4]), int(z.header[5])) }

// run executes the program with A=input until it halts.
//
// The total number of instructions is limited to stepsBase plus stepsPerCall
// for every call so far, so that a malicious program cannot hang the decoder
// while a legitimate one may still spend O(n) steps in a single call, as the
// inverse BWT of a post-processor does at the end of a block.
func (z *zpaql) run(input uint32) {
	z.pc = z.hbegin
	z.a = input
	z.calls++
	budget := stepsBase + stepsPerCall*z.calls
	if z.steps >= budget {
		formatPanic("ZPAQL program exceeds its step budget")
	}
	left := budget - z.steps
	var n uint64
	for z.step() {
		n++
		if n > left {
			formatPanic("ZPAQL program exceeds its step budget")
		}
	}
	z.steps += n
}

var errUnsupported = errors.New("zpaq: unsupported feature")
