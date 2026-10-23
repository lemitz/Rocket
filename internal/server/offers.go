package server

import (
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"time"

	"rocket/internal/wire"
)

func (s Server) poll(conn net.Conn, req wire.Request) error {
	if s.Outbox == nil {
		return replyError(conn, fmt.Errorf("PC file sending is unavailable"))
	}
	items, err := s.Outbox.Touch(req.Device, req.DeviceName)
	if err != nil {
		return replyError(conn, err)
	}
	response := wire.Response{OK: true, Offers: make([]wire.Offer, 0, len(items))}
	for _, it := range items {
		response.Offers = append(response.Offers, wire.Offer{ID: it.ID, Name: it.Name, Size: it.Size, Mtime: it.Mtime})
	}
	return wire.Write(conn, response)
}

// Only a staged file for the explicit destination phone can be fetched.
// The phone must send offer-done after its own checksum and publication.
func (s Server) offerGet(conn net.Conn, req wire.Request, stats *sessionStats) error {
	if s.Outbox == nil || req.Offset != 0 {
		return replyError(conn, fmt.Errorf("file is unavailable"))
	}
	f, info, err := s.Outbox.Open(req.Device, req.ID)
	if err != nil {
		return replyError(conn, err)
	}
	defer f.Close()
	if err := wire.Write(conn, wire.Response{OK: true, Size: info.Size(), Mtime: info.ModTime().Unix()}); err != nil {
		return err
	}
	stats.begin("outgoing", info.Name(), info.Size())
	conn.SetDeadline(time.Now().Add(time.Hour))
	hash := crc32.New(crcTable)
	n, err := io.CopyBuffer(countingWriter{Writer: io.MultiWriter(conn, hash), stats: stats}, io.LimitReader(f, info.Size()), make([]byte, 1<<20))
	if err != nil {
		return err
	}
	if n != info.Size() {
		return io.ErrUnexpectedEOF
	}
	// The file only counts as delivered once the phone confirms it with offer-done: sent
	// bytes say nothing about the phone having verified and saved them.
	return wire.Write(conn, wire.Trailer{Hash: fmt.Sprintf("%08x", hash.Sum32())})
}

func (s Server) offerDone(conn net.Conn, req wire.Request, stats *sessionStats) error {
	if s.Outbox == nil {
		return replyError(conn, fmt.Errorf("file is unavailable"))
	}
	if err := s.Outbox.Done(req.Device, req.ID); err != nil {
		return replyError(conn, err)
	}
	if err := wire.Write(conn, wire.Response{OK: true}); err != nil {
		return err
	}
	stats.confirm()
	return nil
}
