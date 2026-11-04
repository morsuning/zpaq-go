package ctxmix

import (
	"bytes"
	"math/rand"
	"testing"
)

// textData returns deterministic, compressible, source-code-like data.
func textData(n int, seed uint32) []byte {
	var b bytes.Buffer
	x := seed
	words := []string{"alpha", "beta", "gamma", "delta", "func", "return", "if", "else", "for", "range", "x", "y", "(", ")", "{", "}", "\n", "\t", " ", "=", "+"}
	for b.Len() < n {
		x = x*1664525 + 1013904223
		b.WriteString(words[(x>>16)%uint32(len(words))])
		if (x>>8)%7 == 0 {
			b.WriteByte(byte(x >> 24))
		}
	}
	return b.Bytes()[:n]
}

// randData returns deterministic incompressible data.
func randData(n int, seed int64) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// vectorData is the fixed input of the golden vectors. Do not change it.
func vectorData() []byte { return textData(300000, 12345) }

type testInput struct {
	name string
	data []byte
}

func testInputs(sizes ...int) []testInput {
	var out []testInput
	for _, n := range sizes {
		out = append(out,
			testInput{"text", textData(n, uint32(n)+1)},
			testInput{"random", randData(n, int64(n)+1)},
			testInput{"zeros", make([]byte, n)},
			testInput{"ones", bytes.Repeat([]byte{0xff}, n)},
			testInput{"period", bytes.Repeat([]byte("abcdefghij0123456789"), n/20+1)[:n]},
		)
	}
	return out
}

func mustEncode(t testing.TB, data []byte, opts ...Option) []byte {
	t.Helper()
	comp, err := EncodeAll(data, opts...)
	if err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	return comp
}

func mustDecode(t testing.TB, comp []byte, opts ...Option) []byte {
	t.Helper()
	out, err := DecodeAll(comp, opts...)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	return out
}

func levelName(l Level) string {
	if l == Fast {
		return "fast"
	}
	return "best"
}

// short scales a size down in -short mode.
func short(n int) int {
	if testing.Short() {
		return n / 4
	}
	return n
}
