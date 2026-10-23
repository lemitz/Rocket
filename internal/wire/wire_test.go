package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestZeroLengthFileResponseIncludesSize(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, Response{OK: true, Size: 0}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.Bytes(), []byte(`"size":0`)) {
		t.Fatal("a zero-size download must include its size in the GET response")
	}
}

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, Request{V: 1, Cmd: "ping"}); err != nil {
		t.Fatal(err)
	}
	var got Request
	if err := Read(&b, &got); err != nil || got.Cmd != "ping" {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	for _, n := range []uint32{0, MaxHeader + 1} {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], n)
		if err := Read(bytes.NewReader(b[:]), &got); !errors.Is(err, ErrFrame) {
			t.Errorf("length %d: %v", n, err)
		}
	}
	if err := Write(&b, map[string]string{"too_large": strings.Repeat("a", MaxHeader)}); !errors.Is(err, ErrFrame) {
		t.Fatalf("accepted oversize frame: %v", err)
	}
}
