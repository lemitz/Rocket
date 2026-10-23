package server_test

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rocket/internal/client"
	"rocket/internal/desktop"
	"rocket/internal/server"
	"rocket/internal/wire"
)

func startServer(t *testing.T, dir string) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() { _ = (server.Server{Dir: dir}).Serve(ln) }()
	return ln.Addr().(*net.TCPAddr).String()[len("127.0.0.1:"):]
}

func TestRoundTripAndCollisions(t *testing.T) {
	serverDir, sourceDir := t.TempDir(), t.TempDir()
	payload := bytes.Repeat([]byte("rocket\x00"), 400000) // > 1 MiB and contains NUL bytes.
	source := filepath.Join(sourceDir, "video.bin")
	if err := os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	port := startServer(t, serverDir)
	c, err := client.Dial(port)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if name, err := c.Ping(); err != nil || name != "Rocket" {
		t.Fatalf("ping: %q %v", name, err)
	}
	for _, want := range []string{"video.bin", "video (1).bin"} {
		name, err := c.Put(source)
		if err != nil || name != want {
			t.Fatalf("put: %q %v; want %q", name, err, want)
		}
	}
	for _, name := range []string{"video.bin", "video (1).bin"} {
		actual, err := os.ReadFile(filepath.Join(serverDir, name))
		if err != nil || !bytes.Equal(actual, payload) {
			t.Fatalf("published file corrupted: %q size %d, %v", name, len(actual), err)
		}
	}
}

func TestFolderChangeAffectsOnlyNewConnections(t *testing.T) {
	oldDir, newDir, sourceDir := t.TempDir(), t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "switch.txt")
	if err := os.WriteFile(source, []byte("Rocket"), 0600); err != nil {
		t.Fatal(err)
	}
	var current atomic.Value
	current.Store(oldDir)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = (server.Server{Dir: oldDir, DirProvider: func() string { return current.Load().(string) }}).Serve(ln)
	}()
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	c, err := client.Dial(port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Put(source); err != nil {
		t.Fatal(err)
	}
	current.Store(newDir)
	if name, err := c.Put(source); err != nil || name != "switch (1).txt" {
		t.Fatalf("active connection switched folder: %q %v", name, err)
	}
	c.Close()
	fresh, err := client.Dial(port)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if name, err := fresh.Put(source); err != nil || name != "switch.txt" {
		t.Fatalf("new connection did not use updated folder: %q %v", name, err)
	}
	for _, dir := range []string{oldDir, newDir} {
		if _, err := os.Stat(filepath.Join(dir, "switch.txt")); err != nil {
			t.Fatalf("file missing from %s: %v", dir, err)
		}
	}
}

func TestBatchEmitsRealTransferStats(t *testing.T) {
	dir, sourceDir := t.TempDir(), t.TempDir()
	payload := bytes.Repeat([]byte("x"), 1<<20)
	source := filepath.Join(sourceDir, "stats.bin")
	if err := os.WriteFile(source, payload, 0600); err != nil {
		t.Fatal(err)
	}
	events := make(chan desktop.Event, 128)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = (server.Server{Dir: dir, OnEvent: func(e desktop.Event) { events <- e }}).Serve(ln)
	}()
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	c, err := client.Dial(port)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := c.Put(source); err != nil {
			c.Close()
			t.Fatal(err)
		}
	}
	c.Close()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == "complete" {
				if e.SessionID == 0 || e.Files != 2 || e.Bytes != 2*int64(len(payload)) || e.Duration <= 0 {
					t.Fatalf("incorrect summary: %+v", e)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing batch completion event")
		}
	}
}

func TestPingAdvertisesPcIdentity(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		_ = (server.Server{Dir: dir, Name: "PC-ONE", ID: "0123456789abcdef0123456789abcdef"}).Serve(ln)
	}()
	conn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "ping"}); err != nil {
		t.Fatal(err)
	}
	var response wire.Response
	if err := wire.Read(conn, &response); err != nil || !response.OK || response.Name != "PC-ONE" || response.ID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected PC identity: %+v %v", response, err)
	}
}

func TestEmptyFileHasExplicitSize(t *testing.T) {
	dir, sourceDir := t.TempDir(), t.TempDir()
	source := filepath.Join(sourceDir, "empty.txt")
	if err := os.WriteFile(source, nil, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := client.Dial(startServer(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Put(source); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(dir, "empty.txt"))
	if err != nil || stat.Size() != 0 {
		t.Fatalf("empty result: %+v %v", stat, err)
	}
}

func TestPcFilesCannotBeBrowsed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", startServer(t, dir)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "list"}); err != nil {
		t.Fatal(err)
	}
	var response wire.Response
	if err := wire.Read(conn, &response); err != nil || response.OK || !strings.Contains(response.Error, "unknown command") {
		t.Fatalf("obsolete directory browsing is reachable: %+v %v", response, err)
	}
}

func TestBadChecksumAndInterruptedTransferLeaveNoDownload(t *testing.T) {
	dir := t.TempDir()
	port := startServer(t, dir)
	conn, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "put", Name: "broken.txt", Size: 3}); err != nil {
		t.Fatal(err)
	}
	var ack wire.Response
	if err := wire.Read(conn, &ack); err != nil || !ack.OK {
		t.Fatalf("put ACK: %+v %v", ack, err)
	}
	if _, err := conn.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := wire.Write(conn, wire.Trailer{Hash: "00000000"}); err != nil {
		t.Fatal(err)
	}
	if err := wire.Read(conn, &ack); err != nil || ack.OK {
		t.Fatalf("bad checksum accepted: %+v %v", ack, err)
	}
	conn.Close()
	if _, err := os.Stat(filepath.Join(dir, "broken.txt")); !os.IsNotExist(err) {
		t.Fatalf("invalid file published: %v", err)
	}
	conn, err = net.Dial("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "put", Name: "cut.txt", Size: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := wire.Read(conn, &ack); err != nil || !ack.OK {
		t.Fatalf("put ACK: %+v %v", ack, err)
	}
	conn.Write([]byte("half"))
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(dir, ".rocket-staging"))
		if err == nil && len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphaned partial files: %v, %v", entries, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(dir, "cut.txt")); !os.IsNotExist(err) {
		t.Fatalf("interrupted file published: %v", err)
	}
}

func TestRejectTraversalAndOversizedFrame(t *testing.T) {
	dir := t.TempDir()
	port := startServer(t, dir)
	conn, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "get", Name: "../secret"}); err != nil {
		t.Fatal(err)
	}
	var resp wire.Response
	if err := wire.Read(conn, &resp); err != nil || resp.OK || !strings.Contains(resp.Error, "unknown command") {
		t.Fatalf("traversal response: %+v %v", resp, err)
	}
}
