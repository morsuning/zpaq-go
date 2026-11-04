# Changelog

All notable changes are listed here. The project follows [Semantic Versioning](https://semver.org/).

## v0.1.0

First release.

- ZPAQ level 2 encoder and decoder in pure Go: libzpaq's ZPAQL virtual machine,
  all component types (CONST, CM, ICM, MATCH, AVG, MIX2, MIX, ISSE, SSE),
  arithmetic coder and streaming-format container.
- Levels 0 (store) to 5; levels 1 to 3 use libzpaq's built-in models, levels 4
  and 5 generate the models of `zpaq -method 4` and `-method 5`.
- `Compress`, `Decompress`, `NewWriter`, `NewReader` and `Walk`; parallel
  block compression and decompression with output independent of concurrency.
- Safety limits for untrusted input: ZPAQL step budget, model memory and output
  size limits, strict container checks, context cancellation.
- `cmd/zpaq-go` command line tool (`c`, `d`, `l`, `t`).
- `ctxmix` subpackage: a stronger, non-ZPAQ context-mixing codec.
- Verified against the reference zpaq 7.15 in both directions.
