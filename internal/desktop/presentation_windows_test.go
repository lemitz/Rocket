//go:build windows

package desktop

import (
	"os"
	"runtime"
	"sort"
	"syscall"
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

// Run manually on a Windows interactive desktop with ROCKET_GUI_SMOKE=1.
// This probes real EndDraw intervals on the current display; it does not
// assert a specific FPS or assume presentation implies unique visible frames.
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
	moveWindow.Call(hwnd, 80, 80, 1100, 740, 1) // exercise WM_SIZE / target resize
	events <- Event{SessionID: 1, Kind: "start", Direction: "incoming", Name: "smoke.txt", Total: 4096}
	events <- Event{SessionID: 1, Kind: "progress", Direction: "incoming", Bytes: 2048, Total: 4096}
	time.Sleep(200 * time.Millisecond)
	events <- Event{SessionID: 1, Kind: "complete", Direction: "incoming", Name: "smoke.txt", Bytes: 4096, Total: 4096, Files: 1}
	time.Sleep(2300 * time.Millisecond)
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

func TestInteractivePresentation(t *testing.T) {
	if os.Getenv("ROCKET_GUI_SMOKE") != "1" {
		t.Skip("set ROCKET_GUI_SMOKE=1 on an interactive Windows desktop")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w := &window{width: 990, height: 675, state: transferring, focused: true, serverOnline: true, hover: -1, pressed: -1}
	if err := w.presenter.init(); err != nil {
		t.Fatal(err)
	}
	defer w.presenter.close()
	instance, _, _ := getModuleHandle.Call(0)
	className := wide("RocketPresentationSmoke")
	cb := syscall.NewCallback(func(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
		if message == wmPaint {
			var ps paintStruct
			beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
			return 0
		}
		r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wp, lp)
		return r
	})
	wc := wndClass{wndProc: cb, instance: instance, className: uintptr(unsafe.Pointer(className))}
	if r, _, e := registerClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		t.Fatalf("register class: %v", e)
	}
	w.hwnd, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(wide("Rocket presentation smoke test"))),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, 990, 675, 0, 0, instance, 0)
	if w.hwnd == 0 {
		t.Fatal("CreateWindowExW failed")
	}
	defer destroyWindow.Call(w.hwnd)
	w.initFonts()
	defer func() {
		for _, font := range w.fonts {
			if font != 0 {
				deleteObject.Call(font)
			}
		}
	}()
	showWindow.Call(w.hwnd, 5)
	updateWindow.Call(w.hwnd)
	getDC := user32.NewProc("GetDC")
	releaseDC := user32.NewProc("ReleaseDC")
	getDeviceCaps := gdi32.NewProc("GetDeviceCaps")
	dc, _, _ := getDC.Call(w.hwnd)
	refresh, _, _ := getDeviceCaps.Call(dc, 116) // VREFRESH
	releaseDC.Call(w.hwnd, dc)
	var intervals []time.Duration
	last := time.Now()
	for i := 0; i < 240; i++ {
		var m msg
		for {
			r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
			if r == 0 {
				break
			}
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
		if err := w.present(); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if i > 0 {
			intervals = append(intervals, now.Sub(last))
		}
		last = now
		if w.occluded {
			t.Skip("smoke window occluded; vsync timing unavailable")
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i] < intervals[j] })
	t.Logf("GDI VREFRESH=%d Hz; EndDraw intervals n=%d min=%s median=%s p90=%s max=%s (not measured on-screen unique frames)",
		refresh, len(intervals), intervals[0], intervals[len(intervals)/2], intervals[len(intervals)*9/10], intervals[len(intervals)-1])
	runtime.KeepAlive(className)
	runtime.KeepAlive(cb)
}
