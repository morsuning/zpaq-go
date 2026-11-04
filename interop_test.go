package zpaq

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run the reference zpaq program (https://mattmahoney.net/dc/zpaq.html,
// Debian/Ubuntu package "zpaq") against this package. They are skipped when no
// zpaq binary is found; set ZPAQ_BIN to point at a specific one.

func zpaqBin(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("ZPAQ_BIN"); p != "" {
		return p
	}
	p, err := exec.LookPath("zpaq")
	if err != nil {
		t.Skip("reference zpaq not found in PATH (set ZPAQ_BIN)")
	}
	return p
}

// runZpaq runs zpaq in dir and fails the test if it reports any error.
func runZpaq(t testing.TB, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	s := string(out)
	if err != nil || strings.Contains(s, "with errors") || strings.Contains(s, "zpaq error") {
		t.Fatalf("zpaq %v: %v\n%s", args, err, s)
	}
	return s
}

func interopInputs() map[string][]byte {
	// A tiny x86-like blob, so that zpaq's exe detection is exercised.
	exe := make([]byte, 0, 70000)
	for i := 0; len(exe) < 70000; i++ {
		exe = append(exe, 0xE8, byte(i), byte(i>>8), 0, 0, 0x55, 0x8B, 0xEC, byte(i*7), 0x90)
	}
	return map[string][]byte{
		"empty":   {},
		"onebyte": {'x'},
		"short":   []byte("hello, zpaq\n"),
		"text":    textBytes(11, 300_000),
		"random":  randomBytes(12, 100_000),
		"zeros":   make([]byte, 200_000),
		"exe":     exe,
		"mixed":   append(append(textBytes(13, 50_000), randomBytes(14, 30_000)...), textBytes(13, 50_000)...),
	}
}

// TestInteropZpaqExtractsOurArchives checks that the reference program
// extracts, byte for byte, archives written by this package.
func TestInteropZpaqExtractsOurArchives(t *testing.T) {
	bin := zpaqBin(t)
	levels := []Level{Store, 1, 2, 3, Default, Best}
	if testing.Short() {
		levels = []Level{Store, 1, Default}
	}
	for name, data := range interopInputs() {
		for _, level := range levels {
			if level == Best && len(data) > 150_000 {
				continue // keep the run time reasonable
			}
			for _, bs := range []int{0, 1 << 16} {
				t.Run(fmt.Sprintf("%s/L%d/bs%d", name, level, bs), func(t *testing.T) {
					if bs != 0 && len(data) < 3*bs/2 {
						t.Skip("single block anyway")
					}
					dir := t.TempDir()
					arc := mustCompress(t, data, WithLevel(level), WithBlockSize(bs), WithName("sub/dir/"+name+".bin"))
					if err := os.WriteFile(filepath.Join(dir, "a.zpaq"), arc, 0o644); err != nil {
						t.Fatal(err)
					}
					out := runZpaq(t, bin, dir, "x", "a.zpaq")
					if !strings.Contains(out, "all OK") {
						t.Fatalf("unexpected zpaq output:\n%s", out)
					}
					got, err := os.ReadFile(filepath.Join(dir, "sub", "dir", name+".bin"))
					if err != nil {
						t.Fatalf("%v\n%s", err, out)
					}
					if !bytes.Equal(got, data) {
						t.Fatalf("zpaq extracted %d bytes that differ from the %d input bytes", len(got), len(data))
					}
				})
			}
		}
	}
}

// TestInteropUnnamedAndConcatenated covers an archive without a file name
// (zpaq names the output after the archive) and two archives appended to
// each other (which zpaq treats as one archive holding two files).
func TestInteropUnnamedAndConcatenated(t *testing.T) {
	bin := zpaqBin(t)
	dir := t.TempDir()
	d1, d2 := textBytes(21, 40_000), randomBytes(22, 5_000)

	if err := os.WriteFile(filepath.Join(dir, "plain.zpaq"), mustCompress(t, d1), 0o644); err != nil {
		t.Fatal(err)
	}
	runZpaq(t, bin, dir, "x", "plain.zpaq")
	if got, err := os.ReadFile(filepath.Join(dir, "plain")); err != nil || !bytes.Equal(got, d1) {
		t.Fatalf("unnamed archive: err=%v equal=%v", err, bytes.Equal(got, d1))
	}

	both := mustCompress(t, d1, WithName("one.txt"), WithModTime(time.Date(2026, 11, 4, 12, 30, 5, 0, time.UTC)))
	both = append(both, mustCompress(t, d2, WithName("two.bin"), WithComment("note"), WithLevel(2))...)
	if err := os.WriteFile(filepath.Join(dir, "both.zpaq"), both, 0o644); err != nil {
		t.Fatal(err)
	}
	out := runZpaq(t, bin, dir, "x", "both.zpaq")
	for name, want := range map[string][]byte{"one.txt": d1, "two.bin": d2} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: err=%v equal=%v\n%s", name, err, bytes.Equal(got, want), out)
		}
	}
}

// TestInteropWriterStream checks the streaming Writer output with zpaq.
func TestInteropWriterStream(t *testing.T) {
	bin := zpaqBin(t)
	dir := t.TempDir()
	data := textBytes(31, 500_000)
	var buf bytes.Buffer
	w := NewWriter(&buf, WithName("stream.txt"), WithBlockSize(100_000), WithLevel(2), WithConcurrency(3))
	for off := 0; off < len(data); off += 7777 {
		if _, err := w.Write(data[off:min(off+7777, len(data))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.zpaq"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	runZpaq(t, bin, dir, "x", "s.zpaq")
	if got, err := os.ReadFile(filepath.Join(dir, "stream.txt")); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("writer stream: err=%v equal=%v", err, bytes.Equal(got, data))
	}
}

// TestInteropWeDecodeZpaqArchives creates journaling archives with the
// reference program at several methods and decodes them with Walk, which
// verifies the SHA-1 stored with every segment. For inputs that do not
// deduplicate, the "d" (data) blocks must also hold the original bytes.
func TestInteropWeDecodeZpaqArchives(t *testing.T) {
	bin := zpaqBin(t)
	methods := []string{"0", "1", "2", "3", "4", "5", "14", "26", "x0,0,0", "5,0,2", "4,1,0"}
	if testing.Short() {
		methods = []string{"1", "3", "4"}
	}
	inputs := map[string][]byte{
		"text":   textBytes(41, 200_000),
		"random": randomBytes(42, 80_000),
		"exe":    interopInputs()["exe"],
		"short":  []byte("tiny"),
	}
	for name, data := range inputs {
		for _, m := range methods {
			t.Run(name+"/m"+m, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "in.bin"), data, 0o644); err != nil {
					t.Fatal(err)
				}
				runZpaq(t, bin, dir, "a", "ref.zpaq", "in.bin", "-method", m)
				raw, err := os.ReadFile(filepath.Join(dir, "ref.zpaq"))
				if err != nil {
					t.Fatal(err)
				}
				var dataBlocks bytes.Buffer
				segs := 0
				err = Walk(bytes.NewReader(raw), func(segName, comment string, seg []byte) error {
					segs++
					if len(segName) == 28 && strings.HasPrefix(segName, "jDC") && segName[17] == 'd' {
						frags, err := journalFragments(seg)
						if err != nil {
							return err
						}
						dataBlocks.Write(frags)
					}
					return nil
				})
				if err != nil {
					t.Fatalf("Walk: %v", err)
				}
				if segs < 3 {
					t.Fatalf("only %d segments", segs)
				}
				if !bytes.Equal(dataBlocks.Bytes(), data) {
					t.Fatalf("data blocks hold %d bytes, want the %d input bytes", dataBlocks.Len(), len(data))
				}
			})
		}
	}
}

// journalFragments strips the trailer of a zpaq journaling data block. The
// fragments are followed by one little-endian uint32 size per fragment, a
// zero word (the omitted first fragment ID) and the fragment count.
func journalFragments(seg []byte) ([]byte, error) {
	if len(seg) < 8 {
		return nil, fmt.Errorf("data block of %d bytes", len(seg))
	}
	n := int(binary.LittleEndian.Uint32(seg[len(seg)-4:]))
	if n < 0 || 4*n+8 > len(seg) {
		return nil, fmt.Errorf("bad fragment count %d in a %d byte block", n, len(seg))
	}
	if binary.LittleEndian.Uint32(seg[len(seg)-8:]) != 0 {
		return nil, fmt.Errorf("first fragment ID is not zero")
	}
	body := seg[:len(seg)-8-4*n]
	sum := 0
	for i := 0; i < n; i++ {
		sum += int(binary.LittleEndian.Uint32(seg[len(body)+4*i:]))
	}
	if sum != len(body) {
		return nil, fmt.Errorf("fragment sizes add up to %d, block holds %d bytes", sum, len(body))
	}
	return body, nil
}

// TestInteropRepeatedContent decodes a deduplicating journaling archive made
// of repeated data: the stored bytes must still decode and verify even though
// they are shorter than the input.
func TestInteropRepeatedContent(t *testing.T) {
	bin := zpaqBin(t)
	dir := t.TempDir()
	chunk := randomBytes(51, 200_000)
	if err := os.WriteFile(filepath.Join(dir, "in.bin"), bytes.Repeat(chunk, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	runZpaq(t, bin, dir, "a", "ref.zpaq", "in.bin", "-method", "3")
	raw, err := os.ReadFile(filepath.Join(dir, "ref.zpaq"))
	if err != nil {
		t.Fatal(err)
	}
	var total int
	if err := Walk(bytes.NewReader(raw), func(_, _ string, seg []byte) error {
		total += len(seg)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("no data decoded")
	}
}
