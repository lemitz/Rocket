package server

import (
	"errors"
	"net"
	"strings"
	"unicode/utf8"

	"rocket/internal/outbox"
	"rocket/internal/wire"
)

func (s Server) putText(conn net.Conn, req wire.Request, stats *sessionStats) error {
	if len(req.Text) == 0 || len(req.Text) > outbox.MaxTextBytes || !utf8.ValidString(req.Text) || strings.ContainsRune(req.Text, 0) {
		return replyError(conn, errors.New("invalid text"))
	}
	if s.OnText == nil {
		return replyError(conn, errors.New("clipboard is unavailable"))
	}
	// Never include the callback error in a response/event: it may contain the text.
	if err := s.OnText(req.Text); err != nil {
		return replyError(conn, errors.New("could not copy text to clipboard"))
	}
	stats.begin("incoming", "", int64(len(req.Text)))
	stats.copied(len(req.Text))
	stats.fileComplete()
	return wire.Write(conn, wire.Response{OK: true})
}

func (s Server) textDone(conn net.Conn, req wire.Request, stats *sessionStats) error {
	if s.Outbox == nil {
		return replyError(conn, outbox.ErrNoText)
	}
	n, err := s.Outbox.DoneText(req.Device, req.ID)
	if err != nil {
		return replyError(conn, err)
	}
	// A polled message is not delivered until the phone confirms clipboard publication.
	stats.begin("outgoing", "", int64(n))
	stats.copied(n)
	stats.confirm()
	return wire.Write(conn, wire.Response{OK: true})
}
