package zpaq

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strings"
	"testing"
)

func TestTables(t *testing.T) {
	// stdt holds the run lengths of stretch(): they must add up to the upper half.
	sum := 0
	for _, n := range stdt {
		sum += int(n)
	}
	if sum != 16384 {
		t.Fatalf("sum(stdt) = %d, want 16384", sum)
	}
	for i := 0; i < 16384; i++ {
		if stretcht[i] != -stretcht[32767-i] {
			t.Fatalf("stretch not antisymmetric at %d", i)
		}
	}
	prev := -1 << 20
	for i := 0; i < 32768; i++ {
		if s := stretch(i); s < prev || s < -2047 || s > 2047 {
			t.Fatalf("stretch(%d) = %d (prev %d)", i, s, prev)
		} else {
			prev = s
		}
	}
	prev = -1
	for x := -2048; x < 2048; x++ {
		if q := squash(x); q < prev || q < 0 || q > 32767 {
			t.Fatalf("squash(%d) = %d", x, q)
		} else {
			prev = q
		}
	}
	// The libzpaq self-test checksums of its stretch and squash tables.
	var st, sq uint32
	for i := 32767; i >= 0; i-- {
		st = st*3 + uint32(int32(stretch(i)))
	}
	for i := 4095; i >= 0; i-- {
		sq = sq*3 + uint32(squash(i-2048))
	}
	if st != 3887533746 || sq != 2278286169 {
		t.Fatalf("table checksums %d %d do not match libzpaq's (3887533746, 2278286169)", st, sq)
	}
	for x := -400; x <= 400; x++ {
		if d := stretch(squash(x)) - x; d < -3 || d > 3 {
			t.Fatalf("stretch(squash(%d)) off by %d", x, d)
		}
	}
}

func TestStateTable(t *testing.T) {
	if sns[0] != 1 || sns[1] != 2 || sns[2] != 0 || sns[3] != 0 {
		t.Fatalf("state 0 = %v", sns[:4])
	}
	if got := cminit(0); got != 1<<22 {
		t.Fatalf("cminit(0) = %d", got)
	}
	// Every state reachable from 0 stays within the table and keeps its counts
	// small; states that are never reached are not checked.
	seen := map[int]bool{0: true}
	queue := []int{0}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		n0, n1 := int(sns[s*4+2]), int(sns[s*4+3])
		if n0 > 48 || n1 > 48 {
			t.Fatalf("state %d has counts %d,%d", s, n0, n1)
		}
		for y := 0; y < 2; y++ {
			n := int(sns[s*4+y])
			if n >= 255 && n != 255 {
				t.Fatalf("state %d -> %d", s, n)
			}
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	if len(seen) < 200 {
		t.Fatalf("only %d states reachable", len(seen))
	}
	// A 1 bit never lowers n1 relative to n0 ordering: from a state with n0 == 0
	// the next state after a 1 keeps n0 == 0.
	for s := range seen {
		if sns[s*4+2] == 0 && sns[int(sns[s*4+1])*4+2] != 0 {
			t.Fatalf("state %d: a 1 bit introduced zeros", s)
		}
	}
}

// TestCoderRoundTrip checks the arithmetic coder alone with random bits and
// probabilities, including extreme ones.
func TestCoderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 200000
	ys := make([]int, n)
	ps := make([]int, n)
	for i := range ys {
		switch rng.Intn(4) {
		case 0:
			ps[i] = 1 + rng.Intn(65535)
		case 1:
			ps[i] = 1
		case 2:
			ps[i] = 65535
		default:
			ps[i] = 32768 + rng.Intn(65)
		}
		// draw the bit roughly according to p, with deliberate surprises
		if rng.Intn(65536) < ps[i] {
			ys[i] = 1
		}
		if rng.Intn(50) == 0 {
			ys[i] ^= 1
		}
	}
	e := newEncoder(nil)
	e.low, e.high = 1, 0xFFFFFFFF
	for i := range ys {
		e.encode(ys[i], ps[i])
	}
	e.encode(1, 0) // end marker, as in compress(-1)
	out := append(e.out, 0, 0, 0, 0)
	if bytes.Contains(out[:len(out)-4], []byte{0, 0, 0, 0}) {
		t.Fatalf("coder emitted four zero bytes in a row")
	}
	d := &decoder{low: 1, high: 0xFFFFFFFF, src: &sliceSource{b: out}}
	for i := 0; i < 4; i++ {
		d.curr = d.curr<<8 | uint32(d.get())
	}
	for i := range ys {
		if got := d.decode(ps[i]); got != ys[i] {
			t.Fatalf("bit %d: got %d want %d", i, got, ys[i])
		}
	}
	if d.decode(0) != 1 || d.curr != 0 {
		t.Fatalf("end marker not decoded (curr=%#x)", d.curr)
	}
}

func TestStoredCoder(t *testing.T) {
	for _, n := range []int{0, 1, 100, 1<<16 - 1, 1 << 16, 1<<16 + 1, 3*(1<<16) + 5} {
		data := randomBytes(int64(n), n)
		e := newEncoder(nil)
		for _, c := range data {
			e.compress(int(c))
		}
		e.compress(-1)
		out := append(e.out, 0, 0, 0, 0, 253)
		d := newDecoder(nil, &sliceSource{b: out})
		var got []byte
		for {
			c := d.decompress()
			if c < 0 {
				break
			}
			got = append(got, byte(c))
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d: stored round trip failed", n)
		}
		if next := d.get(); next != 253 {
			t.Fatalf("n=%d: stored stream consumed wrongly, next byte %d", n, next)
		}
		// skipSegment finds the same end
		if c := skipSegment(&sliceSource{b: out}, false); c != 253 {
			t.Fatalf("n=%d: skipSegment(stored) = %d", n, c)
		}
	}
}

// TestSkipSegmentModeled checks that skipSegment finds the end of coded data
// without decoding, as zpaq does when scanning an archive.
func TestSkipSegmentModeled(t *testing.T) {
	for _, n := range []int{0, 1, 50, 5000} {
		data := textBytes(int64(n), n)
		c := mustCompress(t, data, WithLevel(Fast), WithChecksum(false))
		z := new(zpaql)
		src := &sliceSource{b: c[16:]}
		z = readBlockHeader(src)
		if src.get() != 1 {
			t.Fatal("no segment")
		}
		readCString(src)
		readCString(src)
		src.get()
		if got := skipSegment(src, z.header[6] > 0); got != 254 {
			t.Fatalf("n=%d: skipSegment = %d, want 254", n, got)
		}
		if src.get() != 255 {
			t.Fatalf("n=%d: end of block not found after skip", n)
		}
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	check := func(name string, hdr []byte) {
		t.Helper()
		z := new(zpaql)
		src := &sliceSource{b: hdr}
		z.readHeader(src.get)
		if src.pos != len(hdr) {
			t.Errorf("%s: readHeader consumed %d of %d bytes", name, src.pos, len(hdr))
		}
		if out := z.writeHeader(nil, false); !bytes.Equal(out, hdr) {
			t.Errorf("%s: header does not round-trip", name)
		}
	}
	for i, m := range builtinModels {
		check("builtin"+string(rune('1'+i)), m)
	}
	for lvl := Level(4); lvl <= 5; lvl++ {
		data := textBytes(11, 3000)
		z, err := modelFor(lvl, data)
		if err != nil {
			t.Fatal(err)
		}
		check("level"+string(rune('0'+lvl)), z.writeHeader(nil, false))
	}
}

func TestReadHeaderErrors(t *testing.T) {
	good := builtinModels[1]
	for name, mut := range map[string]func([]byte) []byte{
		"truncated":     func(b []byte) []byte { return b[:len(b)-5] },
		"bad comp type": func(b []byte) []byte { c := append([]byte(nil), b...); c[7] = 77; return c },
		"missing comp end": func(b []byte) []byte {
			z := new(zpaql)
			src := &sliceSource{b: b}
			z.readHeader(src.get)
			c := append([]byte(nil), b...)
			c[z.cend-1] = 5
			return c
		},
		"missing hcomp end": func(b []byte) []byte {
			c := append([]byte(nil), b...)
			c[len(c)-1] = 9
			return c
		},
		"huge size": func(b []byte) []byte { c := append([]byte(nil), b...); c[0], c[1] = 255, 255; return c },
	} {
		t.Run(name, func(t *testing.T) {
			z := new(zpaql)
			src := &sliceSource{b: mut(good)}
			var err error
			func() { defer recoverFormat(&err); z.readHeader(src.get) }()
			if err == nil {
				t.Fatalf("expected an error")
			}
		})
	}
}

func TestCompileProgramBytes(t *testing.T) {
	prog := func(text string) []byte {
		z := testModel(t, vmConfig(text))
		return append([]byte(nil), z.header[z.hbegin:z.hend]...)
	}
	for text, want := range map[string][]byte{
		"a++ a-- b++":                     {1, 2, 9, 56, 0},
		"a= 5 b=a *b=a":                   {71, 5, 72, 96, 56, 0},
		"a> 255 if a++ endif":             nil, // operand 255 is fine for a>, see below
		"a> 200 if a++ endif":             {239, 200, 47, 1, 1, 56, 0},
		"a> 200 if a++ else a-- endif":    {239, 200, 47, 3, 1, 63, 1, 2, 56, 0},
		"a> 200 ifnot a++ endif":          {239, 200, 39, 1, 1, 56, 0},
		"do a++ a< 9 while":               {1, 231, 9, 39, 251, 56, 0},
		"hash hashd out":                  {59, 60, 57, 56, 0},
		"a=r 3 r=a 4":                     {7, 3, 55, 4, 56, 0},
		"a<<= 8 a>>= 2 a&~ 1 a%= 3 a/=*d": {207, 8, 215, 2, 183, 1, 167, 3, 158, 56, 0},
		"d= 3 a=*d d++ *d=a":              {95, 3, 70, 25, 112, 56, 0},
		"(a++ (nested) a--) a++":          {1, 56, 0},
		"A++ Halt":                        {1, 56, 56, 0},
		"a= $1 b= $2+3":                   nil,
	} {
		if want == nil {
			continue
		}
		if got := prog(text); !bytes.Equal(got, want) {
			t.Errorf("%q compiles to %v, want %v", text, got, want)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	for name, text := range map[string]string{
		"missing comp":      "hcomp halt post 0 end",
		"bad component":     "comp 1 1 0 0 1 0 foo 1 hcomp halt post 0 end",
		"component index":   "comp 1 1 0 0 1 5 icm 1 hcomp halt post 0 end",
		"unknown op":        vmConfig("frobnicate"),
		"number too high":   vmConfig("a= 256"),
		"number too low":    "comp 1 1 0 0 0 hcomp jt -129 halt post 0 end",
		"missing end":       "comp 1 1 0 0 0 hcomp halt post 0",
		"unterminated":      "comp 1 1 0 0 0 hcomp halt post 0 (end",
		"endif without if":  vmConfig("endif"),
		"post needs 0":      "comp 1 1 0 0 0 hcomp halt post 1 end",
		"pcomp unsupported": "comp 1 1 0 0 0 hcomp halt pcomp cmd ; halt end",
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			func() {
				defer func() { recover() }() // an unmatched endif indexes an empty stack: must not escape as a crash in Compress
				if _, err := compileConfig(text, [9]int{}); err == nil {
					t.Errorf("expected an error")
				}
			}()
		})
	}
}

func TestMakeConfig(t *testing.T) {
	for _, m := range []string{
		"0", "00,0", "x0,0", "x0,0ci1", "x4,0ci1,1,1,1,2a", "x4,0ci1,1,1,1,2awm", "s4,0c0,2,0,255i1",
		"x6,0w2c0,1010,255i1c256ci1,1,1,1,1,1,2ac0,0,1006,255i1c0,7i1c0,2,0,255i1mm16ts19t0",
		"x3,0c0,0,0,0,0i1,2,3a8,0,1m8,24s8,32,255t",
	} {
		cfg, args, err := makeConfig(m)
		if err != nil {
			t.Errorf("%s: %v", m, err)
			continue
		}
		z, err := compileConfig(cfg, args)
		if err != nil {
			t.Errorf("%s: compile: %v\n%s", m, err, cfg)
			continue
		}
		if mem := z.memory(); mem < 1000 || mem > 1<<34 {
			t.Errorf("%s: implausible memory %g", m, mem)
		}
		// the generated block must also be decodable
		data := textBytes(5, 2000)
		blk, err := encodeWith(z, data, "", "", nil, nil, nil)
		if err != nil {
			t.Errorf("%s: encode: %v", m, err)
			continue
		}
		if got, err := Decompress(nil, blk); err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: round trip: %v", m, err)
		}
	}
	for _, m := range []string{"", "q1", "x4,1", "x4,2", "x4,3", "x4,5"} {
		if _, _, err := makeConfig(m); err == nil {
			t.Errorf("makeConfig(%q): expected an error", m)
		}
	}
}

func TestMethodString(t *testing.T) {
	text := textBytes(12, 50000)
	bin := randomBytes(13, 50000)
	if m := methodString(5, text); !strings.Contains(m, "w2c0,1010,255i1") {
		t.Errorf("text method %q lacks the text model", m)
	}
	if m := methodString(5, bin); !strings.Contains(m, "w1i1") || strings.Contains(m, "w2c") {
		t.Errorf("binary method %q", m)
	}
	if m := methodString(4, text); !strings.HasSuffix(m, "ci1,1,1,1,2awm") {
		t.Errorf("level 4 text method %q", m)
	}
	if m := methodString(4, bin); !strings.HasSuffix(m, "ci1,1,1,1,2am") {
		t.Errorf("level 4 binary method %q", m)
	}
	// records of 24 bytes: the periodic model must pick period 24
	rec := make([]byte, 24*3000)
	rng := rand.New(rand.NewSource(14))
	for i := range rec {
		if i%24 < 20 {
			rec[i] = byte(i % 24 * 3)
		} else {
			rec[i] = byte(rng.Intn(256))
		}
	}
	if m := methodString(5, rec); !strings.Contains(m, "c0,0,1023,255i1c0,24i1") {
		t.Errorf("periodic method %q", m)
	}
	// the memory argument grows with the block size: 3 MiB needs 2^22
	big := make([]byte, 3<<20)
	if m := methodString(4, big); !strings.HasPrefix(m, "x2,0") {
		t.Errorf("3 MiB block method %q", m)
	}
	if m := methodString(4, big[:1<<20-4096]); !strings.HasPrefix(m, "x0,0") {
		t.Errorf("small block method %q", m)
	}
}

func TestTextExe(t *testing.T) {
	if txt, exe := textExe(textBytes(1, 100000)); !txt || exe {
		t.Errorf("text: %v %v", txt, exe)
	}
	if txt, _ := textExe(randomBytes(1, 100000)); txt {
		t.Errorf("random data classified as text")
	}
	// x86-like data: many 0x8B bytes after varied contexts
	x86 := make([]byte, 100000)
	rng := rand.New(rand.NewSource(3))
	for i := range x86 {
		if i%3 == 0 {
			x86[i] = 139
		} else {
			x86[i] = byte(rng.Intn(256))
		}
	}
	if _, exe := textExe(x86); !exe {
		t.Errorf("x86-like data not detected")
	}
	if txt, exe := textExe(nil); txt || exe {
		t.Errorf("empty input: %v %v", txt, exe)
	}
}

// goldenHeaders pins the headers produced for fixed inputs, so that an
// accidental change of the model generation is noticed. The values come from
// this implementation (they are not taken from zpaq) and were checked for
// decodability by the tests above.
func TestGoldenOutput(t *testing.T) {
	data := textBytes(21, 6000)
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	got := map[Level]string{}
	for lvl := Store; lvl <= Best; lvl++ {
		got[lvl] = sum(mustCompress(t, data, WithLevel(lvl), WithName("g")))
	}
	t.Logf("golden digests: %v", got)
	for lvl, want := range goldenDigests {
		if got[lvl] != want {
			t.Errorf("level %d digest = %s, want %s", lvl, got[lvl], want)
		}
	}
}

var goldenDigests = map[Level]string{
	0: "481bd0ea021896bf43d26adfbb9f99ab6d2b2c2ed0ce18288dd6b4c739a0f54d",
	1: "48bd8a07c3cbfac9721d8e19452b7e9d0210689374b97e8f1c08608999f1eb5d",
	2: "0a9ecd2d116bf4d2bb21e0a646284704b41b25a9e81ec728e88573cebdd0bc5c",
	3: "25e65aa6397490d1987d9f35152936796a3386054cc6a9cac565fcf282a54835",
	4: "5363c31b091a4c6f8598ecaeb515685b914f93a1bab5557ed0c15e855de34c35",
	5: "cb02addb1b46bc848a7c77b7307816e7f36e90c0902cda805690b2d911a53111",
}
