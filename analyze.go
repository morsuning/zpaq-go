package zpaq

import "strings"

// textExe reports whether data looks like text and whether it looks like x86
// code, using the per-fragment order-1 tests of the zpaq command line
// program (zpaq.cpp, "Analyze fragment for redundancy, x86, text"). Like zpaq,
// which judges a block by its fragments, data is examined in fragments and the
// block is text if more than half of them are, and x86 code if more than a
// quarter of them are.
func textExe(data []byte) (text, exe bool) {
	const frag = 1 << 16
	frags, nt, nx := 0, 0, 0
	for off := 0; off < len(data) || (off == 0 && frags == 0); off += frag {
		end := min(off+frag, len(data))
		var o1 [256]byte
		c1 := 0
		for _, c := range data[off:end] {
			o1[c1] = c
			c1 = int(c)
		}
		text1, exe1 := 0, 0
		for i := 0; i < 256; i++ {
			if o1[i] == ' ' && (isAlnum(i) || i == '.' || i == ',') {
				text1++
			}
			if o1[i] != 0 && (i < 9 || i == 11 || i == 12 || (i >= 14 && i <= 31) || i >= 240) {
				text1--
			}
			if i >= 192 && i < 240 && o1[i] != 0 && (o1[i] < 128 || o1[i] >= 192) {
				text1--
			}
			if o1[i] == 139 {
				exe1++
			}
		}
		if text1 >= 3 {
			nt++
		}
		if exe1 >= 5 {
			nx++
		}
		frags++
		if end == len(data) {
			break
		}
	}
	return nt*2 > frags, nx*4 > frags
}

func isAlnum(c int) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// methodString returns the zpaq method string (see makeConfig) for a
// context-mixing level 4 or 5 block of the given data, as libzpaq's
// compressBlock chooses it, except that no E8E9 or LZ77 preprocessing is used.
func methodString(level int, data []byte) string {
	n := len(data)
	arg0 := max(lg(uint(n+4095))-20, 0)
	text, _ := textExe(data)
	m := "x" + itos(arg0) + ",0"
	if level == 4 {
		m += "ci1,1,1,1,2a"
		if text {
			m += "w"
		}
		return m + "m"
	}

	// level 5: slow CM with lots of models
	if text {
		m += "w2c0,1010,255i1"
	} else {
		m += "w1i1"
	}
	m += "c256ci1,1,1,1,1,1,2a"

	// Analyze the data for periodic structure: r[k] counts the distances k
	// between consecutive equal bytes.
	const nr = 1 << 12
	var pt [256]int
	r := make([]int, nr)
	for i, c := range data {
		k := i - pt[c]
		if k > 0 && k < nr {
			r[k]++
		}
		pt[c] = i
	}

	// Add periodic models.
	n1 := n - r[1] - r[2] - r[3]
	var sb strings.Builder
	for i := 0; i < 2; i++ {
		period := 0
		score := 0.0
		t := 0
		for j := 5; j < nr && t < n1; j++ {
			s := float64(r[j]) / (256.0 + float64(n1-t))
			if s > score {
				score, period = s, j
			}
			t += r[j]
		}
		if period > 4 && score > 0.1 {
			sb.WriteString("c0,0," + itos(999+period) + ",255i1")
			if period <= 255 {
				sb.WriteString("c0," + itos(period) + "i1")
			}
			n1 -= r[period]
			r[period] = 0
		} else {
			break
		}
	}
	return m + sb.String() + "c0,2,0,255i1c0,3,0,0,255i1c0,4,0,0,0,255i1mm16ts19t0"
}
