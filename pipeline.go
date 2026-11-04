package zpaq

import (
	"io"
	"sync"
)

// pipeline cuts its input into blocks, compresses up to c.concurrency of them
// at a time and hands the finished blocks, in order, to emit. It is used by
// Compress and Writer.
type pipeline struct {
	c    *config
	emit func([]byte) error

	buf   []byte // current partial block
	first bool   // next block is the first one

	sem     chan struct{}
	pending chan *job
	done    chan struct{} // closed when the emitting goroutine has finished

	mu       sync.Mutex
	err      error
	closed   bool
	progress func(int)
	canceled func() bool
	nblocks  int
}

type job struct {
	data  []byte
	first bool
	out   []byte
	err   error
	ready chan struct{}
}

func newPipeline(c *config, emit func([]byte) error) *pipeline {
	p := &pipeline{
		c:       c,
		emit:    emit,
		first:   true,
		sem:     make(chan struct{}, c.concurrency),
		pending: make(chan *job, c.concurrency),
		done:    make(chan struct{}),
	}
	if c.progress != nil {
		var pmu sync.Mutex
		p.progress = func(n int) {
			pmu.Lock()
			c.progress(n)
			pmu.Unlock()
		}
	}
	ctx := c.ctx
	p.canceled = func() bool { return ctx.Err() != nil }
	go p.emitter()
	return p
}

func (p *pipeline) setErr(err error) {
	p.mu.Lock()
	if p.err == nil {
		p.err = err
	}
	p.mu.Unlock()
}

func (p *pipeline) getErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// emitter writes finished blocks in order.
func (p *pipeline) emitter() {
	defer close(p.done)
	for j := range p.pending {
		<-j.ready
		if p.getErr() != nil {
			continue
		}
		if j.err != nil {
			p.setErr(j.err)
			continue
		}
		if err := p.emit(j.out); err != nil {
			p.setErr(err)
		}
	}
}

func (p *pipeline) submit(data []byte) error {
	if err := p.getErr(); err != nil {
		return err
	}
	if err := p.c.ctx.Err(); err != nil {
		p.setErr(err)
		return err
	}
	j := &job{data: data, first: p.first, ready: make(chan struct{})}
	p.first = false
	p.nblocks++
	select {
	case p.sem <- struct{}{}:
	case <-p.c.ctx.Done():
		p.setErr(p.c.ctx.Err())
		return p.c.ctx.Err()
	}
	p.pending <- j
	go func() {
		j.out, j.err = encodeBlock(j.data, p.c, j.first, p.progress, p.canceled)
		if j.err == errCanceled {
			j.err = p.c.ctx.Err()
		}
		j.data = nil
		<-p.sem
		close(j.ready)
	}()
	return nil
}

// write buffers src, submitting every full block.
func (p *pipeline) write(src []byte) error {
	bs := p.c.blockSize
	for len(src) > 0 {
		if len(p.buf) == 0 && len(src) >= bs {
			// Whole block available without copying; the caller keeps
			// ownership of src, so it must be copied for asynchronous use.
			blk := make([]byte, bs)
			copy(blk, src)
			src = src[bs:]
			if err := p.submit(blk); err != nil {
				return err
			}
			continue
		}
		n := min(bs-len(p.buf), len(src))
		p.buf = append(p.buf, src[:n]...)
		src = src[n:]
		if len(p.buf) == bs {
			blk := p.buf
			p.buf = nil
			if err := p.submit(blk); err != nil {
				return err
			}
		}
	}
	return p.getErr()
}

// close submits the last partial block, waits for all blocks to be written
// and returns the first error.
func (p *pipeline) close() error {
	if p.closed {
		return p.getErr()
	}
	p.closed = true
	if len(p.buf) > 0 || p.nblocks == 0 {
		blk := p.buf
		p.buf = nil
		if blk == nil {
			blk = []byte{}
		}
		p.submit(blk) // an error is recorded in p.err
	}
	close(p.pending)
	<-p.done
	return p.getErr()
}

// abort stops accepting data and releases the goroutines.
func (p *pipeline) abort() {
	p.setErr(io.ErrClosedPipe)
	p.close()
}

// Writer compresses data written to it into a ZPAQ streaming archive.
type Writer struct {
	p   *pipeline
	err error
}

// NewWriter returns a Writer that writes to w a ZPAQ archive holding the data
// written to it. The data is cut into blocks of at most the block size, which
// are compressed concurrently. Close must be called to flush the last block;
// it does not close w.
func NewWriter(w io.Writer, opts ...Option) *Writer {
	c := newConfig(opts)
	return &Writer{p: newPipeline(&c, func(b []byte) error { _, err := w.Write(b); return err })}
}

// Write implements io.Writer.
func (w *Writer) Write(b []byte) (int, error) {
	if w.p.closed {
		return 0, io.ErrClosedPipe
	}
	if err := w.p.write(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Close flushes the remaining data and waits for the compression to finish.
func (w *Writer) Close() error { return w.p.close() }
