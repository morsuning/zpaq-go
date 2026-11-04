package zpaq

import (
	"crypto/sha1"
	"strconv"
	"strings"
)

// modelFor returns the compiled model for a block of data at the given level.
func modelFor(level Level, data []byte) (*zpaql, error) {
	switch {
	case level <= Store:
		cfg, args, err := makeConfig("0" + itos(max(lg(uint(len(data)+4095))-20, 0)) + ",0")
		if err != nil {
			return nil, err
		}
		return compileConfig(cfg, args)
	case level <= 3:
		z := new(zpaql)
		err := func() (err error) {
			defer recoverFormat(&err)
			z.readHeader((&sliceSource{b: builtinModels[level-1]}).get)
			return nil
		}()
		return z, err
	default:
		cfg, args, err := makeConfig(methodString(int(level), data))
		if err != nil {
			return nil, err
		}
		return compileConfig(cfg, args)
	}
}

// comment returns the comment of a block's segment: the block size, followed
// by extra for the first block.
func (c *config) segmentComment(n int, first bool) string {
	s := strconv.Itoa(n)
	if !first {
		return s
	}
	if !c.modTime.IsZero() {
		s += " " + c.modTime.Format("20060102150405")
	}
	if c.comment != "" {
		s += " " + c.comment
	}
	// zpaq treats a comment ending in "jDC\x01" as a journaling block.
	if strings.HasSuffix(s, "jDC\x01") {
		s += " "
	}
	return s
}

// encodeBlock compresses data into one complete ZPAQ block: locator tag,
// header, one segment named name with the given comment, checksum and
// end-of-block byte. progress and canceled may be nil.
func encodeBlock(data []byte, c *config, first bool, progress func(int), canceled func() bool) ([]byte, error) {
	name := ""
	if first {
		name = c.name
	}
	comment := c.segmentComment(len(data), first)
	var sum *[20]byte
	if c.checksum {
		s := sha1.Sum(data)
		sum = &s
	}
	z, err := modelFor(c.level, data)
	if err != nil {
		return nil, err
	}
	out, err := encodeWith(z, data, name, comment, sum, progress, canceled)
	if err != nil {
		return nil, err
	}
	if c.level > Store && z.header[6] > 0 {
		// Fall back to stored chunks when modeling does not help.
		if st, err := modelFor(Store, data); err == nil {
			if stored, err := encodeWith(st, data, name, comment, sum, nil, nil); err == nil && len(stored) < len(out) {
				return stored, nil
			}
		}
	}
	return out, nil
}

func encodeWith(z *zpaql, data []byte, name, comment string, sum *[20]byte, progress func(int), canceled func() bool) (out []byte, err error) {
	defer recoverFormat(&err)
	var pr *predictor
	if z.header[6] > 0 {
		pr = newPredictor(z)
	}
	enc := newEncoder(pr)
	out = make([]byte, 0, len(data)/2+256)
	out = append(out, blockTag[:]...)
	out = append(out, byte(1+b2i(z.header[6] == 0)), 1)
	out = z.writeHeader(out, false)
	out = append(out, 1)
	out = append(out, name...)
	out = append(out, 0)
	out = append(out, comment...)
	out = append(out, 0, 0)

	enc.out = out
	enc.compress(0) // no post-processing program
	const step = 1 << 16
	for i := 0; i < len(data); i += step {
		if canceled != nil && canceled() {
			return nil, errCanceled
		}
		end := min(i+step, len(data))
		for _, ch := range data[i:end] {
			enc.compress(int(ch))
		}
		if progress != nil {
			progress(end - i)
		}
	}
	enc.compress(-1)
	out = append(enc.out, 0, 0, 0, 0)
	if sum != nil {
		out = append(out, 253)
		out = append(out, sum[:]...)
	} else {
		out = append(out, 254)
	}
	return append(out, 255), nil
}

// Compress appends to dst a ZPAQ archive holding src and returns the extended
// slice. The archive is in zpaq's streaming format: "zpaq x" extracts it
// (and "zpaq l"/"zpaq t" list and test it); src larger than the block size is
// split into several blocks, only the first of which carries the name.
func Compress(dst, src []byte, opts ...Option) ([]byte, error) {
	c := newConfig(opts)
	return compressAll(dst, src, &c)
}

func compressAll(dst, src []byte, c *config) ([]byte, error) {
	w := newPipeline(c, func(b []byte) error { dst = append(dst, b...); return nil })
	if err := w.write(src); err != nil {
		w.abort()
		return dst, err
	}
	if err := w.close(); err != nil {
		return dst, err
	}
	return dst, nil
}
