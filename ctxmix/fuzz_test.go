package ctxmix

import (
	"bytes"
	"testing"
)

func FuzzRoundtrip(f *testing.F) {
	f.Add([]byte(""), uint8(0))
	f.Add([]byte("a"), uint8(1))
	f.Add([]byte("hello hello hello hello"), uint8(0))
	f.Add(bytes.Repeat([]byte{0}, 3000), uint8(1))
	f.Add(textData(1500, 4), uint8(0))
	f.Fuzz(func(t *testing.T, data []byte, mode uint8) {
		if len(data) > 3072 {
			data = data[:3072]
		}
		lv := Level(mode & 1)
		comp, err := EncodeAll(data, WithLevel(lv), WithBlockSize(MinBlockSize), WithConcurrency(1+int(mode>>1)%3))
		if err != nil {
			t.Fatal(err)
		}
		out, err := DecodeAll(comp)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out, data) {
			t.Fatal("roundtrip mismatch")
		}
		blk, err := EncodeBlock(nil, data, lv)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := DecodeBlock(nil, blk, len(data), lv); err != nil || !bytes.Equal(out, data) {
			t.Fatalf("block roundtrip: %v", err)
		}
	})
}

func FuzzDecode(f *testing.F) {
	stream, _ := smallStream(f)
	f.Add(stream)
	f.Add([]byte("CMX\x01\x00\x80\x08"))
	f.Add([]byte("CMX\x01\x01\x80\x08\x01\x05\x05\x00\x00\x00\x00hello\x02\x05"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := DecodeAll(in, WithMaxBlockSize(MinBlockSize))
		if err == nil {
			// Anything accepted must re-encode and decode to the same bytes.
			again, err := DecodeAll(mustEncode(t, out, WithBlockSize(MinBlockSize)))
			if err != nil || !bytes.Equal(again, out) {
				t.Fatalf("accepted stream does not roundtrip: %v", err)
			}
		} else if !acceptableDecodeError(err) {
			t.Fatalf("unexpected error type: %v", err)
		}
	})
}
