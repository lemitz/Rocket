//go:build windows

package desktop

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestPreferredLanguageTag(t *testing.T) {
	for _, tc := range []struct {
		tag string
		ru  bool
	}{{"ru-RU", true}, {"ru", true}, {"en-US", false}, {"fr-FR", false}, {"russian", false}} {
		if got := isRussianLanguage(tc.tag); got != tc.ru {
			t.Errorf("%s: got %v", tc.tag, got)
		}
	}
	russian := &window{russian: true}
	if russian.byteSize(1024) != "1.0 КБ" || russian.duration(65*time.Second) != "1м 05с" || russian.speed(2048, time.Second) != "2.0 КБ/с" {
		t.Fatal("Russian transfer units not localized")
	}
	for key := range russianText {
		w := window{russian: true}
		if w.tr(key) == key {
			t.Errorf("missing Russian translation for %q", key)
		}
		w.russian = false
		if w.tr(key) != key {
			t.Errorf("English fallback for %q", key)
		}
	}
}

func TestFilesLabelPlurals(t *testing.T) {
	ru, en := &window{russian: true}, &window{}
	for n, want := range map[int]string{1: "1 файл", 2: "2 файла", 4: "4 файла", 5: "5 файлов", 11: "11 файлов", 21: "21 файл", 22: "22 файла", 100: "100 файлов", 112: "112 файлов"} {
		if got := ru.filesLabel(n); got != want {
			t.Errorf("ru %d: %q, want %q", n, got, want)
		}
	}
	if en.filesLabel(1) != "1 file" || en.filesLabel(3) != "3 files" {
		t.Error("English plurals")
	}
}

// testWindow builds a window that renders without an HWND, on a fake clock.
func testWindow(width, height int, russian bool) (*window, *time.Time) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	w := newWindow(Options{
		Name: "DESKTOP-ROCKET", UDPOnline: true, Addresses: []string{"Wi-Fi  192.168.1.23:42070", "Ethernet  10.0.0.7:42070"},
		Queue:     func(string, []string) (string, error) { return "", nil },
		QueueText: func(string, string) (string, error) { return "", nil },
	})
	w.russian = russian
	w.width, w.height = width, height
	w.clock = func() time.Time { return now }
	w.folder = `C:\Users\you\Downloads`
	w.phones = []Phone{{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Pixel 8 Pro", LastSeen: now}, {ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Name: "realme GT 6T", LastSeen: now}}
	w.selectedPhone = w.phones[0].ID
	return w, &now
}

func step(w *window, now *time.Time, d time.Duration) {
	for end := now.Add(d); now.Before(end); {
		*now = now.Add(time.Second / 120)
		w.tick(*now)
	}
}

func dump(t *testing.T, w *window, dir, name string) {
	t.Helper()
	w.draw(w.frameTime())
	img := image.NewNRGBA(image.Rect(0, 0, w.fb.w, w.fb.h))
	for i, p := range w.fb.pix {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = byte(p>>16), byte(p>>8), byte(p), 255
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// Set ROCKET_SNAPSHOT_DIR to dump one PNG per screen state for visual review.
func TestRenderStates(t *testing.T) {
	dir := os.Getenv("ROCKET_SNAPSHOT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	for _, lang := range []struct {
		russian bool
		tag     string
	}{{true, "ru"}, {false, "en"}} {
		w, now := testWindow(440, 780, lang.russian)
		defer w.ts.close()
		snap := func(name string) { dump(t, w, dir, lang.tag+"-"+name) }
		snap("1-idle")

		w.paths, w.sizes = []string{`C:\a\holiday.mov`, `C:\a\notes.txt`, `C:\a\photo.jpg`}, []int64{820 << 20, 4096, 6 << 20}
		step(w, now, time.Second)
		snap("2-selected")
		w.sl.pos = .55
		snap("3-dragging")
		w.sl.pos = 0

		w.launch()
		step(w, now, 300*time.Millisecond)
		snap("4-takeoff")
		step(w, now, 1800*time.Millisecond)
		w.onEvent(Event{SessionID: 1, Kind: "start", Direction: "outgoing", Name: "holiday.mov", Total: 100})
		w.onEvent(Event{SessionID: 1, Kind: "progress", Direction: "outgoing", Bytes: 42, Total: 100, Files: 1})
		step(w, now, 800*time.Millisecond)
		snap("5-flight")

		w.onEvent(Event{SessionID: 1, Kind: "complete", Direction: "outgoing", Bytes: 826 << 20, Total: 826 << 20, Files: 3, Duration: 9400 * time.Millisecond})
		step(w, now, 350*time.Millisecond)
		snap("6-departing")
		step(w, now, 500*time.Millisecond)
		snap("7-smoke")
		step(w, now, 1800*time.Millisecond)
		snap("8-result")

		w.dismiss()
		step(w, now, 1200*time.Millisecond)
		snap("9-after")

		w.state = resultFail
		w.failAt = *now
		w.flightStart = now.Add(-2 * time.Second)
		w.failMsg = "The phone did not respond. Open Rocket on it and try again."
		step(w, now, time.Second)
		snap("10-fail")
		w.state = idle

		w.paths = []string{`C:\a\x.txt`}
		w.sizes = []int64{10}
		w.sheetOpen = true
		snap("11-sheet")
		w.sheetOpen = false

		w.screen = scSettings
		step(w, now, time.Second)
		snap("12-settings")
		w.fwState = fwMissing
		w.opts.FixFirewall = func() error { return nil }
		snap("13-settings-fw")

		w.screen, w.state, w.currentSession = scMain, idle, 0
		w.paths, w.sizes, w.message = nil, nil, "Привет, Rocket! 🚀\nДве строки"
		step(w, now, time.Second)
		snap("14-text-selected")
		w.onEvent(Event{SessionID: 8, Kind: "start", Direction: "outgoing", Text: true, Total: int64(len(w.message))})
		w.onEvent(Event{SessionID: 8, Kind: "complete", Direction: "outgoing", Text: true, Files: 1, Bytes: int64(len(w.message))})
		step(w, now, 2600*time.Millisecond)
		snap("15-text-result")
	}
	// A wide window keeps the portrait layout centred.
	w, now := testWindow(1100, 760, false)
	defer w.ts.close()
	w.paths, w.sizes = []string{`C:\a\x.txt`}, []int64{1000}
	step(w, now, time.Second)
	dump(t, w, dir, "en-wide-selected")
	for _, ru := range []bool{false, true} {
		compact, _ := testWindow(398, 709, ru)
		compact.state = resultOK
		compact.current = Event{Direction: "incoming", Text: true, Files: 1}
		tag := "en"
		if ru {
			tag = "ru"
		}
		dump(t, compact, dir, tag+"-compact-clipboard-result")
		compact.ts.close()
	}
}

// Frame cost bounds the refresh rate the software renderer can sustain: 8.3 ms per
// frame for 120 Hz, 16.7 ms for 60 Hz. It logs timings and only fails on absurd cost.
func TestFrameCost(t *testing.T) {
	for _, size := range [][2]int{{440, 780}, {880, 1560}} {
		w, now := testWindow(size[0], size[1], false)
		defer w.ts.close()
		w.paths, w.sizes = []string{`C:\a\x.txt`}, []int64{1000}
		w.launch()
		w.onEvent(Event{SessionID: 1, Kind: "start", Direction: "outgoing", Name: "x", Total: 100})
		step(w, now, 2*time.Second)
		w.draw(*now) // warm caches
		const frames = 60
		start := time.Now()
		for i := 0; i < frames; i++ {
			*now = now.Add(time.Second / 120)
			w.tick(*now)
			w.draw(*now)
		}
		per := time.Since(start) / frames
		t.Logf("%dx%d flight frame: %v", size[0], size[1], per)
		if per > 80*time.Millisecond {
			t.Errorf("frame cost %v is far too high", per)
		}
	}
}

func TestSliderLaunchesOnlyWithTarget(t *testing.T) {
	w, _ := testWindow(440, 780, false)
	defer w.ts.close()
	w.paths, w.sizes = []string{`C:\a\x.txt`}, []int64{1}
	w.selectedPhone = ""
	if w.canLaunch() {
		t.Fatal("must not launch without a destination phone")
	}
	w.sl.pos = .95
	w.releaseSlider()
	if w.state != idle || !w.sl.springing {
		t.Fatalf("slider should spring back, state=%v springing=%v", w.state, w.sl.springing)
	}
	w.selectedPhone = w.phones[0].ID
	w.sl.pos = .6
	w.releaseSlider()
	if w.state != idle {
		t.Fatal("a short drag must not launch")
	}
	w.sl.pos = .9
	w.releaseSlider()
	if w.state != launching {
		t.Fatalf("a full drag must launch, state=%v", w.state)
	}
}

func TestTextSelectionNeedsLaunchAndTarget(t *testing.T) {
	w, _ := testWindow(440, 780, false)
	defer w.ts.close()
	w.message = "Привет!\nRocket"
	if !w.canLaunch() || w.selTarget() != 1 || w.summaryText() != "Message: Привет! Rocket" {
		t.Fatalf("staged text must show a preview and enable launch: %q", w.summaryText())
	}
	w.selectedPhone = ""
	if w.canLaunch() {
		t.Fatal("text must not launch without a target")
	}
	w.selectedPhone = w.phones[0].ID
	w.setPaths([]string{filepath.Join(t.TempDir(), "not-there")})
	// A failed file selection must leave the previously staged text unchanged.
	if w.message == "" {
		t.Fatal("failed file selection cleared a text draft")
	}
	w.onEvent(Event{SessionID: 9, Kind: "start", Text: true, Direction: "incoming"})
	w.onEvent(Event{SessionID: 9, Kind: "complete", Text: true, Direction: "incoming", Files: 1, Bytes: 8})
	if !w.current.Text || w.state != departing {
		t.Fatal("incoming text result must be distinguished from files")
	}
}

func TestIncomingClipboardResultHitAndTitle(t *testing.T) {
	for _, width := range []int{340, 398, 440} {
		for _, russian := range []bool{false, true} {
			w, _ := testWindow(width, 709, russian)
			w.state = resultOK
			w.current = Event{Direction: "incoming", Text: true, Files: 1}
			l := w.layout()
			if w.showOpenFolder() || l.openOut.w != 0 || w.hit(l.ok.x+l.ok.w/2, l.ok.y+l.ok.h/2) != hOK {
				t.Fatalf("clipboard OK was not a dismiss button at %dpx, ru=%v", width, russian)
			}
			title := w.tr("Message copied to clipboard")
			maxWidth, fontSize := int(l.card.w-48*l.u), int(25*l.u+.5)
			lines := w.ts.wrap(title, maxWidth, fontSize, 700, 2)
			if strings.Join(lines, " ") != title {
				t.Errorf("truncated clipboard title at %dpx ru=%v: %q", width, russian, lines)
			}
			for _, line := range lines {
				if w.ts.width(line, fontSize, 700) > maxWidth {
					t.Errorf("clipboard title overflows at %dpx ru=%v: %q", width, russian, line)
				}
			}
			w.dismiss()
			if w.state != idle {
				t.Fatal("dismissing a clipboard result must return to the main screen")
			}
			w.state, w.current = resultOK, Event{Direction: "incoming", Files: 1}
			l = w.layout()
			if !w.showOpenFolder() || w.hit(l.openOut.x+l.openOut.w/2, l.openOut.y+l.openOut.h/2) != hOpenOut ||
				w.hit(l.ok.x+l.ok.w/2, l.ok.y+l.ok.h/2) != hOK {
				t.Fatal("file result must keep separate Open folder and OK buttons")
			}
			w.ts.close()
		}
	}
}

func TestSessionFlow(t *testing.T) {
	w, now := testWindow(440, 780, false)
	defer w.ts.close()
	w.onEvent(Event{SessionID: 1, Kind: "start", Direction: "incoming", Name: "a"})
	w.onEvent(Event{SessionID: 2, Kind: "start", Direction: "incoming", Name: "b"})
	if w.state != flying || w.currentSession != 1 || len(w.waiting) != 1 {
		t.Fatalf("second phone must wait: state=%v session=%d waiting=%d", w.state, w.currentSession, len(w.waiting))
	}
	w.onEvent(Event{SessionID: 1, Kind: "complete", Files: 1, Bytes: 10})
	if w.state != departing || w.current.Files != 1 {
		t.Fatalf("state=%v", w.state)
	}
	step(w, now, 2600*time.Millisecond)
	if w.state != resultOK {
		t.Fatalf("expected resultOK, got %v", w.state)
	}
	w.dismiss()
	if w.state != flying || w.currentSession != 2 {
		t.Fatalf("waiting session should start after dismiss: state=%v session=%d", w.state, w.currentSession)
	}
	w.onEvent(Event{SessionID: 2, Kind: "error", Message: "boom"})
	if w.state != resultFail || w.failMsg != "boom" {
		t.Fatalf("state=%v msg=%q", w.state, w.failMsg)
	}
	w.dismiss()
	if w.state != idle {
		t.Fatalf("state=%v", w.state)
	}
}

// Run manually on a Windows interactive desktop with ROCKET_GUI_SMOKE=1.
func TestInteractiveWindowLifecycle(t *testing.T) {
	if os.Getenv("ROCKET_GUI_SMOKE") != "1" {
		t.Skip("set ROCKET_GUI_SMOKE=1 on an interactive Windows desktop")
	}
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() { done <- Run(events, Options{Name: "GUI smoke test", Addresses: []string{"127.0.0.1"}}) }()
	findWindow := user32.NewProc("FindWindowW")
	class := wide("RocketDesktopWindow")
	var hwnd uintptr
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		hwnd, _, _ = findWindow.Call(uintptr(unsafe.Pointer(class)), 0)
		if hwnd != 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("Run failed: %v", err)
		default:
		}
	}
	if hwnd == 0 {
		t.Fatal("Rocket window did not appear")
	}
	defer postMessage.Call(hwnd, wmClose, 0, 0)
	moveWindow := user32.NewProc("MoveWindow")
	moveWindow.Call(hwnd, 80, 80, 700, 900, 1) // exercise WM_SIZE / target resize
	events <- Event{SessionID: 1, Kind: "start", Direction: "incoming", Name: "smoke.txt", Total: 4096}
	events <- Event{SessionID: 1, Kind: "progress", Direction: "incoming", Bytes: 2048, Total: 4096}
	time.Sleep(1500 * time.Millisecond)
	events <- Event{SessionID: 1, Kind: "complete", Direction: "incoming", Name: "smoke.txt", Bytes: 4096, Total: 4096, Files: 1}
	time.Sleep(3 * time.Second)
	postMessage.Call(hwnd, wmClose, 0, 0)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window did not exit")
	}
}
