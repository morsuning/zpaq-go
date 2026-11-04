// Package zpaq is a pure-Go implementation of the ZPAQ level 2 compression
// format: a port of Matt Mahoney's public-domain libzpaq. It has no
// dependencies, no cgo, and runs anywhere Go does.
//
// ZPAQ is a context-mixing format with very high compression ratios. Every
// block of a ZPAQ archive carries its own model (a ZPAQL program and a list of
// components), which is why any conforming decoder can read what any encoder
// wrote. This package contains both sides: an encoder with the same levels
// and models as the reference "zpaq" program, and a decoder for arbitrary
// ZPAQ models, including those with ZPAQL post-processing programs (the
// inverse BWT and LZ77 steps of zpaq's faster levels).
//
// # Compatibility
//
// The archives written by this package are ZPAQ streaming-format archives:
// a sequence of self-describing blocks, one or more segments each, with a
// SHA-1 per segment. They are extracted by the reference program:
//
//	zpaq x archive.zpaq
//
// The test suite runs the reference zpaq 7.15 against this package in both
// directions (see interop_test.go; the tests are skipped when no zpaq
// binary is found). zpaq extracts archives produced here at every level,
// with one block or many, with and without a file name; and this package
// decodes, with every checksum verified, archives that zpaq produced with
// -method 0 to 5 and several advanced methods.
//
// Two limits are worth knowing:
//
//   - zpaq's default "journaling" archives (the output of "zpaq a") consist of
//     index and data blocks that describe files, versions and deduplicated
//     fragments. This package decodes their blocks (see [Walk]) but does not
//     interpret the journal, so it cannot list or extract individual files
//     from them. Use zpaq for that.
//   - Encryption (zpaq -key) is not supported.
//
// # Usage
//
// Compress and Decompress work on byte slices:
//
//	archive, err := zpaq.Compress(nil, data, zpaq.WithLevel(zpaq.Best), zpaq.WithName("data.bin"))
//	restored, err := zpaq.Decompress(nil, archive)
//
// [NewWriter] and [NewReader] stream. Input is cut into blocks (16 MiB by
// default, see [WithBlockSize]) that are compressed and decompressed on
// several goroutines at once; the output does not depend on the number of
// goroutines. [Walk] visits the segments of an archive one by one, with
// their names and comments.
//
// # Levels
//
// The levels follow zpaq's -method numbers. Higher levels compress better and
// take much more time and memory; see [Level] for what each one does. Levels 1
// to 3 are libzpaq's built-in models, levels 4 and 5 generate the context
// mixing models that the zpaq program builds for -method 4 and 5, choosing
// them from the data (text or binary, record length) as it does. They never
// use zpaq's LZ77, BWT or E8E9 pre-processing, so the output size can differ
// from the zpaq program's for data where it would have chosen one of those.
// A block that does not compress is stored.
//
// # Untrusted input
//
// The decoder runs ZPAQL programs found in the archive. It limits the
// instructions a program may execute, the memory its model may allocate
// ([WithMaxMemory]) and the amount of data produced ([WithMaxOutput]), and it
// reports malformed input as an error wrapping [ErrCorrupt]. Set the two
// limits when decoding data from untrusted sources.
//
// # Related package
//
// The ctxmix subpackage is a separate, stronger context-mixing model that is
// not ZPAQ compatible: a few percent smaller than level 5, with its own
// format. Use it when only your own code reads the data.
package zpaq
