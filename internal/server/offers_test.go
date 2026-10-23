package server_test

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"rocket/internal/outbox"
	"rocket/internal/server"
	"rocket/internal/wire"
)

const deviceOne = "11111111111111111111111111111111"
const deviceTwo = "22222222222222222222222222222222"

func connect(t *testing.T, port int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(8 * time.Second))
	return conn
}

func exchange(t *testing.T, conn net.Conn, req wire.Request) wire.Response {
	t.Helper()
	req.V = 1
	if err := wire.Write(conn, req); err != nil {
		t.Fatal(err)
	}
	var response wire.Response
	if err := wire.Read(conn, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestPcChosenFilesReachOnlyChosenPhone(t *testing.T) {
	pcDir, src := t.TempDir(), t.TempDir()
	data := []byte("PC-selected file, not entire PC directory")
	path := filepath.Join(src, "selected.txt")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pcDir, "private.txt"), []byte("do not show"), 0600); err != nil {
		t.Fatal(err)
	}
	out := outbox.New()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = (server.Server{Dir: pcDir, Outbox: out}).Serve(ln) }()
	port := ln.Addr().(*net.TCPAddr).Port
	phoneOne := connect(t, port)
	defer phoneOne.Close()
	phoneTwo := connect(t, port)
	defer phoneTwo.Close()
	poll := func(c net.Conn, id, name string) wire.Response {
		return exchange(t, c, wire.Request{Cmd: "poll", Device: id, DeviceName: name})
	}
	if r := poll(phoneOne, deviceOne, "First phone"); !r.OK || len(r.Offers) != 0 {
		t.Fatalf("initial poll: %+v", r)
	}
	if r := poll(phoneTwo, deviceTwo, "Second phone"); !r.OK || len(r.Offers) != 0 {
		t.Fatalf("initial poll: %+v", r)
	}
	if _, err := out.Queue(deviceOne, []string{path}); err != nil {
		t.Fatal(err)
	}
	if r := poll(phoneTwo, deviceTwo, "Second phone"); len(r.Offers) != 0 {
		t.Fatalf("offer leaked to wrong phone: %+v", r)
	}
	selected := poll(phoneOne, deviceOne, "First phone")
	if len(selected.Offers) != 1 || selected.Offers[0].Name != "selected.txt" {
		t.Fatalf("missing selected file: %+v", selected)
	}
	id := selected.Offers[0].ID
	if r := exchange(t, phoneTwo, wire.Request{Cmd: "offer-get", ID: id, Device: deviceTwo}); r.OK {
		t.Fatalf("other phone received offer: %+v", r)
	}
	if r := exchange(t, phoneOne, wire.Request{Cmd: "get", Name: "private.txt"}); r.OK {
		t.Fatalf("arbitrary PC file readable: %+v", r)
	}
	// The rejected legacy request ends its TCP session.
	phoneOne.Close()
	phoneOne = connect(t, port)
	defer phoneOne.Close()
	resp := exchange(t, phoneOne, wire.Request{Cmd: "offer-get", ID: id, Device: deviceOne})
	if !resp.OK || resp.Size != int64(len(data)) {
		t.Fatalf("offer header: %+v", resp)
	}
	got := make([]byte, resp.Size)
	if _, err := io.ReadFull(phoneOne, got); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("offered bytes: %q, %v", got, err)
	}
	var trailer wire.Trailer
	if err := wire.Read(phoneOne, &trailer); err != nil || trailer.Hash != fmt.Sprintf("%08x", crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))) {
		t.Fatalf("hash: %+v %v", trailer, err)
	}
	if r := exchange(t, phoneOne, wire.Request{Cmd: "offer-done", ID: id, Device: deviceOne}); !r.OK {
		t.Fatalf("publish acknowledgement: %+v", r)
	}
	if r := poll(phoneOne, deviceOne, "First phone"); len(r.Offers) != 0 {
		t.Fatalf("offer repeated after confirmation: %+v", r)
	}
}
