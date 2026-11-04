package zpaq

import (
	"bytes"
	"testing"
)

// FuzzDecompress feeds arbitrary bytes to the decoder. It must never panic,
// hang or allocate without bound, and anything it accepts must decode again
// to the same output.
func FuzzDecompress(f *testing.F) {
	for _, level := range []Level{Store, 1, 2, 3, Default, Best} {
		f.Add(mustCompress(f, []byte("fuzz seed fuzz seed fuzz seed"), WithLevel(level), WithName("n")))
	}
	f.Add(mustCompress(f, textBytes(1, 3000), WithLevel(2), WithBlockSize(1000)))
	f.Add([]byte{})
	f.Add(blockTag[:])
	f.Fuzz(func(t *testing.T, in []byte) {
		opts := []Option{WithMaxOutput(1 << 22), WithMaxMemory(1 << 28), WithConcurrency(2)}
		out, err := Decompress(nil, in, opts...)
		if err != nil {
			return
		}
		again, err := Decompress(nil, in, opts...)
		if err != nil || !bytes.Equal(out, again) {
			t.Fatalf("decoding is not deterministic: %v", err)
		}
	})
}

// FuzzRoundTrip compresses arbitrary data at an arbitrary level and block
// size and requires the exact bytes back.
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte(""), uint8(0), uint8(0))
	f.Add([]byte("a"), uint8(1), uint8(1))
	f.Add([]byte("hello hello hello hello"), uint8(4), uint8(7))
	f.Add(textBytes(2, 2000), uint8(2), uint8(200))
	f.Add(randomBytes(3, 500), uint8(3), uint8(64))
	f.Fuzz(func(t *testing.T, data []byte, level, block uint8) {
		if len(data) > 1<<16 {
			data = data[:1<<16]
		}
		lv := Level(level % 6)
		if lv == Best && len(data) > 1<<12 {
			lv = Default // keep each iteration fast
		}
		opts := []Option{WithLevel(lv), WithBlockSize(int(block)*64 + 1), WithConcurrency(2)}
		comp, err := Compress(nil, data, opts...)
		if err != nil {
			t.Fatalf("Compress: %v", err)
		}
		got, err := Decompress(nil, comp)
		if err != nil {
			t.Fatalf("Decompress: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("round trip mismatch (level %d, %d bytes)", lv, len(data))
		}
	})
}
