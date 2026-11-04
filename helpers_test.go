package zpaq

import (
	"bytes"
	"crypto/sha1"
	"math/rand"
	"testing"
)

// testModel compiles a config (text) with default arguments.
func testModel(t testing.TB, text string) *zpaql {
	t.Helper()
	z, err := compileConfig(text, [9]int{})
	if err != nil {
		t.Fatalf("compile: %v\n%s", err, text)
	}
	return z
}

// order1Config is a small model: order-1 ICM followed by order-2 ISSE.
const order1Config = `comp 1 1 0 0 3
  0 icm 12
  1 isse 16 0
  2 mix2 0 0 1 24 0
hcomp
  c++ *c=a b=c a=0 hash *d=a
  d= 1 b=c a=*b hash b++ hash *d=a
  d= 2 *d=0
halt
post 0 end
`

type manualSeg struct {
	name, comment string
	payload       []byte // bytes to be compressed, including any PCOMP prefix
	sum           []byte // 20-byte SHA-1, nil for none
}

// manualBlock builds a block by hand from z and segments, independently of
// encodeBlock.
func manualBlock(t testing.TB, z *zpaql, segs ...manualSeg) []byte {
	t.Helper()
	var pr *predictor
	if z.header[6] > 0 {
		pr = newPredictor(z)
	}
	enc := newEncoder(pr)
	out := append([]byte(nil), blockTag[:]...)
	out = append(out, byte(1+b2i(z.header[6] == 0)), 1)
	out = z.writeHeader(out, false)
	enc.out = out
	for _, s := range segs {
		enc.out = append(enc.out, 1)
		enc.out = append(enc.out, s.name...)
		enc.out = append(enc.out, 0)
		enc.out = append(enc.out, s.comment...)
		enc.out = append(enc.out, 0, 0)
		for _, c := range s.payload {
			enc.compress(int(c))
		}
		enc.compress(-1)
		enc.out = append(enc.out, 0, 0, 0, 0)
		if s.sum != nil {
			enc.out = append(enc.out, 253)
			enc.out = append(enc.out, s.sum...)
		} else {
			enc.out = append(enc.out, 254)
		}
	}
	return append(enc.out, 255)
}

func sum1(b []byte) []byte { s := sha1.Sum(b); return s[:] }

func randomBytes(seed int64, n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(b)
	return b
}

// textBytes returns n bytes of pseudo-text with a small vocabulary.
func textBytes(seed int64, n int) []byte {
	words := []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog", "context", "mixing", "archive", "block", "segment", "model", "predict"}
	rng := rand.New(rand.NewSource(seed))
	var b bytes.Buffer
	for b.Len() < n {
		b.WriteString(words[rng.Intn(len(words))])
		if rng.Intn(12) == 0 {
			b.WriteString(".\n")
		} else {
			b.WriteByte(' ')
		}
	}
	return b.Bytes()[:n]
}

func mustCompress(t testing.TB, data []byte, opts ...Option) []byte {
	t.Helper()
	c, err := Compress(nil, data, opts...)
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	return c
}

func mustDecompress(t testing.TB, c []byte, opts ...Option) []byte {
	t.Helper()
	d, err := Decompress(nil, c, opts...)
	if err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	return d
}
