package client

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"rocket/internal/wire"
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type Client struct{ conn net.Conn }

// The technical prototype deliberately connects only to localhost.
func Dial(port string) (*Client, error) {
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", port), 3*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) request(req wire.Request) (wire.Response, error) {
	c.conn.SetDeadline(time.Now().Add(2 * time.Minute))
	req.V = 1
	if err := wire.Write(c.conn, req); err != nil {
		return wire.Response{}, err
	}
	var resp wire.Response
	if err := wire.Read(c.conn, &resp); err != nil {
		return resp, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

func (c *Client) Ping() (string, error) {
	resp, err := c.request(wire.Request{Cmd: "ping"})
	return resp.Name, err
}

func (c *Client) Put(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("source is not a regular file")
	}
	if _, err := c.request(wire.Request{Cmd: "put", Name: filepath.Base(path), Size: info.Size(), Mtime: info.ModTime().Unix()}); err != nil {
		return "", err
	}
	c.conn.SetDeadline(time.Now().Add(time.Hour))
	hash := crc32.New(crcTable)
	n, err := io.CopyBuffer(io.MultiWriter(c.conn, hash), io.LimitReader(f, info.Size()), make([]byte, 1<<20))
	if err == nil && n != info.Size() {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		c.conn.Close()
		return "", err
	}
	if err := wire.Write(c.conn, wire.Trailer{Hash: fmt.Sprintf("%08x", hash.Sum32())}); err != nil {
		return "", err
	}
	c.conn.SetDeadline(time.Now().Add(2 * time.Minute))
	var result wire.Response
	if err := wire.Read(c.conn, &result); err != nil {
		return "", err
	}
	if !result.OK {
		return "", errors.New(result.Error)
	}
	return result.Saved, nil
}
