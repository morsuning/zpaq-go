package zpaq

import (
	"os"
	"testing"
)

// BenchmarkCompress measures compression of the file named by ZPAQ_BENCH_FILE
// (skipped when unset) at the level in ZPAQ_BENCH_LEVEL (default 5).
func BenchmarkCompress(b *testing.B) {
	name := os.Getenv("ZPAQ_BENCH_FILE")
	if name == "" {
		b.Skip("ZPAQ_BENCH_FILE not set")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		b.Fatal(err)
	}
	lvl := Best
	if s := os.Getenv("ZPAQ_BENCH_LEVEL"); s != "" {
		lvl = Level(s[0] - '0')
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compress(nil, data, WithLevel(lvl), WithConcurrency(1)); err != nil {
			b.Fatal(err)
		}
	}
}
