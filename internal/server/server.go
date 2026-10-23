package server

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"os"
	"sync/atomic"
	"time"

	"rocket/internal/desktop"
	"rocket/internal/fsx"
	"rocket/internal/outbox"
	"rocket/internal/wire"
)

const maxFileSize = 1 << 40 // 1 TiB per transfer; v1 has no resume.

var crcTable = crc32.MakeTable(crc32.Castagnoli)
var nextSessionID atomic.Uint64

type Server struct {
	Dir         string
	DirProvider func() string // evaluated for each accepted connection; active transfers keep their folder.
	Name        string
	ID          string
	OnEvent     func(desktop.Event)
	Outbox      *outbox.Registry
}

func (s Server) Serve(ln net.Listener) error {
	if _, err := os.Stat(s.Dir); err != nil {
		return err
	}
	if err := fsx.CleanOldParts(s.Dir, 24*time.Hour); err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		handler := s
		if s.DirProvider != nil {
			handler.Dir = s.DirProvider()
		}
		go handler.Handle(conn)
	}
}

func (s Server) Handle(conn net.Conn) {
	defer conn.Close()
	stats := sessionStats{send: s.OnEvent, id: nextSessionID.Add(1)}
	var sessionError error
	defer func() { stats.finish(sessionError) }()
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok || !(remote.IP.IsPrivate() || remote.IP.IsLoopback() || remote.IP.IsLinkLocalUnicast()) {
		return // Rocket only serves local/private IPv4 networks, never a public client.
	}
	for {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
		var req wire.Request
		if err := wire.Read(conn, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				sessionError = err
			}
			return
		}
		if req.V != 1 {
			_ = wire.Write(conn, wire.Response{Error: "unsupported protocol version"})
			return
		}
		var err error
		switch req.Cmd {
		case "ping":
			name := s.Name
			if name == "" {
				name = "Rocket"
			}
			err = wire.Write(conn, wire.Response{OK: true, Name: name, ID: s.ID})
		case "poll":
			err = s.poll(conn, req)
		case "put":
			err = s.put(conn, req, &stats)
		case "offer-get":
			err = s.offerGet(conn, req, &stats)
		case "offer-done":
			err = s.offerDone(conn, req)
		default:
			_ = wire.Write(conn, wire.Response{Error: "unknown command"})
			return
		}
		if err != nil {
			sessionError = err
			return // Broken transfer cannot be safely resynchronised.
		}
	}
}

func replyError(conn net.Conn, err error) error {
	return wire.Write(conn, wire.Response{Error: err.Error()})
}

// After a PUT has been acknowledged, errors end the session even if an error
// response was delivered: there may be unread raw bytes on this connection.
func failTransfer(conn net.Conn, err error) error {
	_ = replyError(conn, err)
	return err
}

func (s Server) put(conn net.Conn, req wire.Request, stats *sessionStats) error {
	name, err := fsx.SafeName(req.Name)
	if err != nil || req.Size < 0 || req.Size > maxFileSize || req.Offset != 0 || req.Mtime < 0 || req.Mtime > 4102444800 {
		return replyError(conn, errors.New("invalid file metadata"))
	}
	part, err := fsx.CreatePart(s.Dir)
	if err != nil {
		return replyError(conn, err)
	}
	defer os.Remove(part.Name())
	defer part.Close()
	if err := wire.Write(conn, wire.Response{OK: true}); err != nil {
		return err
	}
	stats.begin("incoming", name, req.Size)
	conn.SetDeadline(time.Now().Add(time.Hour))
	buffer := make([]byte, 1<<20)
	hash := crc32.New(crcTable)
	n, err := io.CopyBuffer(countingWriter{Writer: io.MultiWriter(part, hash), stats: stats}, io.LimitReader(conn, req.Size), buffer)
	if err != nil {
		return err
	}
	if n != req.Size {
		return io.ErrUnexpectedEOF
	}
	var trailer wire.Trailer
	if err := wire.Read(conn, &trailer); err != nil {
		return err
	}
	want := fmt.Sprintf("%08x", hash.Sum32())
	if subtle.ConstantTimeCompare([]byte(trailer.Hash), []byte(want)) != 1 {
		return failTransfer(conn, errors.New("checksum mismatch"))
	}
	if req.Mtime != 0 {
		mtime := time.Unix(req.Mtime, 0)
		if err := os.Chtimes(part.Name(), mtime, mtime); err != nil {
			return failTransfer(conn, err)
		}
	}
	if err := part.Sync(); err != nil {
		return failTransfer(conn, err)
	}
	if err := part.Close(); err != nil {
		return failTransfer(conn, err)
	}
	saved, err := fsx.Publish(part.Name(), s.Dir, name)
	if err != nil {
		return failTransfer(conn, err)
	}
	if err := wire.Write(conn, wire.Response{OK: true, Saved: saved}); err != nil {
		return err
	}
	stats.fileComplete()
	return nil
}
