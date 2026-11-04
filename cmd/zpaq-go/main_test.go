package main

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func exec(t *testing.T, stdin []byte, args ...string) (stdout, stderr []byte, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, bytes.NewReader(stdin), &o, &e)
	return o.Bytes(), e.Bytes(), code
}

func sample() []byte {
	r := rand.New(rand.NewSource(7))
	var b bytes.Buffer
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	for b.Len() < 40000 {
		b.WriteString(words[r.Intn(len(words))])
		b.WriteByte(' ')
	}
	return b.Bytes()
}

func TestStdinStdoutRoundTrip(t *testing.T) {
	data := sample()
	for _, level := range []string{"0", "1", "4"} {
		comp, errOut, code := exec(t, data, "c", "-level", level)
		if code != 0 {
			t.Fatalf("level %s: code %d: %s", level, code, errOut)
		}
		got, errOut, code := exec(t, comp, "d")
		if code != 0 || !bytes.Equal(got, data) {
			t.Fatalf("level %s: decompress code %d: %s", level, code, errOut)
		}
	}
}

func TestFilesAndList(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "notes.txt")
	data := sample()
	if err := os.WriteFile(in, data, 0o600); err != nil {
		t.Fatal(err)
	}
	arc := filepath.Join(dir, "notes.zpaq")
	if _, e, code := exec(t, nil, "c", in, "-o", arc, "-level", "2", "-j", "2"); code != 0 {
		t.Fatalf("compress: %d %s", code, e)
	}
	if st, err := os.Stat(arc); err != nil {
		t.Fatalf("archive stat: %v", err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm() != 0o644 {
		t.Fatalf("archive mode %v, want 0644", st.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".zpaq-go-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	out, _, code := exec(t, nil, "l", arc)
	if code != 0 || !strings.Contains(string(out), "notes.txt") || !strings.Contains(string(out), "1 segment(s)") {
		t.Fatalf("list output (code %d):\n%s", code, out)
	}
	out, _, code = exec(t, nil, "t", arc)
	if code != 0 || !strings.HasPrefix(string(out), "OK: 1 segment(s), 40") {
		t.Fatalf("test output (code %d): %s", code, out)
	}
	back := filepath.Join(dir, "back.txt")
	if _, e, code := exec(t, nil, "d", "-o", back, arc); code != 0 {
		t.Fatalf("decompress: %d %s", code, e)
	}
	got, _ := os.ReadFile(back)
	if !bytes.Equal(got, data) {
		t.Fatal("round trip mismatch")
	}
}

func TestExplicitNameAndBlocks(t *testing.T) {
	data := sample()
	comp, _, code := exec(t, data, "c", "-name", "custom.bin", "-block", "1", "-level", "1")
	if code != 0 {
		t.Fatal(code)
	}
	out, _, _ := exec(t, comp, "l")
	if !strings.Contains(string(out), "custom.bin") {
		t.Fatalf("name missing:\n%s", out)
	}
}

func TestFlagsAfterFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "x")
	os.WriteFile(in, sample(), 0o600)
	if _, e, code := exec(t, nil, "c", in, "-level", "1", "-o", filepath.Join(dir, "x.zpaq")); code != 0 {
		t.Fatalf("%d %s", code, e)
	}
}

func TestFailuresLeaveNoOutput(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.zpaq")
	os.WriteFile(bad, []byte("this is not a zpaq archive at all"), 0o600)
	dst := filepath.Join(dir, "out.bin")
	_, e, code := exec(t, nil, "d", "-o", dst, bad)
	if code != 1 || len(e) == 0 {
		t.Fatalf("code %d stderr %q", code, e)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("output should not exist: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".zpaq-go-*")); len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
	if _, _, code := exec(t, nil, "t", bad); code != 1 {
		t.Fatalf("test of bad archive: code %d", code)
	}
	if _, _, code := exec(t, nil, "c", filepath.Join(dir, "missing")); code != 1 {
		t.Fatalf("missing input: code %d", code)
	}
}

func TestTruncatedArchive(t *testing.T) {
	comp, _, _ := exec(t, sample(), "c")
	if _, _, code := exec(t, comp[:len(comp)/2], "d"); code != 1 {
		t.Fatalf("truncated: code %d", code)
	}
}

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		nil,
		{"bogus"},
		{"c", "-level", "9"},
		{"c", "-level", "-1"},
		{"c", "-block", "-3"},
		{"c", "a", "b"},
		{"c", "-nosuchflag"},
		{"d", "-nosuchflag"},
	}
	for _, args := range cases {
		if _, _, code := exec(t, nil, args...); code != 2 {
			t.Errorf("%q: code %d, want 2", args, code)
		}
	}
	if out, _, code := exec(t, nil, "help"); code != 0 || !strings.Contains(string(out), "usage:") {
		t.Errorf("help: %d %q", code, out)
	}
	if _, _, code := exec(t, nil, "c", "-h"); code != 0 {
		t.Errorf("-h: code %d", code)
	}
}

func TestSkipGarbageFlag(t *testing.T) {
	data := sample()
	comp, _, _ := exec(t, data, "c")
	padded := append([]byte("junk bytes"), comp...)
	if _, _, code := exec(t, padded, "d"); code != 1 {
		t.Fatalf("strict mode accepted garbage: code %d", code)
	}
	got, _, code := exec(t, padded, "d", "-skip-garbage")
	if code != 0 || !bytes.Equal(got, data) {
		t.Fatalf("skip-garbage: code %d", code)
	}
}
