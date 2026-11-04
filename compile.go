package zpaq

import (
	"fmt"
	"strings"
)

// Pseudo-opcodes of the config language, above the 256 real ZPAQL opcodes.
const (
	opPost = 256 + iota
	opPcomp
	opEnd
	opIf
	opIfNot
	opElse
	opEndif
	opDo
	opWhile
	opUntil
	opForever
	opIfl
	opIfnotl
	opElsel
	opSemi
)

const (
	opJT  = 39
	opJF  = 47
	opJMP = 63
	opLJ  = 255
)

var compNames = []string{"", "const", "cm", "icm", "match", "avg", "mix2", "mix", "isse", "sse"}

var opcodeList = [...]string{
	"error", "a++", "a--", "a!", "a=0", "", "", "a=r",
	"b<>a", "b++", "b--", "b!", "b=0", "", "", "b=r",
	"c<>a", "c++", "c--", "c!", "c=0", "", "", "c=r",
	"d<>a", "d++", "d--", "d!", "d=0", "", "", "d=r",
	"*b<>a", "*b++", "*b--", "*b!", "*b=0", "", "", "jt",
	"*c<>a", "*c++", "*c--", "*c!", "*c=0", "", "", "jf",
	"*d<>a", "*d++", "*d--", "*d!", "*d=0", "", "", "r=a",
	"halt", "out", "", "hash", "hashd", "", "", "jmp",
	"a=a", "a=b", "a=c", "a=d", "a=*b", "a=*c", "a=*d", "a=",
	"b=a", "b=b", "b=c", "b=d", "b=*b", "b=*c", "b=*d", "b=",
	"c=a", "c=b", "c=c", "c=d", "c=*b", "c=*c", "c=*d", "c=",
	"d=a", "d=b", "d=c", "d=d", "d=*b", "d=*c", "d=*d", "d=",
	"*b=a", "*b=b", "*b=c", "*b=d", "*b=*b", "*b=*c", "*b=*d", "*b=",
	"*c=a", "*c=b", "*c=c", "*c=d", "*c=*b", "*c=*c", "*c=*d", "*c=",
	"*d=a", "*d=b", "*d=c", "*d=d", "*d=*b", "*d=*c", "*d=*d", "*d=",
	"", "", "", "", "", "", "", "",
	"a+=a", "a+=b", "a+=c", "a+=d", "a+=*b", "a+=*c", "a+=*d", "a+=",
	"a-=a", "a-=b", "a-=c", "a-=d", "a-=*b", "a-=*c", "a-=*d", "a-=",
	"a*=a", "a*=b", "a*=c", "a*=d", "a*=*b", "a*=*c", "a*=*d", "a*=",
	"a/=a", "a/=b", "a/=c", "a/=d", "a/=*b", "a/=*c", "a/=*d", "a/=",
	"a%=a", "a%=b", "a%=c", "a%=d", "a%=*b", "a%=*c", "a%=*d", "a%=",
	"a&=a", "a&=b", "a&=c", "a&=d", "a&=*b", "a&=*c", "a&=*d", "a&=",
	"a&~a", "a&~b", "a&~c", "a&~d", "a&~*b", "a&~*c", "a&~*d", "a&~",
	"a|=a", "a|=b", "a|=c", "a|=d", "a|=*b", "a|=*c", "a|=*d", "a|=",
	"a^=a", "a^=b", "a^=c", "a^=d", "a^=*b", "a^=*c", "a^=*d", "a^=",
	"a<<=a", "a<<=b", "a<<=c", "a<<=d", "a<<=*b", "a<<=*c", "a<<=*d", "a<<=",
	"a>>=a", "a>>=b", "a>>=c", "a>>=d", "a>>=*b", "a>>=*c", "a>>=*d", "a>>=",
	"a==a", "a==b", "a==c", "a==d", "a==*b", "a==*c", "a==*d", "a==",
	"a<a", "a<b", "a<c", "a<d", "a<*b", "a<*c", "a<*d", "a<",
	"a>a", "a>b", "a>c", "a>d", "a>*b", "a>*c", "a>*d", "a>",
	"", "", "", "", "", "", "", "",
	"", "", "", "", "", "", "", "lj",
	"post", "pcomp", "end", "if", "ifnot", "else", "endif", "do",
	"while", "until", "forever", "ifl", "ifnotl", "elsel", ";",
}

// compiler translates a ZPAQ configuration (the text format of the zpaq
// "cfg" files) into the binary header, as libzpaq's Compiler does.
type compiler struct {
	in      string
	pos     int
	line    int
	state   int
	args    [9]int
	ifStack []int
	doStack []int
}

func (c *compiler) ch(i int) byte {
	if i < len(c.in) {
		return c.in[i]
	}
	return 0
}

func (c *compiler) fail(msg string, expected string) {
	tok := ""
	for i := c.pos; i < len(c.in) && c.in[i] > ' ' && len(tok) < 20; i++ {
		tok += string(c.in[i])
	}
	s := fmt.Sprintf("config line %d at %s: %s", c.line, tok, msg)
	if expected != "" {
		s += ", expected: " + expected
	}
	panic(errFormat{s})
}

func (c *compiler) next() {
	for ; c.pos < len(c.in); c.pos++ {
		b := c.in[c.pos]
		if b == '\n' {
			c.line++
		}
		if b == '(' {
			if c.state < 0 {
				c.state += 2
			} else {
				c.state++
			}
		} else if c.state > 0 && b == ')' {
			c.state--
		} else if c.state < 0 && b <= ' ' {
			c.state = 0
		} else if c.state == 0 && b > ' ' {
			c.state = -1
			break
		}
	}
	if c.pos >= len(c.in) {
		c.fail("unexpected end of config", "")
	}
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

func (c *compiler) matchToken(word string) bool {
	a, w := c.pos, 0
	for ; c.ch(a) > ' ' && c.ch(a) != '(' && w < len(word); a, w = a+1, w+1 {
		if lower(c.ch(a)) != lower(word[w]) {
			return false
		}
	}
	return w == len(word) && (c.ch(a) <= ' ' || c.ch(a) == '(')
}

func (c *compiler) tokenList(list []string) int {
	c.next()
	for i, w := range list {
		if w != "" && c.matchToken(w) {
			return i
		}
	}
	c.fail("unexpected", "")
	return -1
}

func (c *compiler) token(s string) {
	c.next()
	if !c.matchToken(s) {
		c.fail("expected", s)
	}
}

func atoi(s string) int {
	i, neg, n := 0, false, 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		neg = s[i] == '-'
		i++
	}
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		return -n
	}
	return n
}

func (c *compiler) number(low, high int) int {
	c.next()
	r := 0
	rest := c.in[c.pos:]
	switch {
	case len(rest) >= 2 && rest[0] == '$' && rest[1] >= '1' && rest[1] <= '9':
		if len(rest) >= 3 && rest[2] == '+' {
			r = atoi(rest[3:])
		}
		r += c.args[rest[1]-'1']
	case len(rest) >= 1 && (rest[0] == '-' || (rest[0] >= '0' && rest[0] <= '9')):
		r = atoi(rest)
	default:
		c.fail("expected a number", "")
	}
	if r < low {
		c.fail("number too low", "")
	}
	if r > high {
		c.fail("number too high", "")
	}
	return r
}

func (c *compiler) compileProgram(z *zpaql) int {
	op := 0
	compBegin := z.hend
	put := func(b int) { z.header[z.hend] = byte(b); z.hend++ }
	pop := func(s *[]int) int { v := (*s)[len(*s)-1]; *s = (*s)[:len(*s)-1]; return v }
	for {
		op = c.tokenList(opcodeList[:])
		if op == opPost || op == opPcomp || op == opEnd {
			break
		}
		operand, operand2 := -1, -1
		switch {
		case op == opIf:
			op, operand = opJF, 0
			c.ifStack = append(c.ifStack, z.hend+1)
		case op == opIfNot:
			op, operand = opJT, 0
			c.ifStack = append(c.ifStack, z.hend+1)
		case op == opIfl || op == opIfnotl:
			if op == opIfl {
				put(opJT)
			} else {
				put(opJF)
			}
			put(3)
			op, operand, operand2 = opLJ, 0, 0
			c.ifStack = append(c.ifStack, z.hend+1)
		case op == opElse || op == opElsel:
			if op == opElse {
				op, operand = opJMP, 0
			} else {
				op, operand, operand2 = opLJ, 0, 0
			}
			a := pop(&c.ifStack)
			if z.header[a-1] != opLJ {
				j := z.hend - a + 1
				if op == opLJ {
					j++
				}
				if j > 127 {
					c.fail("IF too big, try IFL, IFNOTL", "")
				}
				z.header[a] = byte(j)
			} else {
				j := z.hend - compBegin + 2
				if op == opLJ {
					j++
				}
				z.header[a] = byte(j)
				z.header[a+1] = byte(j >> 8)
			}
			c.ifStack = append(c.ifStack, z.hend+1)
		case op == opEndif:
			a := pop(&c.ifStack)
			j := z.hend - a - 1
			if z.header[a-1] != opLJ {
				if j > 127 {
					c.fail("IF too big, try IFL, IFNOTL, ELSEL", "")
				}
				z.header[a] = byte(j)
			} else {
				j = z.hend - compBegin
				z.header[a] = byte(j)
				z.header[a+1] = byte(j >> 8)
			}
		case op == opDo:
			c.doStack = append(c.doStack, z.hend)
		case op == opWhile || op == opUntil || op == opForever:
			a := pop(&c.doStack)
			j := a - z.hend - 2
			if j >= -127 {
				switch op {
				case opWhile:
					op = opJT
				case opUntil:
					op = opJF
				default:
					op = opJMP
				}
				operand = j & 255
			} else {
				j = a - compBegin
				if op == opWhile {
					put(opJF)
					put(3)
				}
				if op == opUntil {
					put(opJT)
					put(3)
				}
				op = opLJ
				operand = j & 255
				operand2 = j >> 8
			}
		case op&7 == 7:
			switch op {
			case opLJ:
				operand = c.number(0, 65535)
				operand2 = operand >> 8
				operand &= 255
			case opJT, opJF, opJMP:
				operand = c.number(-128, 127) & 255
			default:
				operand = c.number(0, 255)
			}
		}
		if op >= 0 && op <= 255 {
			put(op)
		}
		if operand >= 0 {
			put(operand)
		}
		if operand2 >= 0 {
			put(operand2)
		}
		if z.hend >= len(z.header)-130 || z.hend-z.hbegin+z.cend-2 > 65535 {
			c.fail("program too big", "")
		}
	}
	put(0) // END
	return op
}

// compileConfig compiles a ZPAQ config text with the given $1..$9 arguments
// into a zpaql holding the COMP and HCOMP sections. Configs with a PCOMP
// section are not supported.
func compileConfig(text string, args [9]int) (z *zpaql, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(errFormat); ok {
				z, err = nil, e
				return
			}
			panic(r)
		}
	}()
	c := &compiler{in: text, line: 1, args: args}
	z = &zpaql{header: make([]byte, 68000)}
	c.token("comp")
	z.header[2] = byte(c.number(0, 255)) // hh
	z.header[3] = byte(c.number(0, 255)) // hm
	z.header[4] = byte(c.number(0, 255)) // ph
	z.header[5] = byte(c.number(0, 255)) // pm
	n := c.number(0, 255)
	z.header[6] = byte(n)
	z.cend = 7
	for i := 0; i < n; i++ {
		c.number(i, i)
		typ := c.tokenList(compNames)
		z.header[z.cend] = byte(typ)
		z.cend++
		clen := compSize[typ]
		if clen < 1 || clen > 10 {
			c.fail("invalid component", "")
		}
		for j := 1; j < clen; j++ {
			z.header[z.cend] = byte(c.number(0, 255))
			z.cend++
		}
	}
	z.cend++ // END
	z.hbegin = z.cend + 128
	z.hend = z.hbegin
	c.token("hcomp")
	op := c.compileProgram(z)
	hsize := z.cend - 2 + z.hend - z.hbegin
	z.header[0] = byte(hsize)
	z.header[1] = byte(hsize >> 8)
	switch op {
	case opPost:
		c.number(0, 0)
		c.token("end")
	case opPcomp:
		c.fail("PCOMP is not supported", "")
	}
	return z, nil
}

func itos(n int) string { return fmt.Sprint(n) }

var _ = strings.TrimSpace
