package desktop

import (
	"strings"
	"testing"
	"time"
)

func TestFitTextTruncatesUnicodeAndBoundsMeasurements(t *testing.T) {
	calls := 0
	width := func(s string) int {
		calls++
		return len([]rune(s))
	}

	got := fitText("ab你de🙂f", 5, width)
	if got != "ab你d…" {
		t.Fatalf("fitText() = %q; want %q", got, "ab你d…")
	}
	if calls > 6 {
		t.Fatalf("fitText used %d measurements for a short string", calls)
	}

	calls = 0
	long := strings.Repeat("x", 8192)
	got = fitText(long, 40, width)
	if got != strings.Repeat("x", 39)+"…" {
		t.Fatalf("long fitText() has unexpected result length/content: len=%d", len(got))
	}
	if calls > 16 {
		t.Fatalf("fitText used %d measurements for an 8 KiB string; want logarithmic work", calls)
	}
}

func TestTransferLabels(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{-12, "0 B"}, {0, "0 B"}, {1023, "1023 B"},
		{1024, "1.0 KB"}, {1536, "1.5 KB"}, {1048576, "1.0 MB"},
	} {
		if got := byteSize(tc.bytes); got != tc.want {
			t.Errorf("byteSize(%d) = %q; want %q", tc.bytes, got, tc.want)
		}
	}
	if got := durationLabel(65 * time.Second); got != "1m 05s" {
		t.Errorf("duration = %q", got)
	}
	if got := durationLabel(250 * time.Millisecond); got != "0.25s" {
		t.Errorf("subsecond duration = %q", got)
	}
	if got := speedLabel(2048, time.Second); got != "2.0 KB/s" {
		t.Errorf("speed = %q", got)
	}
	if got := speedLabel(2048, 0); got != "—" {
		t.Errorf("zero-duration speed = %q", got)
	}
}
