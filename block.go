package zpaq

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"runtime"
)

var (
	// ErrCorrupt is returned (wrapped) when an archive is malformed or its
	// checksum does not match.
	ErrCorrupt = errors.New("zpaq: corrupt archive")
	// ErrTooLarge is returned when decompressed output would exceed the limit
	// set with WithMaxOutput.
	ErrTooLarge = errors.New("zpaq: decompressed data exceeds the configured limit")
	// ErrMemory is returned (wrapped) when a block needs more memory than the
	// limit set with WithMaxMemory.
	ErrMemory = errors.New("zpaq: block needs more memory than the configured limit")

	errCanceled = errors.New("zpaq: canceled")
)

// blockTag is the 16-byte string that starts every ZPAQ block: a 13-byte
// locator tag followed by "zPQ".
var blockTag = [16]byte{0x37, 0x6b, 0x53, 0x74, 0xa0, 0x31, 0x83, 0xd3, 0x8c, 0xb2, 0x28, 0xb0, 0xd3, 'z', 'P', 'Q'}

type limitPanic struct{}

// A ZPAQL program may execute at most stepsBase + stepsPerCall*calls
// instructions in total, where calls is the number of times it has been run
// (once per byte). This keeps a malicious program from hanging the decoder.
const (
	stepsBase    = 1 << 24
	stepsPerCall = 1 << 12
)

// recoverFormat converts a panic raised inside the VM, predictor or coder for
// invalid input into an error.
func recoverFormat(err *error) {
	switch r := recover().(type) {
	case nil:
	case errFormat:
		*err = fmt.Errorf("%w: %s", ErrCorrupt, r.msg)
	case limitPanic:
		*err = ErrTooLarge
	case runtime.Error:
		// An out-of-range access caused by a malformed ZPAQL program.
		*err = fmt.Errorf("%w: %v", ErrCorrupt, r)
	default:
		panic(r)
	}
}

// blockOpts carries the per-call limits and hooks of block decoding.
type blockOpts struct {
	maxMemory float64 // bytes; 0 = unlimited
	maxOutput int64   // bytes of decompressed data per call; 0 = unlimited
	progress  func(n int)
	canceled  func() bool
}

// readBlockHeader parses a block header that follows the 16-byte tag: the
// level byte, the type byte and the COMP/HCOMP header.
func readBlockHeader(src byteSource) *zpaql {
	level := src.get()
	if level != 1 && level != 2 {
		formatPanic("unsupported ZPAQ level")
	}
	if src.get() != 1 {
		formatPanic("unsupported ZPAQL type")
	}
	z := new(zpaql)
	z.readHeader(src.get)
	if level == 1 && z.header[6] == 0 {
		formatPanic("ZPAQ level 1 requires at least 1 component")
	}
	return z
}

// decodeBlock decodes the segments of one block whose header z has already
// been read from src, calling emit for each with its name, comment and
// decompressed contents. It returns after the end-of-block byte.
func decodeBlock(z *zpaql, src byteSource, o *blockOpts, emit func(name, comment string, data []byte) error) (err error) {
	defer recoverFormat(&err)
	if o.maxMemory > 0 && z.memory() > o.maxMemory {
		return fmt.Errorf("%w (needs about %.0f MiB)", ErrMemory, z.memory()/(1<<20))
	}
	var pr *predictor
	if z.header[6] > 0 {
		pr = newPredictor(z)
	}
	dec := newDecoder(pr, src)
	var out bytes.Buffer
	post := &postProcessor{ph: int(z.header[4]), pm: int(z.header[5]), out: &out}
	var total int64
	segs := 0
	for {
		c := src.get()
		switch {
		case c == 255:
			if segs == 0 {
				formatPanic("block has no segments")
			}
			return nil
		case c != 1:
			formatPanic("missing segment or end of block")
		}
		segs++
		name := readCString(src)
		comment := readCString(src)
		if src.get() != 0 {
			formatPanic("missing reserved byte")
		}
		out.Reset()
		if pr != nil {
			dec.low, dec.high = 1, 0xFFFFFFFF
		}
		dec.curr = 0
		last := 0
		for n := 0; ; n++ {
			if n&0xffff == 0xffff {
				if o.canceled != nil && o.canceled() {
					return errCanceled
				}
				if l := out.Len(); o.progress != nil && l > last {
					o.progress(l - last)
					last = l
				}
			}
			ch := dec.decompress()
			post.write(ch)
			if ch < 0 {
				break
			}
			if o.maxOutput > 0 && total+int64(out.Len()) > o.maxOutput {
				panic(limitPanic{})
			}
		}
		if o.progress != nil && out.Len() > last {
			o.progress(out.Len() - last)
		}
		total += int64(out.Len())
		var sum [20]byte
		marker := src.get()
		switch marker {
		case 254:
		case 253:
			for i := range sum {
				v := src.get()
				if v < 0 {
					formatPanic("unexpected end of file")
				}
				sum[i] = byte(v)
			}
			if sha1.Sum(out.Bytes()) != sum {
				return fmt.Errorf("%w: SHA-1 checksum mismatch", ErrCorrupt)
			}
		default:
			formatPanic("missing end of segment marker")
		}
		if err := emit(name, comment, out.Bytes()); err != nil {
			return err
		}
	}
}

func readCString(src byteSource) string {
	var b []byte
	for {
		c := src.get()
		if c < 0 {
			formatPanic("unexpected end of file")
		}
		if c == 0 {
			return string(b)
		}
		if len(b) >= 1<<16 {
			formatPanic("segment name or comment too long")
		}
		b = append(b, byte(c))
	}
}

// postProcessor undoes the optional PCOMP transform of a block's segments.
// Like libzpaq's, it is initialized once per block: only the first segment
// carries the PCOMP program, later segments continue in the same state.
type postProcessor struct {
	ph, pm int
	state  int // 0=initial 1=pass 2..4=loading program 5=program loaded
	hsize  int
	z      *zpaql
	out    *bytes.Buffer
}

func (p *postProcessor) write(c int) {
	switch p.state {
	case 0:
		if c < 0 {
			formatPanic("unexpected end of segment")
		}
		p.state = c + 1
		if p.state > 2 {
			formatPanic("unknown post-processing type")
		}
	case 1:
		if c >= 0 {
			p.out.WriteByte(byte(c))
		}
	case 2:
		if c < 0 {
			formatPanic("unexpected end of segment")
		}
		p.hsize = c
		p.state = 3
	case 3:
		if c < 0 {
			formatPanic("unexpected end of segment")
		}
		p.hsize += c * 256
		if p.hsize < 1 {
			formatPanic("empty PCOMP")
		}
		p.z = &zpaql{header: make([]byte, p.hsize+300)}
		p.z.cend = 8
		p.z.hbegin = p.z.cend + 128
		p.z.hend = p.z.hbegin
		p.z.header[4], p.z.header[5] = byte(p.ph), byte(p.pm)
		p.z.out = func(c int) {
			if c >= 0 {
				p.out.WriteByte(byte(c))
			}
		}
		p.state = 4
	case 4:
		if c < 0 {
			formatPanic("unexpected end of segment")
		}
		p.z.header[p.z.hend] = byte(c)
		p.z.hend++
		if p.z.hend-p.z.hbegin == p.hsize {
			hs := p.z.cend - 2 + p.z.hend - p.z.hbegin
			p.z.header[0], p.z.header[1] = byte(hs), byte(hs>>8)
			p.z.initp()
			p.state = 5
		}
	case 5:
		p.z.run(uint32(c)) // c == -1 runs with A = 0xFFFFFFFF
	}
}
