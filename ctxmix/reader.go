package ctxmix

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"sync/atomic"
)

type decResult struct {
	data []byte
	err  error
}

type byteReader interface {
	io.Reader
	io.ByteReader
}

// oneByteReader adapts a plain io.Reader so uvarints can be read without
// consuming bytes past the end of the stream.
type oneByteReader struct {
	r   io.Reader
	buf [1]byte
}

func (o *oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p) }
func (o *oneByteReader) ReadByte() (byte, error) {
	if _, err := io.ReadFull(o.r, o.buf[:]); err != nil {
		return 0, err
	}
	return o.buf[0], nil
}

// A Reader decompresses a stream produced by a Writer.
//
// A Reader reads from the underlying reader only as much as the stream needs
// and never past the end marker, so concatenated streams or trailing data can
// be handled by the caller. It is not safe for concurrent use.
type Reader struct {
	src       byteReader
	cfg       config
	level     Level
	blockSize int
	cur       []byte
	queue     []chan decResult
	sem       chan struct{}
	endSeen   bool
	scheduled int64
	schedErr  error // error hit while reading the stream; reported after queued blocks drain
	err       error
	stop      atomic.Bool
}

// NewReader returns a Reader for the compressed stream in r. It reads and
// validates the stream header immediately. If r is not already an
// io.ByteReader it is not buffered; wrap it in a bufio.Reader for speed.
func NewReader(r io.Reader, opts ...Option) (*Reader, error) {
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}
	br, ok := r.(byteReader)
	if !ok {
		br = &oneByteReader{r: r}
	}
	rd := &Reader{src: br, cfg: cfg, sem: make(chan struct{}, cfg.workers)}
	var magic [4]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return nil, headerErr(err)
	}
	if string(magic[:3]) != frameMagic[:3] {
		return nil, fmt.Errorf("%w: not a ctxmix stream", ErrCorrupt)
	}
	if magic[3] != frameMagic[3] {
		return nil, fmt.Errorf("ctxmix: unsupported format version %d", magic[3])
	}
	lv, err := br.ReadByte()
	if err != nil {
		return nil, headerErr(err)
	}
	rd.level = Level(lv)
	if !rd.level.valid() {
		return nil, fmt.Errorf("%w: invalid level %d", ErrCorrupt, lv)
	}
	bs, err := readUvarint(br)
	if err != nil {
		return nil, headerErr(err)
	}
	if bs == 0 || bs > uint64(MaxBlockSize) {
		return nil, fmt.Errorf("%w: invalid block size %d", ErrCorrupt, bs)
	}
	if bs > uint64(cfg.maxBlock) {
		return nil, fmt.Errorf("ctxmix: stream block size %d exceeds the configured limit %d", bs, cfg.maxBlock)
	}
	rd.blockSize = int(bs)
	return rd, nil
}

// readUvarint is binary.ReadUvarint, except that a malformed varint is
// reported as ErrCorrupt instead of an error from another package.
func readUvarint(r io.ByteReader) (uint64, error) {
	var x uint64
	var shift uint
	for i := 0; i < binary.MaxVarintLen64; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b < 0x80 {
			if i == binary.MaxVarintLen64-1 && b > 1 {
				break
			}
			return x | uint64(b)<<shift, nil
		}
		x |= uint64(b&0x7f) << shift
		shift += 7
	}
	return 0, fmt.Errorf("%w: varint overflow", ErrCorrupt)
}

func headerErr(err error) error {
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// Close stops background decoding. It does not close the underlying reader.
func (r *Reader) Close() error {
	r.stop.Store(true)
	return nil
}

func (r *Reader) cancelled() bool { return r.stop.Load() || r.cfg.canceled() }

// schedule reads the next block from the stream and starts decoding it.
func (r *Reader) schedule() error {
	kind, err := r.src.ReadByte()
	if err != nil {
		return headerErr(err)
	}
	if kind == kindEnd {
		total, err := readUvarint(r.src)
		if err != nil {
			return headerErr(err)
		}
		if total != uint64(r.scheduled) {
			return fmt.Errorf("%w: length mismatch (stream says %d bytes, blocks hold %d)", ErrCorrupt, total, r.scheduled)
		}
		r.endSeen = true
		return nil
	}
	if kind != kindStored && kind != kindComp {
		return fmt.Errorf("%w: unknown block kind %d", ErrCorrupt, kind)
	}
	rawLen, err := readUvarint(r.src)
	if err != nil {
		return headerErr(err)
	}
	compLen, err := readUvarint(r.src)
	if err != nil {
		return headerErr(err)
	}
	if rawLen == 0 || rawLen > uint64(r.blockSize) || compLen == 0 || compLen > rawLen || (kind == kindStored && compLen != rawLen) {
		return fmt.Errorf("%w: invalid block lengths", ErrCorrupt)
	}
	var crcb [4]byte
	if _, err := io.ReadFull(r.src, crcb[:]); err != nil {
		return headerErr(err)
	}
	want := binary.LittleEndian.Uint32(crcb[:])
	payload := make([]byte, compLen)
	if _, err := io.ReadFull(r.src, payload); err != nil {
		return headerErr(err)
	}
	r.scheduled += int64(rawLen)
	ch := make(chan decResult, 1)
	r.queue = append(r.queue, ch)
	r.sem <- struct{}{}
	level, tick := r.level, r.cfg.progress
	go func() {
		defer func() { <-r.sem }()
		var data []byte
		var err error
		if kind == kindStored {
			data = payload
		} else {
			data, err = decodeBlock(payload, int(rawLen), level, r.cancelled, tick)
			if err == errCanceled {
				if cerr := r.cfg.ctx.Err(); cerr != nil {
					err = cerr
				} else {
					err = io.ErrClosedPipe
				}
			}
		}
		if err == nil && crc32.ChecksumIEEE(data) != want {
			err = fmt.Errorf("%w: block checksum mismatch", ErrCorrupt)
		}
		ch <- decResult{data: data, err: err}
	}()
	return nil
}

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if err := r.cfg.ctx.Err(); err != nil {
			r.err = err
			return 0, err
		}
		for r.schedErr == nil && !r.endSeen && len(r.queue) <= r.cfg.workers {
			r.schedErr = r.schedule()
		}
		if len(r.queue) == 0 {
			if r.schedErr != nil {
				r.err = r.schedErr
			} else {
				r.err = io.EOF
			}
			return 0, r.err
		}
		ch := r.queue[0]
		r.queue = r.queue[1:]
		res := <-ch
		if res.err != nil {
			r.err = res.err
			return 0, res.err
		}
		r.cur = res.data
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

var _ io.Reader = (*Reader)(nil)
