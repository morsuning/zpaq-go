package zpaq

import (
	"context"
	"runtime"
	"strings"
	"time"
)

// Level selects the compression model. Higher levels compress better and are
// slower and use more memory.
//
//	0 Store    no compression (stored chunks).
//	1 Fast     libzpaq's built-in "min" model (order 1-2 context mixing).
//	2          libzpaq's built-in "mid" model.
//	3          libzpaq's built-in "max" model.
//	4 Default  context mixing like "zpaq -m4" (orders 1-4 + match model,
//	           plus a word model for text); memory scales with the block size.
//	5 Best     context mixing like "zpaq -m5": orders 1-5, word, sparse and
//	           periodic models, match model and a 2-layer mixer plus SSE.
//
// Levels 4 and 5 generate the same models that the zpaq command line program
// uses for its context-mixing methods, but never use its LZ77, BWT or E8E9
// pre-processing steps, so output sizes differ from "zpaq -m4/-m5" for data
// where those would have been chosen. Every level is decodable by any
// ZPAQ-compatible decompressor.
type Level int

// Named levels.
const (
	Store   Level = 0
	Fast    Level = 1
	Default Level = 4
	Best    Level = 5
)

// DefaultBlockSize is the default maximum number of input bytes per block.
// Each block is compressed independently (and in parallel); a larger block
// compresses slightly better but needs more memory at levels 4 and 5.
const DefaultBlockSize = 1<<24 - 4096

// maxBlockSize is the largest block the ZPAQ models can address (2^31-4096).
const maxBlockSize = 1<<31 - 4096

// DefaultMaxMemory is the default limit on the memory a single block may need
// when decompressing (see WithMaxMemory).
const DefaultMaxMemory = 4 << 30

type config struct {
	level       Level
	blockSize   int
	concurrency int
	name        string
	comment     string
	modTime     time.Time
	checksum    bool
	skipGarbage bool
	ctx         context.Context
	progress    func(n int)
	maxMemory   int64
	maxOutput   int64
}

func newConfig(opts []Option) config {
	c := config{
		level:       Default,
		blockSize:   DefaultBlockSize,
		concurrency: min(runtime.GOMAXPROCS(0), 4),
		checksum:    true,
		maxMemory:   DefaultMaxMemory,
	}
	for _, o := range opts {
		o(&c)
	}
	if c.ctx == nil {
		c.ctx = context.Background()
	}
	// Names and comments are NUL-terminated in the archive.
	c.name = strings.ReplaceAll(c.name, "\x00", "")
	c.comment = strings.ReplaceAll(c.comment, "\x00", "")
	if c.blockSize <= 0 {
		c.blockSize = DefaultBlockSize
	}
	c.blockSize = min(c.blockSize, maxBlockSize)
	if c.concurrency <= 0 {
		c.concurrency = min(runtime.GOMAXPROCS(0), 4)
	}
	return c
}

// An Option configures Compress, Decompress, NewWriter, NewReader and Walk.
type Option func(*config)

// WithLevel sets the compression level (default Default). Levels outside 0-5
// are clamped.
func WithLevel(l Level) Option { return func(c *config) { c.level = min(max(l, 0), 5) } }

// WithBlockSize sets the maximum number of input bytes per block (default
// DefaultBlockSize, also used for values <= 0; maximum 2 GiB-4096). Compression of levels 4 and 5 uses
// about 2^(20+k) bytes of memory per concurrent block where 2^(20+k) is the
// smallest such power of two not less than the block size.
func WithBlockSize(n int) Option { return func(c *config) { c.blockSize = n } }

// WithConcurrency sets how many blocks are compressed or decompressed in
// parallel (default min(GOMAXPROCS, 4), also used for values <= 0). The output does not depend on it.
func WithConcurrency(n int) Option { return func(c *config) { c.concurrency = n } }

// WithName sets the file name stored in the first block, which "zpaq x" uses
// as the name of the extracted file. Without it the name is empty and zpaq
// names the output after the archive.
func WithName(name string) Option { return func(c *config) { c.name = name } }

// WithComment sets a comment stored in the first block after the size, for
// example a modification time in zpaq's format (see WithModTime).
func WithComment(s string) Option { return func(c *config) { c.comment = s } }

// WithModTime stores t as the comment of the first block in zpaq's streaming
// format, a decimal YYYYMMDDHHMMSS in UTC.
func WithModTime(t time.Time) Option {
	return func(c *config) {
		if t.IsZero() {
			c.modTime = time.Time{}
			return
		}
		c.modTime = t.UTC()
	}
}

// WithChecksum controls whether a SHA-1 of each block's data is stored and
// verified (default true; zpaq verifies it on extraction).
func WithChecksum(on bool) Option { return func(c *config) { c.checksum = on } }

// WithContext makes compression or decompression stop with ctx.Err() soon after
// ctx is canceled.
func WithContext(ctx context.Context) Option { return func(c *config) { c.ctx = ctx } }

// WithProgress registers a function called with the number of uncompressed
// bytes processed since its previous call. Calls are serialized.
func WithProgress(fn func(n int)) Option { return func(c *config) { c.progress = fn } }

// WithMaxMemory limits the memory a single block may require when
// decompressing; blocks needing more fail with ErrMemory (default
// DefaultMaxMemory; 0 or negative disables the limit). A malicious archive can
// otherwise ask for tens of gigabytes.
func WithMaxMemory(bytes int64) Option { return func(c *config) { c.maxMemory = bytes } }

// WithMaxOutput limits the total decompressed size; Decompress and Reader fail
// with ErrTooLarge beyond it (default unlimited). Use it when decompressing
// untrusted data, which can expand by a factor of thousands.
func WithMaxOutput(bytes int64) Option { return func(c *config) { c.maxOutput = bytes } }

// WithSkipGarbage makes the decoders skip any data that is not part of a block
// (before the first block, between blocks and after the last one), as zpaq
// does. By default such data, which is usually a sign of damage, is an error.
func WithSkipGarbage(on bool) Option { return func(c *config) { c.skipGarbage = on } }
