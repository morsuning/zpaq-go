package zpaq

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestContainerLayout checks the byte layout of a compressed block against the
// ZPAQ level 2 specification, parsing it without using the reader.
func TestContainerLayout(t *testing.T) {
	data := textBytes(1, 5000)
	c := mustCompress(t, data, WithLevel(Fast), WithName("file.txt"),
		WithModTime(time.Date(2026, 11, 4, 12, 30, 59, 0, time.UTC)), WithComment("extra"))
	if !bytes.HasPrefix(c, blockTag[:]) {
		t.Fatalf("missing locator tag")
	}
	if c[16] != 1 || c[17] != 1 {
		t.Fatalf("level/type bytes = %d %d, want 1 1", c[16], c[17])
	}
	hsize := int(c[18]) | int(c[19])<<8
	if hsize != len(builtinModels[0])-2 {
		t.Fatalf("hsize = %d", hsize)
	}
	// the header is copied verbatim from the built-in model
	if !bytes.Equal(c[18:18+hsize+2], builtinModels[0]) {
		t.Fatalf("header differs from built-in model 1")
	}
	pos := 18 + hsize + 2
	if c[pos] != 1 {
		t.Fatalf("segment marker = %d", c[pos])
	}
	rest := c[pos+1:]
	i := bytes.IndexByte(rest, 0)
	if string(rest[:i]) != "file.txt" {
		t.Fatalf("name = %q", rest[:i])
	}
	rest = rest[i+1:]
	i = bytes.IndexByte(rest, 0)
	if want := "5000 20261104123059 extra"; string(rest[:i]) != want {
		t.Fatalf("comment = %q, want %q", rest[:i], want)
	}
	if rest[i+1] != 0 {
		t.Fatalf("reserved byte = %d", rest[i+1])
	}
	// trailer: 4 zeros, 253, SHA-1, 255
	n := len(c)
	if c[n-1] != 255 {
		t.Fatalf("missing end-of-block byte")
	}
	sum := sha1.Sum(data)
	if c[n-22] != 253 || !bytes.Equal(c[n-21:n-1], sum[:]) {
		t.Fatalf("bad checksum trailer")
	}
	if !bytes.Equal(c[n-26:n-22], []byte{0, 0, 0, 0}) {
		t.Fatalf("missing 4 zero bytes before the checksum")
	}
	if bytes.Contains(c[pos:n-26], []byte{0, 0, 0, 0}) {
		t.Fatalf("coded data contains 4 consecutive zero bytes")
	}
}

func TestNoChecksum(t *testing.T) {
	data := textBytes(2, 3000)
	c := mustCompress(t, data, WithChecksum(false))
	if c[len(c)-2] != 254 || c[len(c)-1] != 255 {
		t.Fatalf("expected 254 255 trailer, got % x", c[len(c)-2:])
	}
	if !bytes.Equal(mustDecompress(t, c), data) {
		t.Fatal("round trip failed")
	}
}

func TestStoredLayout(t *testing.T) {
	data := randomBytes(3, 70000) // two chunks: 65536 + 4464 (+1 for the PCOMP byte)
	c := mustCompress(t, data, WithLevel(Store))
	if c[16] != 2 {
		t.Fatalf("level byte = %d, want 2 for a block without components", c[16])
	}
	if !bytes.Equal(mustDecompress(t, c), data) {
		t.Fatal("round trip failed")
	}
	// An incompressible input at a modeling level falls back to stored chunks.
	c2 := mustCompress(t, data, WithLevel(Best))
	if c2[16] != 2 || len(c2) > len(data)+200 {
		t.Fatalf("expected stored fallback, level byte %d, size %d", c2[16], len(c2))
	}
	if !bytes.Equal(mustDecompress(t, c2), data) {
		t.Fatal("round trip failed")
	}
}

func TestMultiBlockOnlyFirstNamed(t *testing.T) {
	data := textBytes(4, 10000)
	c := mustCompress(t, data, WithBlockSize(4000), WithName("a.txt"))
	var names, comments []string
	var total int
	err := Walk(bytes.NewReader(c), func(name, comment string, d []byte) error {
		names = append(names, name)
		comments = append(comments, comment)
		total += len(d)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(names, "|"), "a.txt||"; got != want {
		t.Fatalf("names = %q, want %q", got, want)
	}
	if got, want := strings.Join(comments, "|"), "4000|4000|2000"; got != want {
		t.Fatalf("comments = %q, want %q", got, want)
	}
	if total != len(data) {
		t.Fatalf("total = %d", total)
	}
	if !bytes.Equal(mustDecompress(t, c), data) {
		t.Fatal("round trip failed")
	}
}

func TestConcatenatedArchivesAndGarbage(t *testing.T) {
	a, b := textBytes(5, 2000), randomBytes(6, 1500)
	ca, cb := mustCompress(t, a), mustCompress(t, b, WithLevel(Store))
	in := append(append(append([]byte("junk before"), ca...), []byte("\x00\xff garbage between")...), cb...)
	in = append(in, "trailing junk"...)
	if _, err := Decompress(nil, in); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("garbage accepted by default: %v", err)
	}
	got := mustDecompress(t, in, WithSkipGarbage(true))
	if !bytes.Equal(got, append(append([]byte(nil), a...), b...)) {
		t.Fatalf("got %d bytes", len(got))
	}
	// Directly concatenated archives need no option.
	got = mustDecompress(t, append(append([]byte(nil), ca...), cb...))
	if !bytes.Equal(got, append(append([]byte(nil), a...), b...)) {
		t.Fatalf("concatenation: got %d bytes", len(got))
	}
	// trailing garbage is rejected too
	if _, err := Decompress(nil, append(append([]byte(nil), ca...), 0)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("trailing byte accepted: %v", err)
	}
}

func TestNoBlockIsError(t *testing.T) {
	for _, in := range [][]byte{nil, {}, []byte("not a zpaq archive at all"), blockTag[:15]} {
		if _, err := Decompress(nil, in); !errors.Is(err, ErrCorrupt) {
			t.Errorf("Decompress(%q) error = %v, want ErrCorrupt", in, err)
		}
	}
}

// TestMultiSegmentBlock builds a block with three segments by hand: the
// predictor state continues across segments and only the first carries the
// PCOMP prefix byte.
func TestMultiSegmentBlock(t *testing.T) {
	z := testModel(t, order1Config)
	p1, p2, p3 := textBytes(7, 3000), []byte{}, textBytes(8, 2500)
	first := append([]byte{0}, p1...) // 0 = no post-processing
	blk := manualBlock(t, z,
		manualSeg{name: "one", comment: "c1", payload: first, sum: sum1(p1)},
		manualSeg{payload: p2, sum: sum1(p2)},
		manualSeg{name: "three", payload: p3},
	)
	var got []string
	var all []byte
	err := Walk(bytes.NewReader(blk), func(name, comment string, d []byte) error {
		got = append(got, name+"/"+comment)
		all = append(all, d...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "one/c1,/,three/" {
		t.Fatalf("segments = %v", got)
	}
	want := append(append(append([]byte(nil), p1...), p2...), p3...)
	if !bytes.Equal(all, want) {
		t.Fatal("content mismatch")
	}
	// A wrong checksum is detected.
	bad := manualBlock(t, z, manualSeg{payload: first, sum: sum1(append([]byte("x"), p1...))})
	if _, err := Decompress(nil, bad); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("bad checksum: err = %v", err)
	}
}

// TestPostProcessor exercises the PCOMP path: segment 1 carries a program that
// XORs every byte with 0x55 and the stored data is the XORed text.
func TestPostProcessor(t *testing.T) {
	z := testModel(t, order1Config)
	// a> 255; jt +3; a^= 85; out; halt; END
	pcomp := []byte{239, 255, 39, 3, 199, 85, 57, 56, 0}
	plain := textBytes(9, 4000)
	xored := make([]byte, len(plain))
	for i, c := range plain {
		xored[i] = c ^ 0x55
	}
	payload := append([]byte{1, byte(len(pcomp)), byte(len(pcomp) >> 8)}, pcomp...)
	payload = append(payload, xored...)
	blk := manualBlock(t, z,
		manualSeg{name: "x", payload: payload, sum: sum1(plain)},
		manualSeg{payload: xored[:100], sum: sum1(plain[:100])}, // continues in the same state
	)
	got := mustDecompress(t, blk)
	want := append(append([]byte(nil), plain...), plain[:100]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("post-processed output differs (%d vs %d bytes)", len(got), len(want))
	}
}

func TestPostProcessorErrors(t *testing.T) {
	z := testModel(t, order1Config)
	for name, payload := range map[string][]byte{
		"unknown type":  {2, 'a'},
		"empty pcomp":   {1, 0, 0},
		"truncated":     {1, 5, 0, 1, 2},
		"bad opcode":    {1, 2, 0, 0, 0, 'x'}, // opcode 0 = ERROR
		"no marker":     {},
		"never halting": {1, 4, 0, 63, 254, 56, 0, 'x'}, // jmp -1 loops forever
	} {
		t.Run(name, func(t *testing.T) {
			blk := manualBlock(t, z, manualSeg{payload: payload})
			_, err := Decompress(nil, blk)
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestPCompWriteTo(t *testing.T) {
	// Reader and Decompress agree on a multi-block archive.
	data := textBytes(10, 50000)
	c := mustCompress(t, data, WithBlockSize(9000))
	r := NewReader(bytes.NewReader(c))
	defer r.Close()
	var buf bytes.Buffer
	n, err := io.Copy(&buf, r)
	if err != nil || n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("io.Copy: n=%d err=%v", n, err)
	}
}

func TestNameAndCommentSanitised(t *testing.T) {
	data := []byte("hello world")
	arc := mustCompress(t, data, WithName("a\x00b"), WithComment("c\x00d jDC\x01"), WithLevel(Store))
	var names, comments []string
	err := Walk(bytes.NewReader(arc), func(name, comment string, d []byte) error {
		names = append(names, name)
		comments = append(comments, comment)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "ab" {
		t.Fatalf("names = %q", names)
	}
	if strings.HasSuffix(comments[0], "jDC\x01") || !strings.HasPrefix(comments[0], "11 cd jDC") {
		t.Fatalf("comment = %q", comments[0])
	}
}
