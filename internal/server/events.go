package server

import (
	"io"
	"time"

	"rocket/internal/desktop"
)

// sessionStats tracks one TCP connection. A batch of PUTs shares one session.
// The UI receives at most ten progress notifications per second per connection.
type sessionStats struct {
	send      func(desktop.Event)
	id        uint64
	started   time.Time
	last      time.Time
	active    bool
	inFile    bool
	direction string
	name      string
	bytes     int64
	total     int64
	files     int
}

func (s *sessionStats) begin(direction, name string, total int64) {
	s.inFile = true
	if !s.active {
		s.active = true
		s.started = time.Now()
		s.direction = direction
		s.name = name
		s.total = total
		s.emit("start", "")
		return
	}
	s.name = name
	s.total += total
	s.emit("progress", "")
}

func (s *sessionStats) copied(n int) {
	if n <= 0 {
		return
	}
	s.bytes += int64(n)
	if time.Since(s.last) >= 100*time.Millisecond {
		s.emit("progress", "")
	}
}

func (s *sessionStats) fileComplete() {
	s.inFile = false
	s.files++
	s.emit("progress", "")
}

func (s *sessionStats) finish(err error) {
	if !s.active {
		return
	}
	if s.inFile {
		message := "transfer interrupted"
		if err != nil {
			message = err.Error()
		}
		s.emit("error", message)
		return
	}
	if s.files > 0 {
		s.emit("complete", "")
	}
}

func (s *sessionStats) emit(kind, message string) {
	if s.send == nil {
		return
	}
	s.last = time.Now()
	s.send(desktop.Event{
		SessionID: s.id,
		Kind:      kind, Direction: s.direction, Name: s.name,
		Bytes: s.bytes, Total: s.total, Files: s.files,
		Duration: time.Since(s.started), Message: message,
	})
}

type countingWriter struct {
	io.Writer
	stats *sessionStats
}

func (w countingWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.stats.copied(n)
	return n, err
}
