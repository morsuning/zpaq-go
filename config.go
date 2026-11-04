package zpaq

import (
	"fmt"
	"math/bits"
	"strings"
)

// lg is the bit length of x (0 for 0), as in libzpaq.
func lg(x uint) int { return bits.Len(x) }

func nbits(x int) int { return bits.OnesCount(uint(x)) }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// makeConfig generates the config text for a zpaq method string such as
// "x5,0ci1,1,1,1,2am" (this is libzpaq's makeConfig restricted to the
// context-mixing methods: no LZ77, BWT or E8E9 preprocessing). It returns the
// config text and the $1..$9 arguments.
func makeConfig(method string) (string, [9]int, error) {
	var args [9]int
	if method == "" {
		return "", args, fmt.Errorf("zpaq: empty method")
	}
	typ := method[0]
	if typ != 'x' && typ != 's' && typ != '0' && typ != 'i' {
		return "", args, fmt.Errorf("zpaq: unsupported method %q", method)
	}
	m := method[1:]
	// args: digits and separators
	i, p := 0, 0
	for p < len(m) && i < 9 && (isDigit(m[p]) || m[p] == ',' || m[p] == '.') {
		if isDigit(m[p]) {
			args[i] = args[i]*10 + int(m[p]-'0')
		} else if i++; i < 9 {
			args[i] = 0
		}
		p++
	}
	m = m[p:]

	if typ == '0' {
		return "comp 0 0 0 0 0 hcomp end\n", args, nil
	}
	if args[1] != 0 {
		return "", args, fmt.Errorf("zpaq: method %q needs LZ77/BWT/E8E9 preprocessing, which is not supported", method)
	}

	var comp strings.Builder
	hcomp := "hcomp\nc-- *c=a a+= 255 d=a *d=c\n"
	ncomp := 0
	membits := args[0] + 20
	sb := 5

	for len(m) > 0 && ncomp < 254 {
		// parse command C[N1[,N2]...] into v = {C, N1, N2...}
		v := []int{int(m[0])}
		m = m[1:]
		if len(m) > 0 && isDigit(m[0]) {
			v = append(v, int(m[0]-'0'))
			m = m[1:]
			for len(m) > 0 && (isDigit(m[0]) || m[0] == ',' || m[0] == '.') {
				if isDigit(m[0]) {
					v[len(v)-1] = v[len(v)-1]*10 + int(m[0]-'0')
				} else {
					v = append(v, 0)
				}
				m = m[1:]
			}
		}

		// c: context model
		if v[0] == 'c' {
			for len(v) < 3 {
				v = append(v, 0)
			}
			comp.WriteString(itos(ncomp) + " ")
			sb = 11
			if v[2] < 256 {
				sb += lg(uint(v[2]))
			} else {
				sb += 6
			}
			for j := 3; j < len(v); j++ {
				if v[j] < 512 {
					sb += nbits(v[j]) * 3 / 4
				}
			}
			if sb > membits {
				sb = membits
			}
			if v[1]%1000 == 0 {
				comp.WriteString("icm " + itos(sb-6-v[1]/1000) + "\n")
			} else {
				comp.WriteString("cm " + itos(sb-2-v[1]/1000) + " " + itos(v[1]%1000-1) + "\n")
			}
			hcomp += "d= " + itos(ncomp) + " *d=0\n"
			if v[2] > 1 && v[2] <= 255 { // periodic context
				if lg(uint(v[2])) != lg(uint(v[2]-1)) {
					hcomp += "a=c a&= " + itos(v[2]-1) + " hashd\n"
				} else {
					hcomp += "a=c a%= " + itos(v[2]) + " hashd\n"
				}
			} else if v[2] >= 1000 && v[2] <= 1255 { // distance context
				hcomp += "a= 255 a+= " + itos(v[2]-1000) +
					" d=a a=*d a-=c a> 255 if a= 255 endif d= " + itos(ncomp) + " hashd\n"
			}
			for j := 3; j < len(v); j++ {
				if j == 3 {
					hcomp += "b=c "
				}
				switch {
				case v[j] == 255:
					hcomp += "a=*b hashd\n"
				case v[j] > 0 && v[j] < 255:
					hcomp += "a=*b a&= " + itos(v[j]) + " hashd\n"
				case v[j] >= 256 && v[j] < 512:
					hcomp += "a=r 1 a> 1 if\n  a=r 2 a< 64 if\n    a=*b "
					if v[j] < 511 {
						hcomp += "a&= " + itos(v[j]-256)
					}
					hcomp += " hashd\n  else\n    a>>= 6 hashd a=r 1 hashd\n  endif\nelse\n  a= 255 hashd a=r 2 hashd\nendif\n"
				case v[j] >= 1256:
					hcomp += "a= " + itos(((v[j]-1000)>>8)&255) + " a<<= 8 a+= " + itos((v[j]-1000)&255) + " a+=b b=a\n"
				case v[j] > 1000:
					hcomp += "a= " + itos(v[j]-1000) + " a+=b b=a\n"
				}
				if v[j] < 512 && j < len(v)-1 {
					hcomp += "b++ "
				}
			}
			ncomp++
		}

		// m,8,24: MIX; t,8,24: MIX2; s,8,32,255: SSE
		if (v[0] == 'm' || v[0] == 't' || v[0] == 's') && ncomp > b2i(v[0] == 't') {
			if len(v) <= 1 {
				v = append(v, 8)
			}
			if len(v) <= 2 {
				v = append(v, 24+8*b2i(v[0] == 's'))
			}
			if v[0] == 's' && len(v) <= 3 {
				v = append(v, 255)
			}
			comp.WriteString(itos(ncomp))
			sb = 5 + v[1]*3/4
			switch v[0] {
			case 'm':
				comp.WriteString(" mix " + itos(v[1]) + " 0 " + itos(ncomp) + " " + itos(v[2]) + " 255\n")
			case 't':
				comp.WriteString(" mix2 " + itos(v[1]) + " " + itos(ncomp-1) + " " + itos(ncomp-2) + " " + itos(v[2]) + " 255\n")
			default:
				comp.WriteString(" sse " + itos(v[1]) + " " + itos(ncomp-1) + " " + itos(v[2]) + " " + itos(v[3]) + "\n")
			}
			if v[1] > 8 {
				hcomp += "d= " + itos(ncomp) + " *d=0 b=c a=0\n"
				for ; v[1] >= 16; v[1] -= 8 {
					hcomp += "a<<= 8 a+=*b"
					if v[1] > 16 {
						hcomp += " b++"
					}
					hcomp += "\n"
				}
				if v[1] > 8 {
					hcomp += "a<<= 8 a+=*b a>>= " + itos(16-v[1]) + "\n"
				}
				hcomp += "a<<= 8 *d=a\n"
			}
			ncomp++
		}

		// i: ISSE chain with order increasing by N1,N2...
		if v[0] == 'i' && ncomp > 0 {
			hcomp += "d= " + itos(ncomp-1) + " b=c a=*d d++\n"
			for j := 1; j < len(v) && ncomp < 254; j++ {
				for k := 0; k < v[j]%10; k++ {
					hcomp += "hash "
					if j < len(v)-1 || k < v[j]%10-1 {
						hcomp += "b++ "
					}
					sb += 6
				}
				hcomp += "*d=a"
				if j < len(v)-1 {
					hcomp += " d++"
				}
				hcomp += "\n"
				if sb > membits {
					sb = membits
				}
				comp.WriteString(itos(ncomp) + " isse " + itos(sb-6-v[j]/10) + " " + itos(ncomp-1) + "\n")
				ncomp++
			}
		}

		// a24,0,0: MATCH. N1=hash multiplier. N2,N3=halve buf, table.
		if v[0] == 'a' {
			if len(v) <= 1 {
				v = append(v, 24)
			}
			for len(v) < 4 {
				v = append(v, 0)
			}
			comp.WriteString(itos(ncomp) + " match " + itos(membits-v[3]-2) + " " + itos(membits-v[2]) + "\n")
			hcomp += "d= " + itos(ncomp) + " a=*d a*= " + itos(v[1]) + " a+=*c a++ *d=a\n"
			sb = 5 + (membits-v[2])*3/4
			ncomp++
		}

		// w1,65,26,223,20,0: ICM-ISSE chain of length N1 with word contexts
		if v[0] == 'w' {
			defaults := []int{1, 65, 26, 223, 20, 0}
			for len(v)-1 < len(defaults) {
				v = append(v, defaults[len(v)-1])
			}
			comp.WriteString(itos(ncomp) + " icm " + itos(membits-6-v[6]) + "\n")
			for j := 1; j < v[1]; j++ {
				comp.WriteString(itos(ncomp+j) + " isse " + itos(membits-6-v[6]) + " " + itos(ncomp+j-1) + "\n")
			}
			hcomp += "a=*c a&= " + itos(v[4]) + " a-= " + itos(v[2]) + " a&= 255 a< " + itos(v[3]) + " if\n"
			for j := 0; j < v[1]; j++ {
				if j == 0 {
					hcomp += "  d= " + itos(ncomp)
				} else {
					hcomp += "  d++"
				}
				hcomp += " a=*d a*= " + itos(v[5]) + " a+=*c a++ *d=a\n"
			}
			hcomp += "else\n"
			for j := v[1] - 1; j > 0; j-- {
				hcomp += "  d= " + itos(ncomp+j-1) + " a=*d d++ *d=a\n"
			}
			hcomp += "  d= " + itos(ncomp) + " *d=0\nendif\n"
			ncomp += v[1] - 1
			sb = membits - v[6]
			ncomp++
		}
	}
	_ = sb
	return "comp 9 16 0 0 " + itos(ncomp) + "\n" + comp.String() + hcomp + "halt\nend\n", args, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
