package zpaq

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sampleArchives(t testing.TB) map[string][]byte {
	text := textBytes(31, 1500)
	return map[string][]byte{
		"store":     mustCompress(t, text, WithLevel(Store), WithName("n")),
		"fast":      mustCompress(t, text, WithLevel(Fast), WithName("n")),
		"mid":       mustCompress(t, text, WithLevel(2)),
		"default":   mustCompress(t, text, WithLevel(Default)),
		"best":      mustCompress(t, text[:300], WithLevel(Best), WithName("best")),
		"multi":     mustCompress(t, text[:900], WithLevel(Fast), WithBlockSize(400)),
		"nochecksm": mustCompress(t, text, WithLevel(Fast), WithChecksum(false)),
	}
}

func TestTruncation(t *testing.T) {
	for name, c := range sampleArchives(t) {
		if name == "best" && testing.Short() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			full, err := Decompress(nil, c)
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < len(c); n++ {
				got, err := Decompress(nil, c[:n])
				if err == nil {
					// only acceptable when whole blocks survived
					if !bytes.HasPrefix(full, got) || len(got) == len(full) && n < len(c)-1 && name != "nochecksm" {
						t.Fatalf("truncated at %d/%d: no error, got %d bytes", n, len(c), len(got))
					}
					continue
				}
				if !errors.Is(err, ErrCorrupt) {
					t.Fatalf("truncated at %d: error %v does not wrap ErrCorrupt", n, err)
				}
			}
		})
	}
}

func TestBitFlips(t *testing.T) {
	for name, c := range sampleArchives(t) {
		if name == "best" || name == "mid" && testing.Short() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			want, _ := Decompress(nil, c)
			buf := append([]byte(nil), c...)
			flip := func(i, bit int) {
				buf[i] ^= 1 << bit
				defer func() { buf[i] ^= 1 << bit }()
				got, err := Decompress(nil, buf, WithMaxOutput(1<<20), WithMaxMemory(1<<28))
				if err == nil && !bytes.Equal(got, want) && name != "nochecksm" {
					t.Fatalf("flip byte %d bit %d: no error but different output", i, bit)
				}
				if err != nil && !errors.Is(err, ErrCorrupt) && !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrMemory) {
					t.Fatalf("flip byte %d bit %d: unexpected error %v", i, bit, err)
				}
			}
			for i := 0; i < len(buf); i++ {
				if testing.Short() && i >= 80 && i < len(buf)-40 && i%7 != 0 {
					continue // headers and trailers get every bit, the coded data a sample
				}
				for bit := 0; bit < 8; bit++ {
					if testing.Short() && i >= 80 && i < len(buf)-40 && bit != i%8 {
						continue
					}
					flip(i, bit)
				}
			}
		})
	}
}

func TestGarbageNeverPanics(t *testing.T) {
	rng := randomBytes(99, 1<<16)
	for i := 0; i < 300; i++ {
		n := 16 + (i*37)%400
		in := append(append([]byte(nil), blockTag[:]...), rng[i*50:i*50+n]...)
		in[16] = byte(1 + i%2) // plausible level byte
		in[17] = 1
		_, err := Decompress(nil, in, WithMaxOutput(1<<20), WithMaxMemory(1<<28))
		if err == nil {
			t.Fatalf("garbage %d decoded without error", i)
		}
	}
}

func TestLimits(t *testing.T) {
	zeros := make([]byte, 8<<20)
	c := mustCompress(t, zeros, WithLevel(Fast))
	if len(c) > 20000 {
		t.Fatalf("8 MiB of zeros compressed to %d bytes", len(c))
	}
	if _, err := Decompress(nil, c, WithMaxOutput(1<<20)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("WithMaxOutput: err = %v", err)
	}
	r := NewReader(bytes.NewReader(c), WithMaxOutput(1<<20))
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Reader WithMaxOutput: err = %v", err)
	}
	if err := Walk(bytes.NewReader(c), func(string, string, []byte) error { return nil }, WithMaxOutput(1<<20)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Walk WithMaxOutput: err = %v", err)
	}
	// exactly at the limit is fine
	if d, err := Decompress(nil, c, WithMaxOutput(int64(len(zeros)))); err != nil || len(d) != len(zeros) {
		t.Fatalf("limit == size: %d, %v", len(d), err)
	}
	// memory limit
	if _, err := Decompress(nil, c, WithMaxMemory(1<<20)); !errors.Is(err, ErrMemory) {
		t.Fatalf("WithMaxMemory: err = %v", err)
	}
	if _, err := Decompress(nil, c, WithMaxMemory(0)); err != nil {
		t.Fatalf("no memory limit: %v", err)
	}
}

func TestContextCancel(t *testing.T) {
	data := textBytes(41, 3<<20)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	start := time.Now()
	_, err := Compress(nil, data, WithLevel(Best), WithContext(ctx), WithConcurrency(2), WithBlockSize(1<<20),
		WithProgress(func(int) {
			if calls.Add(1) == 3 {
				cancel()
			}
		}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compress: err = %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("cancellation took %v", d)
	}

	c := mustCompress(t, data[:1<<20], WithLevel(Default))
	ctx2, cancel2 := context.WithCancel(context.Background())
	calls.Store(0)
	_, err = Decompress(nil, c, WithContext(ctx2), WithProgress(func(int) {
		if calls.Add(1) == 2 {
			cancel2()
		}
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Decompress: err = %v", err)
	}
	// an already canceled context fails immediately
	ctx3, cancel3 := context.WithCancel(context.Background())
	cancel3()
	if _, err := Compress(nil, data[:100], WithContext(ctx3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Compress: %v", err)
	}
	if _, err := Decompress(nil, c, WithContext(ctx3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Decompress: %v", err)
	}
}

func TestDeterministicAcrossConcurrency(t *testing.T) {
	data := append(textBytes(51, 40000), randomBytes(52, 20000)...)
	base := mustCompress(t, data, WithLevel(Default), WithBlockSize(7000), WithConcurrency(1))
	for _, j := range []int{2, 3, 8} {
		if got := mustCompress(t, data, WithLevel(Default), WithBlockSize(7000), WithConcurrency(j)); !bytes.Equal(got, base) {
			t.Fatalf("concurrency %d changes the output", j)
		}
		if got := mustDecompress(t, base, WithConcurrency(j)); !bytes.Equal(got, data) {
			t.Fatalf("decompress with concurrency %d differs", j)
		}
	}
}

func TestWriterMatchesCompress(t *testing.T) {
	data := textBytes(61, 30000)
	want := mustCompress(t, data, WithLevel(Fast), WithBlockSize(8000), WithName("w"))
	for _, chunk := range []int{1, 7, 1000, 8000, 8001, 100000} {
		var buf bytes.Buffer
		w := NewWriter(&buf, WithLevel(Fast), WithBlockSize(8000), WithName("w"))
		for off := 0; off < len(data); off += chunk {
			end := min(off+chunk, len(data))
			if n, err := w.Write(data[off:end]); err != nil || n != end-off {
				t.Fatalf("Write: %d, %v", n, err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Fatalf("chunk %d: output differs from Compress", chunk)
		}
	}
}

func TestWriterEdgeCases(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("Write after Close succeeded")
	}
	if d := mustDecompress(t, buf.Bytes()); len(d) != 0 {
		t.Fatalf("empty archive decoded to %d bytes", len(d))
	}
	// a failing destination
	boom := errors.New("boom")
	w = NewWriter(failWriter{boom}, WithBlockSize(100))
	var werr error
	for i := 0; i < 50 && werr == nil; i++ {
		_, werr = w.Write(textBytes(int64(i), 100))
	}
	if cerr := w.Close(); !errors.Is(cerr, boom) && !errors.Is(werr, boom) {
		t.Fatalf("destination error lost: write=%v close=%v", werr, cerr)
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestReaderBehaviour(t *testing.T) {
	data := textBytes(71, 20000)
	c := mustCompress(t, data, WithLevel(Fast), WithBlockSize(3000))
	// one byte at a time
	r := NewReader(bytes.NewReader(c))
	var got []byte
	var b [1]byte
	for {
		n, err := r.Read(b[:])
		got = append(got, b[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("byte-wise read differs")
	}
	if n, err := r.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("read after EOF: %d, %v", n, err)
	}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty read: %d, %v", n, err)
	}
	r.Close()

	// an error is sticky
	bad := append([]byte(nil), c...)
	bad[len(bad)/2] ^= 0xff
	r = NewReader(bytes.NewReader(bad))
	_, err1 := io.Copy(io.Discard, r)
	_, err2 := r.Read(b[:])
	if err1 == nil || err2 == nil || !errors.Is(err1, ErrCorrupt) {
		t.Fatalf("errors: %v / %v", err1, err2)
	}

	// an underlying read error surfaces
	boom := errors.New("read failure")
	r = NewReader(io.MultiReader(bytes.NewReader(c[:len(c)/2]), errReader{boom}))
	if _, err := io.Copy(io.Discard, r); !errors.Is(err, boom) {
		t.Fatalf("underlying error: %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestReaderCloseStopsGoroutines(t *testing.T) {
	data := textBytes(81, 60000)
	c := mustCompress(t, data, WithLevel(Fast), WithBlockSize(2000))
	runtime.GC()
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		r := NewReader(bytes.NewReader(c), WithConcurrency(3))
		var b [10]byte
		if _, err := r.Read(b[:]); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if _, err := r.Read(b[:]); err == nil {
			// data already queued may still be delivered; an error must follow eventually
			for err == nil {
				_, err = r.Read(make([]byte, 4096))
			}
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before+1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+1 {
		t.Fatalf("goroutines: %d before, %d after Close", before, after)
	}
}

func TestProgressTotals(t *testing.T) {
	data := textBytes(91, 200000)
	var mu sync.Mutex
	total := 0
	add := func(n int) { mu.Lock(); total += n; mu.Unlock() }
	c := mustCompress(t, data, WithLevel(Fast), WithBlockSize(70000), WithProgress(add), WithConcurrency(3))
	if total != len(data) {
		t.Fatalf("compress progress = %d, want %d", total, len(data))
	}
	total = 0
	mustDecompress(t, c, WithProgress(add), WithConcurrency(3))
	if total != len(data) {
		t.Fatalf("decompress progress = %d, want %d", total, len(data))
	}
}

func TestWalkCallbackError(t *testing.T) {
	c := mustCompress(t, textBytes(95, 9000), WithLevel(Fast), WithBlockSize(3000))
	stop := errors.New("stop")
	calls := 0
	err := Walk(bytes.NewReader(c), func(string, string, []byte) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestOptionClamping(t *testing.T) {
	data := textBytes(97, 3000)
	for _, opts := range [][]Option{
		{WithLevel(99)}, {WithLevel(-3)}, {WithBlockSize(0)}, {WithBlockSize(-5)}, {WithConcurrency(0)}, {WithConcurrency(-1)},
	} {
		if got := mustDecompress(t, mustCompress(t, data, opts...)); !bytes.Equal(got, data) {
			t.Fatalf("round trip with odd options failed")
		}
	}
	if c := newConfig([]Option{WithLevel(99)}); c.level != Best {
		t.Fatalf("level 99 -> %d", c.level)
	}
	if c := newConfig([]Option{WithLevel(-2)}); c.level != Store {
		t.Fatalf("level -2 -> %d", c.level)
	}
}

func TestDecompressAppends(t *testing.T) {
	data := textBytes(98, 2000)
	c := mustCompress(t, data, WithLevel(Fast))
	got, err := Decompress([]byte("prefix:"), c)
	if err != nil || !bytes.Equal(got, append([]byte("prefix:"), data...)) {
		t.Fatalf("Decompress does not append: %v", err)
	}
	out, err := Compress([]byte("hdr"), data, WithLevel(Fast))
	if err != nil || !bytes.HasPrefix(out, []byte("hdr")) || !bytes.Equal(out[3:], c) {
		t.Fatalf("Compress does not append: %v", err)
	}
}
