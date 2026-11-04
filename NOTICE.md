# Notices

## libzpaq / ZPAQ

The ZPAQ format, the ZPAQL virtual machine, the predictor components, the
built-in models and the model-generation rules used by this package are
described in, and ported from, **libzpaq** and **zpaq** by Matt Mahoney
(<https://mattmahoney.net/dc/zpaq.html>, version 7.15). Their authors
released them into the public domain ("This is free and unencumbered software
released into the public domain", <http://unlicense.org>). This package is an
independent re-implementation in Go that follows that code closely so that the
output stays bit-compatible; thank you to Matt Mahoney for the format and for
putting it in the public domain.

The reference `zpaq` program is only used by the optional interoperability
tests (`interop_test.go`) when it is installed; none of its source is part of
this repository.

## ctxmix

The `ctxmix` subpackage is original work and is not ZPAQ compatible.
It is licensed under the repository's MIT license.
