package zpaq

import (
	"errors"
	"strings"
	"testing"
)

// vmConfig wraps a program in a config with 16-entry H and 256-entry M arrays.
func vmConfig(prog string) string {
	return "comp 4 8 0 0 0 hcomp " + prog + " halt post 0 end"
}

func runVM(t *testing.T, prog string, a uint32, setup func(z *zpaql)) *zpaql {
	t.Helper()
	z := testModel(t, vmConfig(prog))
	z.inith()
	if setup != nil {
		setup(z)
	}
	z.run(a)
	return z
}

func TestVMRegisters(t *testing.T) {
	tests := []struct {
		name string
		prog string
		in   uint32
		want func(z *zpaql) bool
	}{
		{"inc/dec/not", "a++ a++ b++ c-- d! ", 5, func(z *zpaql) bool { return z.a == 7 && z.b == 1 && z.c == 0xFFFFFFFF && z.d == 0xFFFFFFFF }},
		{"zero", "a=0", 99, func(z *zpaql) bool { return z.a == 0 }},
		{"load imm", "a= 200 b= 7 c= 8 d= 9", 0, func(z *zpaql) bool { return z.a == 200 && z.b == 7 && z.c == 8 && z.d == 9 }},
		{"swap", "b= 3 b<>a c= 4 c<>a", 10, func(z *zpaql) bool { return z.a == 4 && z.b == 10 && z.c == 3 }},
		{"arith", "a= 10 a+= 5 a-= 3 a*= 4 a/= 5 a%= 7", 0, func(z *zpaql) bool { return z.a == 2 }},
		{"div by zero", "a= 100 a/= 0", 0, func(z *zpaql) bool { return z.a == 0 }},
		{"mod by zero", "a= 100 a%= 0", 0, func(z *zpaql) bool { return z.a == 0 }},
		{"bitwise", "a= 204 a&= 170 b=a a= 204 a|= 170 c=a a= 204 a^= 170 d=a a= 15 a&~ 5", 0,
			func(z *zpaql) bool { return z.b == 136 && z.c == 238 && z.d == 102 && z.a == 10 }},
		{"shifts masked to 5 bits", "a= 1 b= 33 a<<=b", 0, func(z *zpaql) bool { return z.a == 2 }},
		{"shift right", "a= 255 a>>= 4", 0, func(z *zpaql) bool { return z.a == 15 }},
		{"wraparound", "a=0 a-- a++ a--", 0, func(z *zpaql) bool { return z.a == 0xFFFFFFFF }},
		{"multiply wraps", "a= 255 a*=a a*=a a*=a", 0, func(z *zpaql) bool {
			x := uint32(255)
			for i := 0; i < 3; i++ {
				x *= x
			}
			return z.a == x && x != 0
		}},
		{"registers r", "a= 77 r=a 5 a=0 a=r 5 b=r 5 c=r 5 d=r 5", 0, func(z *zpaql) bool { return z.a == 77 && z.b == 77 && z.c == 77 && z.d == 77 && z.r[5] == 77 }},
		{"hash", "a= 3 b= 5 *b= 9 hash", 0, func(z *zpaql) bool { return z.a == (3+9+512)*773 }},
		{"hashd", "d= 2 *d= 6 a= 4 hashd", 0, func(z *zpaql) bool { return z.h[2] == (6+4+512)*773 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := runVM(t, tt.prog, tt.in, nil)
			if !tt.want(z) {
				t.Fatalf("unexpected state a=%d b=%d c=%d d=%d f=%v", z.a, z.b, z.c, z.d, z.f)
			}
		})
	}
}

func TestVMMemory(t *testing.T) {
	z := runVM(t, "b= 3 a= 77 *b=a *b++ *b++ c= 3 a=*c b= 4 *b= 255 a+=*b", 0, nil)
	if z.m[3] != 79 || z.a != 79+255 {
		t.Fatalf("m[3]=%d a=%d", z.m[3], z.a)
	}
	// M and H indices wrap at the array sizes (256 and 16 here).
	z = runVM(t, "b= 200 a= 5 *b=a d= 3 *d= 8 d=0 d++ d++ d++ a=*d", 0, func(z *zpaql) { z.b = 0 })
	if z.m[200] != 5 || z.a != 8 {
		t.Fatalf("m[200]=%d a=%d", z.m[200], z.a)
	}
	z = runVM(t, "b= 255 b++ b++ a= 9 *b=a", 0, nil)
	if z.b != 257 || z.m[1] != 9 {
		t.Fatalf("b=%d m[1]=%d", z.b, z.m[1])
	}
	z = runVM(t, "d= 0 d-- *d= 1", 0, nil)
	if z.h[15] != 1 {
		t.Fatalf("h[15]=%d", z.h[15])
	}
}

// The byte swap *B<>A exchanges only the low byte of A (libzpaq's xor swap).
func TestVMByteSwapKeepsHighBytes(t *testing.T) {
	z := runVM(t, "a= 1 a<<= 8 a+= 2 a<<= 8 a+= 3 b= 7 *b= 170 *b<>a", 0, nil)
	if z.a != 0x0102AA || z.m[7] != 3 {
		t.Fatalf("a=%#x m[7]=%d", z.a, z.m[7])
	}
	z = runVM(t, "a= 1 a<<= 8 a+= 2 a<<= 8 a+= 3 c= 9 *c= 170 *c<>a", 0, nil)
	if z.a != 0x0102AA || z.m[9] != 3 {
		t.Fatalf("*c<>a: a=%#x m[9]=%d", z.a, z.m[9])
	}
	// *D<>A and the register swaps exchange whole 32-bit words
	z = runVM(t, "a= 1 a<<= 8 a+= 2 d= 5 *d= 77 *d<>a", 0, nil)
	if z.a != 77 || z.h[5] != 0x0102 {
		t.Fatalf("*d<>a: a=%d h[5]=%#x", z.a, z.h[5])
	}
}

func TestVMComparisonsAndJumps(t *testing.T) {
	tests := []struct {
		prog string
		in   uint32
		a    uint32
	}{
		{"a> 5 if a= 1 else a= 2 endif", 9, 1},
		{"a> 5 if a= 1 else a= 2 endif", 5, 2},
		{"a< 5 if a= 1 else a= 2 endif", 3, 1},
		{"a== 5 if a= 1 else a= 2 endif", 5, 1},
		{"a== 5 ifnot a= 1 else a= 2 endif", 5, 2},
		{"a= 0 do a++ a< 10 while", 0, 10},
		{"a= 100 do a-- a== 50 until", 0, 50},
		{"a= 0 c= 0 do a++ b=a a+=c c=a a=b a< 10 while", 0, 10},
		{"a= 0 do a++ a== 7 if a= 99 endif a< 99 while", 0, 99},
		{"a= 3 jmp 2 a= 9", 0, 3}, // jmp N skips the N bytes after its operand
		{"a= 3 jmp 0 a= 9", 0, 9},
		{"a= 1 a== 1 jt 2 a= 9", 0, 1},
		{"a= 1 a== 2 jf 2 a= 9", 0, 1},
	}
	for _, tt := range tests {
		z := runVM(t, tt.prog, tt.in, nil)
		if z.a != tt.a {
			t.Errorf("%q with a=%d: got %d, want %d", tt.prog, tt.in, z.a, tt.a)
		}
	}
}

func TestVMLongJumps(t *testing.T) {
	// ifl/elsel/endif use 16-bit jumps; a body longer than 127 bytes needs them.
	body := strings.Repeat("a++ ", 150)
	z := runVM(t, "a> 5 ifl "+body+" else a= 2 endif", 9, nil)
	if z.a != 9+150 {
		t.Fatalf("long if taken: a=%d", z.a)
	}
	z = runVM(t, "a> 5 ifl "+body+" elsel a= 2 endif", 1, nil)
	if z.a != 2 {
		t.Fatalf("long if not taken: a=%d", z.a)
	}
	// A short if with a long body is rejected by the compiler.
	if _, err := compileConfig(vmConfig("a> 5 if "+body+" endif"), [9]int{}); err == nil {
		t.Fatalf("expected an 'IF too big' error")
	}
	// a long backwards loop
	z = runVM(t, "a= 0 do "+strings.Repeat("a++ ", 130)+" a< 255 while", 0, nil)
	if z.a != 260 {
		t.Fatalf("long loop: a=%d", z.a)
	}
}

func TestVMOutput(t *testing.T) {
	var got []int
	z := testModel(t, vmConfig("a= 65 out a++ out a= 255 a+= 45 out"))
	z.inith()
	z.out = func(c int) { got = append(got, c) }
	z.run(0)
	if len(got) != 3 || got[0] != 65 || got[1] != 66 || got[2] != 300&255 {
		t.Fatalf("output = %v", got)
	}
}

func TestVMErrors(t *testing.T) {
	// opcode 0 is ERROR
	z := testModel(t, vmConfig("error"))
	z.inith()
	err := func() (err error) { defer recoverFormat(&err); z.run(0); return }()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error opcode: %v", err)
	}
	// jumping out of the program
	z = testModel(t, vmConfig("a= 1"))
	z.inith()
	z.header[z.hbegin] = 255 // LJ to 65535
	z.header[z.hbegin+1], z.header[z.hbegin+2] = 255, 255
	err = func() (err error) { defer recoverFormat(&err); z.run(0); return }()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("lj out of range: %v", err)
	}
	// infinite loop is stopped
	z = testModel(t, vmConfig("do forever"))
	z.inith()
	err = func() (err error) { defer recoverFormat(&err); z.run(0); return }()
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("infinite loop: %v", err)
	}
}

func TestVMStepBudget(t *testing.T) {
	// About 100 million instructions in one call: more than the allowance of
	// a program that has only run once, but fine for one that has run for
	// 30000 calls (like an inverse BWT at the end of a large block).
	const prog = "a= 250 a<<= 17 do a-- a> 0 while"
	z := testModel(t, vmConfig(prog))
	z.inith()
	err := func() (err error) { defer recoverFormat(&err); z.run(0); return }()
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "step budget") {
		t.Fatalf("first call: %v", err)
	}

	z = testModel(t, vmConfig(prog))
	z.inith()
	z.calls = 30000
	err = func() (err error) { defer recoverFormat(&err); z.run(0); return }()
	if err != nil || z.a != 0 {
		t.Fatalf("after 30000 calls: err=%v a=%d", err, z.a)
	}
	if z.steps < 90_000_000 {
		t.Fatalf("steps = %d", z.steps)
	}

	// The budget is cumulative: many cheap calls do not stack up credit for
	// a later runaway beyond stepsBase + stepsPerCall*calls.
	z = testModel(t, vmConfig("a++"))
	z.inith()
	for i := 0; i < 1000; i++ {
		z.run(0)
	}
	if z.calls != 1000 || z.steps == 0 || z.steps > 10_000 {
		t.Fatalf("calls=%d steps=%d", z.calls, z.steps)
	}
}
