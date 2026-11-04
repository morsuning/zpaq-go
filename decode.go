package zpaq

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// recSource reads bytes from br and records them.
type recSource struct {
	br  *bufio.Reader
	buf []byte
	err error // first non-EOF read error
}

func (s *recSource) get() int {
	b, err := s.br.ReadByte()
	if err != nil {
		if err != io.EOF && s.err == nil {
			s.err = err
		}
		return -1
	}
	s.buf = append(s.buf, b)
	return int(b)
}

// scanTag reads input up to and including the next block tag. It returns
// false (and a nil error) at the end of the input. Unless skipGarbage is set,
// any byte that is not part of the tag is an error.
func scanTag(br *bufio.Reader, skipGarbage bool) (bool, error) {
	var win [16]byte
	n := 0 // bytes read so far, saturating
	for {
		b, err := br.ReadByte()
		if err != nil {
			if err == io.EOF {
				if n > 0 && !skipGarbage {
					return false, fmt.Errorf("%w: truncated block tag at end of input", ErrCorrupt)
				}
				return false, nil
			}
			return false, err
		}
		if !skipGarbage && b != blockTag[min(n, 15)] {
			return false, fmt.Errorf("%w: unexpected data where a block was expected", ErrCorrupt)
		}
		copy(win[:], win[1:])
		win[15] = b
		if n < 16 {
			n++
		}
		if n == 16 && win == blockTag {
			return true, nil
		}
	}
}

// nextBlock reads the raw bytes of the next block, from its tag to its
// end-of-block byte, without decoding them. It returns io.EOF when there
// are no more blocks.
func nextBlock(br *bufio.Reader, skipGarbage bool) (raw []byte, err error) {
	ok, err := scanTag(br, skipGarbage)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, io.EOF
	}
	src := &recSource{br: br, buf: append(make([]byte, 0, 1<<16), blockTag[:]...)}
	func() {
		defer recoverFormat(&err)
		src.buf = src.buf[:0] // the tag is not part of the recorded block
		z := readBlockHeader(src)
		modeled := z.header[6] > 0
		segs := 0
		for {
			switch src.get() {
			case 255:
				if segs == 0 {
					formatPanic("block has no segments")
				}
				return
			case 1:
				segs++
				readCString(src) // name
				readCString(src) // comment
				if src.get() != 0 {
					formatPanic("missing reserved byte")
				}
				switch skipSegment(src, modeled) {
				case 254:
				case 253:
					for i := 0; i < 20; i++ {
						if src.get() < 0 {
							formatPanic("unexpected end of file")
						}
					}
				default:
					formatPanic("missing end of segment marker")
				}
			default:
				formatPanic("missing segment or end of block")
			}
		}
	}()
	if src.err != nil {
		return nil, src.err
	}
	if err != nil {
		return nil, err
	}
	return src.buf, nil
}

// decodeRaw decodes a block recorded by nextBlock.
func decodeRaw(raw []byte, o *blockOpts, emit func(name, comment string, data []byte) error) (err error) {
	defer recoverFormat(&err)
	src := &sliceSource{b: raw}
	z := readBlockHeader(src)
	return decodeBlock(z, src, o, emit)
}

func (c *config) blockOpts(progress func(int)) *blockOpts {
	ctx := c.ctx
	o := &blockOpts{
		maxOutput: c.maxOutput,
		progress:  progress,
		canceled:  func() bool { return ctx.Err() != nil },
	}
	if c.maxMemory > 0 {
		o.maxMemory = float64(c.maxMemory)
	}
	return o
}

// Walk calls fn for every segment of the ZPAQ archive read from r, in order,
// with the segment's file name (empty except for the first segment of each
// file), comment and decompressed contents; data is only valid during the
// call. Walk handles archives written by this package and zpaq streaming
// archives (zpaq "-method s..."); journaling archives, whose blocks hold
// internal index and fragment data, are not interpreted: their segments are
// delivered like any other. The input may contain data before the first block
// and between blocks, which is skipped.
func Walk(r io.Reader, fn func(name, comment string, data []byte) error, opts ...Option) error {
	c := newConfig(opts)
	br := bufio.NewReaderSize(r, 1<<16)
	o := c.blockOpts(c.progress)
	blocks := 0
	var total int64
	for {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		raw, err := nextBlock(br, c.skipGarbage)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		blocks++
		bo := *o
		if c.maxOutput > 0 {
			bo.maxOutput = c.maxOutput - total
		}
		err = decodeRaw(raw, &bo, func(name, comment string, data []byte) error {
			total += int64(len(data))
			return fn(name, comment, data)
		})
		if err != nil {
			return err
		}
	}
	if blocks == 0 {
		return fmt.Errorf("%w: no ZPAQ block found", ErrCorrupt)
	}
	return nil
}

// Decompress appends to dst the concatenated contents of all segments of the
// ZPAQ archive in src and returns the extended slice. For an archive written
// by Compress this is the original data. Use WithMaxOutput when src is
// untrusted.
func Decompress(dst, src []byte, opts ...Option) ([]byte, error) {
	r := NewReader(bytes.NewReader(src), opts...)
	defer r.Close()
	var buf bytes.Buffer
	if _, err := r.WriteTo(&buf); err != nil {
		return dst, err
	}
	if dst == nil {
		return buf.Bytes(), nil
	}
	return append(dst, buf.Bytes()...), nil
}

// Reader decompresses a ZPAQ archive, returning the concatenated contents of
// its segments. Blocks are read ahead and decoded concurrently.
type Reader struct {
	c       config
	parent  context.Context
	closed  bool
	cancel  context.CancelFunc
	results chan *rjob
	queue   [][]byte
	cur     []byte
	total   int64
	err     error
}

type rjob struct {
	raw   []byte
	out   [][]byte
	err   error
	ready chan struct{}
}

// NewReader returns a Reader that decompresses the archive read from r.
// Close it to stop its goroutines if it is not read to the end.
func NewReader(r io.Reader, opts ...Option) *Reader {
	c := newConfig(opts)
	ctx, cancel := context.WithCancel(c.ctx)
	parent := c.ctx
	c.ctx = ctx
	rd := &Reader{c: c, parent: parent, cancel: cancel, results: make(chan *rjob, c.concurrency)}
	go rd.produce(bufio.NewReaderSize(r, 1<<16))
	return rd
}

func (r *Reader) produce(br *bufio.Reader) {
	defer close(r.results)
	ctx := r.c.ctx
	sem := make(chan struct{}, r.c.concurrency)
	var progMu sync.Mutex
	var progress func(int)
	if r.c.progress != nil {
		progress = func(n int) {
			progMu.Lock()
			r.c.progress(n)
			progMu.Unlock()
		}
	}
	fail := func(err error) {
		j := &rjob{err: err, ready: make(chan struct{})}
		close(j.ready)
		select {
		case r.results <- j:
		case <-ctx.Done():
		}
	}
	blocks := 0
	for {
		raw, err := nextBlock(br, r.c.skipGarbage)
		if err == io.EOF {
			if blocks == 0 {
				fail(fmt.Errorf("%w: no ZPAQ block found", ErrCorrupt))
			}
			return
		}
		if err != nil {
			fail(err)
			return
		}
		blocks++
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			fail(ctx.Err())
			return
		}
		j := &rjob{raw: raw, ready: make(chan struct{})}
		select {
		case r.results <- j:
		case <-ctx.Done():
			<-sem
			return
		}
		go func() {
			o := r.c.blockOpts(progress)
			j.err = decodeRaw(j.raw, o, func(_, _ string, data []byte) error {
				j.out = append(j.out, append([]byte(nil), data...))
				return nil
			})
			if j.err == errCanceled {
				j.err = ctx.Err()
			}
			j.raw = nil
			<-sem
			close(j.ready)
		}()
	}
}

// fill makes r.cur non-empty, or sets r.err.
func (r *Reader) fill() {
	for len(r.cur) == 0 {
		if len(r.queue) > 0 {
			r.cur, r.queue = r.queue[0], r.queue[1:]
			continue
		}
		if r.err != nil {
			return
		}
		j, ok := <-r.results
		if !ok {
			r.err = io.EOF
			if r.closed {
				r.err = io.ErrClosedPipe
			} else if err := r.parent.Err(); err != nil {
				r.err = err
			}
			return
		}
		<-j.ready
		if j.err != nil {
			r.err = j.err
			r.cancel()
			return
		}
		for _, d := range j.out {
			r.total += int64(len(d))
		}
		if r.c.maxOutput > 0 && r.total > r.c.maxOutput {
			r.err = ErrTooLarge
			r.cancel()
			return
		}
		r.queue = j.out
	}
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.fill(); len(r.cur) == 0 {
		return 0, r.err
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

// WriteTo implements io.WriterTo.
func (r *Reader) WriteTo(w io.Writer) (int64, error) {
	var n int64
	for {
		if r.fill(); len(r.cur) == 0 {
			if r.err == io.EOF {
				return n, nil
			}
			return n, r.err
		}
		m, err := w.Write(r.cur)
		n += int64(m)
		r.cur = r.cur[m:]
		if err != nil {
			return n, err
		}
	}
}

// Close stops the Reader's goroutines. It does not close the underlying
// reader.
func (r *Reader) Close() error {
	r.closed = true
	r.cancel()
	return nil
}
