// Package ctxmix is a pure-Go, dependency-free, general purpose compressor
// based on context mixing, the technique behind the PAQ, lpaq and ZPAQ family
// of archivers. It is the experimental, non-ZPAQ companion of the parent
// package: it compresses somewhat better than the ZPAQ models of the parent
// package on the data tried so far (about 3-4% smaller than level 5 on Go
// source code), but its output can NOT be read by zpaq or any other ZPAQ
// decompressor (see "Compatibility" below). Use the parent package when you
// need ZPAQ compatibility.
//
// It trades speed for size: on Go source code it produced output about 27%
// smaller than "xz -9e" (see the README for measurements), but it is roughly 6x
// slower than xz and uses about 130 MiB of memory per in-flight 16 MiB block.
// Decompression costs the same as compression, because the decoder runs the
// same model.
//
// # Usage
//
// The streaming API mirrors compress/flate:
//
//	w, err := ctxmix.NewWriter(dst)            // or NewWriter(dst, ctxmix.WithConcurrency(4))
//	if err != nil { ... }
//	w.Write(data)
//	w.Close()                                  // required: flushes the last block
//
//	r, err := ctxmix.NewReader(src)
//	if err != nil { ... }
//	io.Copy(out, r)
//
// For in-memory data use [EncodeAll] and [DecodeAll]. Callers that build their
// own container format can use the headerless [EncodeBlock] and [DecodeBlock].
//
// # Format
//
// A stream is a sequence of independent blocks (16 MiB of input by default),
// each with its own length and CRC-32, followed by an end marker, so blocks can
// be compressed and decompressed in parallel (see [WithConcurrency]). The output
// does not depend on the concurrency setting. Every frame starts with a version
// byte; the format is not yet frozen and may change before v1.0.0.
//
// # Compatibility
//
// This is NOT the ZPAQ file format and cannot be read by zpaq, nor can it read
// .zpaq archives. It reuses the bit-history state table of libzpaq (public
// domain) and the general architecture of PAQ-style compressors, but the model,
// the bitstream and the container are specific to this package.
//
// # Determinism
//
// The model uses integer arithmetic only, with all lookup tables embedded as
// constants, so compressed output is bit-identical on every architecture and
// operating system. The test suite pins golden digests to detect accidental
// changes.
//
// # Safety
//
// Decoding is defensive: malformed input yields [ErrCorrupt] (or
// io.ErrUnexpectedEOF), never a panic or an out-of-bounds access, and every
// block is verified against its CRC-32. As with any decompressor, a tiny crafted
// stream can expand to a lot of data; use [WithMaxBlockSize] and wrap the Reader
// with io.LimitReader when decoding untrusted input.
package ctxmix
