package zpaq

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func testInputs() map[string][]byte {
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, 20000)
	rng.Read(random)
	text := []byte(strings.Repeat("The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs.\n", 300))
	var rec bytes.Buffer
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&rec, "%08d,%d,%x\n", i*7, i%13, i*i)
	}
	zeros := make([]byte, 100000)
	periodic := make([]byte, 30000)
	for i := range periodic {
		periodic[i] = byte(i%7*31 + i%100/50)
	}
	return map[string][]byte{
		"empty":    {},
		"one":      {42},
		"random":   random,
		"text":     text,
		"records":  rec.Bytes(),
		"zeros":    zeros,
		"periodic": periodic,
	}
}

func TestRoundTripLevels(t *testing.T) {
	for name, data := range testInputs() {
		for lvl := Store; lvl <= Best; lvl++ {
			t.Run(fmt.Sprintf("%s/L%d", name, lvl), func(t *testing.T) {
				c, err := Compress(nil, data, WithLevel(lvl))
				if err != nil {
					t.Fatal(err)
				}
				d, err := Decompress(nil, c)
				if err != nil {
					t.Fatalf("decompress: %v", err)
				}
				if !bytes.Equal(d, data) {
					t.Fatalf("mismatch: got %d bytes, want %d", len(d), len(data))
				}
				t.Logf("%d -> %d", len(data), len(c))
			})
		}
	}
}
