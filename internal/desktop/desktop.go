package desktop

import (
	"fmt"
	"time"
)

// Event describes a transfer notification sent to the desktop window.
type Event struct {
	SessionID uint64 // one TCP transfer batch; prevents mixed stats from concurrent phones.
	Kind      string // "start", "progress", "complete", "error"
	Direction string
	Name      string
	Bytes     int64
	Total     int64
	Files     int
	Duration  time.Duration
	Message   string
}

// Phone is a discovered destination available for sending files from this PC.
type Phone struct {
	ID       string
	Name     string
	LastSeen time.Time
}

// Options supplies the identity, destinations and actions shown by the desktop window.
type Options struct {
	Name         string
	ID           string
	Addresses    []string
	Folder       func() string
	ChangeFolder func(path string) error
	// Phones is queried on demand when the phone selector is pressed.
	Phones func() []Phone
	// Queue receives local paths from the picker. The handler must revalidate
	// each path as a regular file before queuing; its return value is UI status.
	Queue func(phoneID string, paths []string) (string, error)
}

func byteSize(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, unit := range []string{"KB", "MB", "GB", "TB", "PB"} {
		v /= 1024
		if v < 1024 || unit == "PB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
	}
	return "0 B"
}

func durationLabel(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d > 0 && d < time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	seconds := int64(d.Round(time.Second) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %02dm", seconds/3600, (seconds%3600)/60)
}

func speedLabel(bytes int64, d time.Duration) string {
	if d <= 0 || bytes <= 0 {
		return "—"
	}
	return byteSize(int64(float64(bytes)/d.Seconds())) + "/s"
}
