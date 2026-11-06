# zpaq-go

[![Go Reference](https://pkg.go.dev/badge/github.com/morsuning/zpaq-go.svg)](https://pkg.go.dev/github.com/morsuning/zpaq-go)
[![CI](https://github.com/morsuning/zpaq-go/actions/workflows/ci.yml/badge.svg)](https://github.com/morsuning/zpaq-go/actions/workflows/ci.yml)

A pure-Go implementation of the [ZPAQ](https://mattmahoney.net/dc/zpaq.html)
compression format. No cgo, no dependencies, Go 1.21+. It writes archives that
the reference `zpaq` program extracts, and reads what `zpaq` writes.

[简体中文](README.zh-CN.md)

```
go get github.com/morsuning/zpaq-go
```

```go
archive, err := zpaq.Compress(nil, data, zpaq.WithLevel(zpaq.Best), zpaq.WithName("data.bin"))
if err != nil { ... }
restored, err := zpaq.Decompress(nil, archive)
```

Streaming, with blocks compressed on several cores:

```go
w := zpaq.NewWriter(dst, zpaq.WithLevel(zpaq.Default), zpaq.WithName("log.txt"))
io.Copy(w, src)
w.Close()

r := zpaq.NewReader(archive)
defer r.Close()
io.Copy(out, r)
```

## Why ZPAQ

ZPAQ is a context-mixing format: every block of an archive carries its own
model, so any decoder can read any encoder's output, and the compression
ratio is in the class of PAQ. On the sample below, level 5 output is about 24%
smaller than `xz -9e`.

## Compatibility

Archives written by this package are ZPAQ streaming-format archives. The
reference program extracts them:

```
$ zpaq x archive.zpaq
> data.bin
1 streaming segments extracted
0.027 seconds (all OK)
```

This is checked by the test suite against **zpaq 7.15**, in both directions
(`interop_test.go`, run in CI):

- zpaq extracts, byte for byte, archives produced here at levels 0–5, with one
  block or many, with and without a file name, concatenated, and from the
  streaming `Writer`.
- this package decodes, verifying every SHA-1, archives that zpaq produced with
  `-method 0` to `5` (including the LZ77 and BWT post-processing programs) and
  several advanced methods, and, for inputs without repeated fragments, the data blocks of those archives match the
  original input.

Limits:

- zpaq's default "journaling" archives (`zpaq a`) hold files, versions and
  deduplicated fragments in index and data blocks. `Walk` decodes their blocks,
  but this package does not interpret the journal; use `zpaq` to list or
  extract files from them.
- No encryption (`zpaq -key`).
- Levels 4 and 5 build the same context-mixing models as `zpaq -method 4/5`,
  but do not apply zpaq's LZ77, BWT or E8E9 pre-processing, so sizes can
  differ from zpaq's for data where it would use those.

## Levels

| Level | Model | 3 MB text sample |
|---|---|---|
| 0 `Store` | none | 3,000,259 |
| 1 `Fast` | libzpaq "min" | 415,926 |
| 2 | libzpaq "mid" | 210,497 |
| 3 | libzpaq "max" | 194,285 |
| 4 `Default` | like `zpaq -m4` | 208,336 |
| 5 `Best` | like `zpaq -m5` | 196,663 |

Sample: the first 3,000,000 bytes of a tar archive of documentation (mostly
text). For comparison, on the same bytes: `xz -9e` 258,988; `zpaq -method 4`
210,128; `zpaq -method 5` 198,408 (these include the journaling index). The
best level depends on the data; try a few if you need the smallest output.

Speed is that of context mixing, a few hundred KB/s per core at the high
levels (level 5: about 0.2 MB/s, roughly twice the time of the C reference),
and decompression takes as long as compression. Input is cut into blocks of
16 MiB by default (`WithBlockSize`) and the blocks run on up to 4 goroutines
(`WithConcurrency`); the output does not depend on the number of goroutines.

## Untrusted input

The decoder runs ZPAQL programs from the archive, so it limits them: an
instruction budget, `WithMaxMemory` (default 4 GiB per block) and
`WithMaxOutput` for the model memory and the size of the output, strict
checks of the container, and `WithContext` for cancellation. Malformed input
yields an error wrapping `zpaq.ErrCorrupt`; it never panics (the fuzz tests
check this). Set the two limits when decoding data you do not control.

## Command line

```
go install github.com/morsuning/zpaq-go/cmd/zpaq-go@latest

zpaq-go c -level 5 -o notes.zpaq notes.txt     # compress
zpaq-go d -o notes.txt notes.zpaq              # decompress
zpaq-go l notes.zpaq                           # list segments
zpaq-go t notes.zpaq                           # verify
```

## Related project: ctxmix

[ctxmix](https://github.com/morsuning/ctxmix) is a separate codec with its own format
(not ZPAQ compatible): a stronger context-mixing model for source code and text, about
3.5% smaller than `zpaq -m5` and 27% smaller than `xz -9e` on Go source code. Use it when
only your own code reads the data. It began as the `ctxmix` subpackage of this module and
has been its own module (`github.com/morsuning/ctxmix`) since zpaq-go v0.2.0.

## Credits and license

The format and the reference implementation are by Matt Mahoney and in the
public domain; see [NOTICE.md](NOTICE.md). This package is MIT licensed.
