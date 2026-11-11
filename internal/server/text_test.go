package server_test

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rocket/internal/desktop"
	"rocket/internal/outbox"
	"rocket/internal/server"
	"rocket/internal/wire"
)

func textServer(t *testing.T, out *outbox.Registry, callback func(string) error, events chan desktop.Event) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := server.Server{Dir: t.TempDir(), Outbox: out, OnText: callback}
	if events != nil {
		s.OnEvent = func(e desktop.Event) { events <- e }
	}
	go func() { _ = s.Serve(ln) }()
	return ln.Addr().(*net.TCPAddr).Port
}

func expectTextFinal(t *testing.T, events <-chan desktop.Event, direction string, size int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == "error" {
				t.Fatalf("unexpected transfer error: %+v", e)
			}
			if !e.Text || e.Name != "" || e.Message != "" || e.Direction != direction {
				t.Fatalf("text event missing flag or contains content: %+v", e)
			}
			if e.Kind == "complete" {
				if e.Files != 1 || e.Bytes != int64(size) || e.Total != int64(size) {
					t.Fatalf("unexpected transfer summary: %+v", e)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing text completion event")
		}
	}
}

func TestPutTextWaitsForClipboardAndReportsProgress(t *testing.T) {
	started := make(chan string, 1)
	release := make(chan struct{})
	events := make(chan desktop.Event, 16)
	port := textServer(t, nil, func(text string) error {
		started <- text
		<-release
		return nil
	}, events)
	conn := connect(t, port)
	defer conn.Close()
	text := "hello 文字"
	if err := wire.Write(conn, wire.Request{V: 1, Cmd: "put-text", Text: text}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-started:
		if got != text {
			t.Fatalf("callback received wrong text: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clipboard callback not invoked")
	}
	conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	var resp wire.Response
	if err := wire.Read(conn, &resp); err == nil {
		t.Fatalf("acknowledged before clipboard set: %+v", resp)
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected timeout waiting for callback, got %v", err)
	}
	select {
	case e := <-events:
		t.Fatalf("emitted progress before clipboard set: %+v", e)
	default:
	}
	close(release)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := wire.Read(conn, &resp); err != nil || !resp.OK {
		t.Fatalf("clipboard ACK: %+v %v", resp, err)
	}
	conn.Close()
	expectTextFinal(t, events, "incoming", len(text))
}

func TestPutTextValidationAndUnavailableClipboard(t *testing.T) {
	var calls atomic.Int32
	callback := func(text string) error { calls.Add(1); return nil }
	port := textServer(t, nil, callback, nil)
	conn := connect(t, port)
	defer conn.Close()
	for _, text := range []string{"", "a\x00b", strings.Repeat("é", outbox.MaxTextBytes/2+1)} {
		r := exchange(t, conn, wire.Request{Cmd: "put-text", Text: text})
		if r.OK || r.Error == "" || strings.Contains(r.Error, text) && text != "" {
			t.Fatalf("bad text accepted or echoed: %+v", r)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("clipboard called for invalid text %d times", calls.Load())
	}
	if r := exchange(t, conn, wire.Request{Cmd: "put-text", Text: strings.Repeat("é", outbox.MaxTextBytes/2)}); !r.OK || calls.Load() != 1 {
		t.Fatalf("valid UTF-8 at byte limit rejected: %+v, calls %d", r, calls.Load())
	}
	for _, cb := range []func(string) error{nil, func(s string) error { return fmt.Errorf("secret leaked: %s", s) }} {
		port := textServer(t, nil, cb, nil)
		c := connect(t, port)
		secret := "secret clipboard value"
		r := exchange(t, c, wire.Request{Cmd: "put-text", Text: secret})
		c.Close()
		if r.OK || r.Error == "" || strings.Contains(r.Error, secret) {
			t.Fatalf("clipboard failure acknowledged or leaked: %+v", r)
		}
	}
}

func TestOutgoingTextIsolationFrameSizeAndConfirmation(t *testing.T) {
	out := outbox.New()
	events := make(chan desktop.Event, 32)
	port := textServer(t, out, nil, events)
	one, two := connect(t, port), connect(t, port)
	defer one.Close()
	defer two.Close()
	if r := exchange(t, one, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"}); !r.OK || len(r.Offers) != 0 {
		t.Fatalf("first poll: %+v", r)
	}
	if r := exchange(t, two, wire.Request{Cmd: "poll", Device: deviceTwo, DeviceName: "Two"}); !r.OK {
		t.Fatalf("second poll: %+v", r)
	}
	file := filepath.Join(t.TempDir(), "selected.txt")
	if err := os.WriteFile(file, []byte("file offer"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Queue(deviceOne, []string{file}); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("\x01", outbox.MaxTextBytes) // JSON escaped: a single text costs ~49 KiB.
	first, err := out.QueueText(deviceOne, secret)
	if err != nil {
		t.Fatal(err)
	}
	second, err := out.QueueText(deviceOne, secret)
	if err != nil {
		t.Fatal(err)
	}
	if r := exchange(t, two, wire.Request{Cmd: "poll", Device: deviceTwo, DeviceName: "Two"}); len(r.Texts) != 0 {
		t.Fatalf("text visible to wrong phone: %+v", r)
	}
	poll := exchange(t, one, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"})
	if !poll.OK || len(poll.Texts) != 1 || poll.Texts[0].ID != first || poll.Texts[0].Text != secret || len(poll.Offers) != 1 || poll.Offers[0].Name != "selected.txt" {
		t.Fatalf("poll did not frame and preserve first queued text: %+v", poll)
	}
	select {
	case e := <-events:
		t.Fatalf("poll alone created delivery event: %+v", e)
	default:
	}
	if r := exchange(t, two, wire.Request{Cmd: "text-done", Device: deviceTwo, ID: first}); r.OK {
		t.Fatalf("wrong destination acknowledged text: %+v", r)
	}
	if r := exchange(t, one, wire.Request{Cmd: "text-done", Device: deviceOne, ID: "bad-id"}); r.OK {
		t.Fatalf("bad ID acknowledged text: %+v", r)
	}
	if r := exchange(t, one, wire.Request{Cmd: "text-done", Device: deviceOne, ID: first}); !r.OK {
		t.Fatalf("text-done rejected: %+v", r)
	}
	if r := exchange(t, one, wire.Request{Cmd: "text-done", Device: deviceOne, ID: first}); r.OK {
		t.Fatalf("duplicate text-done accepted: %+v", r)
	}
	poll = exchange(t, one, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"})
	if !poll.OK || len(poll.Texts) != 1 || poll.Texts[0].ID != second || len(poll.Offers) != 1 {
		t.Fatalf("second queued text not visible: %+v", poll)
	}
	one.Close()
	expectTextFinal(t, events, "outgoing", len(secret))
}

func TestFileAndTextOnSameConnectionHaveSeparateSessions(t *testing.T) {
	out := outbox.New()
	events := make(chan desktop.Event, 16)
	port := textServer(t, out, nil, events)
	conn := connect(t, port)
	if r := exchange(t, conn, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"}); !r.OK {
		t.Fatalf("poll: %+v", r)
	}
	file := filepath.Join(t.TempDir(), "selected.txt")
	if err := os.WriteFile(file, []byte("files"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Queue(deviceOne, []string{file}); err != nil {
		t.Fatal(err)
	}
	id, err := out.QueueText(deviceOne, "clipboard")
	if err != nil {
		t.Fatal(err)
	}
	poll := exchange(t, conn, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"})
	if len(poll.Offers) != 1 || len(poll.Texts) != 1 || poll.Texts[0].ID != id {
		t.Fatalf("combined poll: %+v", poll)
	}
	resp := exchange(t, conn, wire.Request{Cmd: "offer-get", Device: deviceOne, ID: poll.Offers[0].ID})
	data := make([]byte, resp.Size)
	if _, err := io.ReadFull(conn, data); err != nil || string(data) != "files" {
		t.Fatalf("file bytes: %q %v", data, err)
	}
	var trailer wire.Trailer
	if err := wire.Read(conn, &trailer); err != nil {
		t.Fatal(err)
	}
	if r := exchange(t, conn, wire.Request{Cmd: "offer-done", Device: deviceOne, ID: poll.Offers[0].ID}); !r.OK {
		t.Fatalf("file confirmation: %+v", r)
	}
	if r := exchange(t, conn, wire.Request{Cmd: "text-done", Device: deviceOne, ID: id}); !r.OK {
		t.Fatalf("text confirmation: %+v", r)
	}
	conn.Close()
	var fileID, textID uint64
	deadline := time.After(3 * time.Second)
	for fileID == 0 || textID == 0 {
		select {
		case e := <-events:
			if e.Kind != "complete" {
				continue
			}
			if e.Direction != "outgoing" || e.Files != 1 {
				t.Fatalf("incorrect combined session: %+v", e)
			}
			if e.Text {
				if e.Bytes != int64(len("clipboard")) || e.Name != "" {
					t.Fatalf("incorrect text session: %+v", e)
				}
				textID = e.SessionID
			} else {
				if e.Bytes != int64(len("files")) || e.Name != "selected.txt" {
					t.Fatalf("incorrect file session: %+v", e)
				}
				fileID = e.SessionID
			}
		case <-deadline:
			t.Fatal("missing file or text completion")
		}
	}
	if fileID == textID {
		t.Fatal("file and text events shared a session ID")
	}
}

func TestUnconfirmedTextHasNoDeliverySession(t *testing.T) {
	out := outbox.New()
	events := make(chan desktop.Event, 8)
	port := textServer(t, out, nil, events)
	conn := connect(t, port)
	if r := exchange(t, conn, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"}); !r.OK {
		t.Fatalf("poll: %+v", r)
	}
	id, err := out.QueueText(deviceOne, "not copied yet")
	if err != nil {
		t.Fatal(err)
	}
	if r := exchange(t, conn, wire.Request{Cmd: "poll", Device: deviceOne, DeviceName: "One"}); len(r.Texts) != 1 || r.Texts[0].ID != id {
		t.Fatalf("message not offered: %+v", r)
	}
	conn.Close()
	select {
	case e := <-events:
		t.Fatalf("unconfirmed text emitted transfer event: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
	if got := out.Texts(deviceOne); len(got) != 1 || got[0].ID != id {
		t.Fatalf("unconfirmed message lost: %+v", got)
	}
}

func TestTextRejectsInvalidFramesAndJSON(t *testing.T) {
	var calls atomic.Int32
	port := textServer(t, nil, func(string) error { calls.Add(1); return nil }, nil)
	for _, data := range [][]byte{
		[]byte(`{"v":1,"cmd":"put-text","text":`),
		[]byte(`{"v":1,"cmd":"put-text","text":"\u0000"}`),
		[]byte{0xff},
	} {
		conn := connect(t, port)
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], uint32(len(data)))
		if _, err := conn.Write(append(prefix[:], data...)); err != nil {
			t.Fatal(err)
		}
		var r wire.Response
		err := wire.Read(conn, &r)
		if string(data) == `{"v":1,"cmd":"put-text","text":"\u0000"}` {
			if err != nil || r.OK {
				t.Fatalf("NUL text accepted: %+v %v", r, err)
			}
		} else if err == nil {
			t.Fatalf("malformed JSON/frame got response: %+v", r)
		}
		conn.Close()
	}
	for _, n := range []uint32{0, wire.MaxHeader + 1} {
		conn := connect(t, port)
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], n)
		conn.Write(prefix[:])
		var r wire.Response
		if err := wire.Read(conn, &r); err == nil {
			t.Fatalf("invalid frame length %d got response %+v", n, r)
		}
		conn.Close()
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid frame reached clipboard %d times", calls.Load())
	}
}
