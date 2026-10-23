package wire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

const MaxHeader = 64 * 1024

var ErrFrame = errors.New("invalid Rocket frame")

func Read(r io.Reader, value any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > MaxHeader {
		return fmt.Errorf("%w: length %d", ErrFrame, n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	if !utf8.Valid(buf) || !json.Valid(buf) || buf[0] != '{' {
		return ErrFrame
	}
	if err := json.Unmarshal(buf, value); err != nil {
		return fmt.Errorf("%w: %v", ErrFrame, err)
	}
	return nil
}

func Write(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxHeader {
		return ErrFrame
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
	if err := writeAll(w, prefix[:]); err != nil {
		return err
	}
	return writeAll(w, data)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type Request struct {
	V          int    `json:"v"`
	Cmd        string `json:"cmd"`
	Name       string `json:"name,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Mtime      int64  `json:"mtime,omitempty"`
	Offset     int64  `json:"offset,omitempty"`
	Device     string `json:"device,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	ID         string `json:"id,omitempty"`
}

type Offer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

type Response struct {
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
	Name   string  `json:"name,omitempty"`
	ID     string  `json:"id,omitempty"`
	Saved  string  `json:"saved,omitempty"`
	Size   int64   `json:"size"`
	Mtime  int64   `json:"mtime,omitempty"`
	Hash   string  `json:"hash,omitempty"`
	Offers []Offer `json:"offers"`
	Offset int64   `json:"offset,omitempty"`
}

type Trailer struct {
	Hash string `json:"hash"`
}
