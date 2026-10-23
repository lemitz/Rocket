//go:build windows

package desktop

import (
	"fmt"
	"image"
	"os"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// All Win32 calls stay on the window's locked OS thread. No cgo, DLLs shipped
// with the app, external UI runtimes, or background animation when idle.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	registerClass    = user32.NewProc("RegisterClassW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	showWindow       = user32.NewProc("ShowWindow")
	updateWindow     = user32.NewProc("UpdateWindow")
	getMessage       = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postQuitMessage  = user32.NewProc("PostQuitMessage")
	postMessage      = user32.NewProc("PostMessageW")
	loadCursor       = user32.NewProc("LoadCursorW")
	setCursor        = user32.NewProc("SetCursor")
	beginPaint       = user32.NewProc("BeginPaint")
	endPaint         = user32.NewProc("EndPaint")
	getClientRect    = user32.NewProc("GetClientRect")
	getWindowRect    = user32.NewProc("GetWindowRect")
	invalidateRect   = user32.NewProc("InvalidateRect")
	drawText         = user32.NewProc("DrawTextW")
	setCapture       = user32.NewProc("SetCapture")
	releaseCapture   = user32.NewProc("ReleaseCapture")
	sendMessage      = user32.NewProc("SendMessageW")
	setTimer         = user32.NewProc("SetTimer")
	killTimer        = user32.NewProc("KillTimer")
	loadImage        = user32.NewProc("LoadImageW")
	getSystemMetrics = user32.NewProc("GetSystemMetrics")
	setWindowPos     = user32.NewProc("SetWindowPos")
	getDpiForWindow  = user32.NewProc("GetDpiForWindow")
	getDpiForSystem  = user32.NewProc("GetDpiForSystem")
	adjustWindowRect = user32.NewProc("AdjustWindowRectExForDpi")

	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	selectObject       = gdi32.NewProc("SelectObject")
	deleteObject       = gdi32.NewProc("DeleteObject")
	deleteDC           = gdi32.NewProc("DeleteDC")
	setTextColor       = gdi32.NewProc("SetTextColor")
	setBkMode          = gdi32.NewProc("SetBkMode")
	createFont         = gdi32.NewProc("CreateFontW")

	browseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	getPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	shellExecute      = shell32.NewProc("ShellExecuteW")
	dragAcceptFiles   = shell32.NewProc("DragAcceptFiles")
	dragQueryFile     = shell32.NewProc("DragQueryFileW")
	dragFinish        = shell32.NewProc("DragFinish")
	coTaskMemFree     = ole32.NewProc("CoTaskMemFree")
	coInitializeEx    = ole32.NewProc("CoInitializeEx")
	coUninitialize    = ole32.NewProc("CoUninitialize")
	getModuleHandle   = kernel32.NewProc("GetModuleHandleW")
	moveMemory        = kernel32.NewProc("RtlMoveMemory")
	dwmSetAttribute   = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmPaint         = 0x000f
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmGetMinMaxInfo = 0x0024
	wmActivateApp   = 0x001c
	wmSetCursor     = 0x0020
	wmKeyDown       = 0x0100
	wmTimer         = 0x0113
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmMouseWheel    = 0x020a
	wmDropFiles     = 0x0233
	wmDpiChanged    = 0x02e0
	wmNCCreate      = 0x0081
	wmNCDestroy     = 0x0082
	wmAppEvent      = 0x8001

	wsOverlappedWindow = 0x00cf0000
	cwUseDefault       = 0x80000000

	designWidth, designHeight = 440, 780 // client size in logical pixels at 100% scaling
	minWidth, minHeight       = 340, 580
)

type point struct{ x, y int32 }
type rect struct{ left, top, right, bottom int32 }
type msg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	pt             point
	private        uint32
}
type paintStruct struct {
	hdc                uintptr
	erase              int32
	paint              rect
	restore, incUpdate int32
	reserved           [32]byte
}
type wndClass struct {
	style                                                   uint32
	wndProc                                                 uintptr
	clsExtra, wndExtra                                      int32
	instance, icon, cursor, background, menuName, className uintptr
}
type browseInfo struct {
	owner, root, displayName, title uintptr
	flags                           uint32
	callback, param                 uintptr
	image                           int32
}

type screenKind uint8

const (
	scMain screenKind = iota
	scSettings
)

type notification struct {
	kind   string
	event  Event
	folder string
	status string
	ok     bool
	err    error
}

type window struct {
	hwnd uintptr
	opts Options
	done chan struct{}
	mu   sync.Mutex

	queue  []notification
	wake   bool // a wake-up message is already posted (guarded by mu)
	queued bool // an outgoing batch is waiting for the phone to fetch it

	// transfer session
	state           phase
	serverOnline    bool
	current         Event
	currentSession  uint64
	waiting         map[uint64]Event
	finishedQueue   []Event
	started         time.Time
	displayProgress float64
	failMsg         string
	flightStart     time.Time
	departAt        time.Time
	failAt          time.Time
	returnAt        time.Time
	launchDeadline  time.Time

	// selection and destination
	paths         []string
	sizes         []int64
	phones        []Phone
	selectedPhone string
	sheetOpen     bool
	toast         string
	toastUntil    time.Time
	flashAt       time.Time

	// settings screen
	screen     screenKind
	setA       float64
	setScroll  float32
	folder     string
	folderBusy bool
	fwState    int
	fwBusy     bool

	// interaction and animation
	sl               slider
	selA             float64
	hover, pressed   int
	thrust, streak   float64
	scroll           float64
	spawnAcc         float64
	parts            []particle
	seed             uint32
	lastTick         time.Time
	animUntil        time.Time
	clock            func() time.Time
	width, height    int
	scale            float64
	sprite           *image.NRGBA
	spriteBmp        *bitmap
	spriteH          int
	bg, fb           *surface
	ts               *textRenderer
	presenter        presenter
	russian, focused bool
	occluded         bool
	drawErr          error
}

var (
	classOnce      sync.Once
	classErr       error
	windowMu       sync.Mutex
	windows        = make(map[uintptr]*window)
	creatingMu     sync.Mutex // serializes CreateWindow's synchronous WM_NCCREATE callback
	creating       *window
	windowCallback = syscall.NewCallback(windowProc)
)

func wide(s string) *uint16 { return syscall.StringToUTF16Ptr(s) }
func (w *window) frameTime() time.Time {
	if w.clock != nil {
		return w.clock()
	}
	return time.Now()
}

func newWindow(opts Options) *window {
	return &window{opts: opts, done: make(chan struct{}), serverOnline: true, hover: hNone, pressed: hNone,
		focused: true, russian: preferredRussian(), seed: 2463534242, scale: 1,
		sprite: loadRocket(), ts: newTextRenderer(), waiting: map[uint64]Event{}}
}

// Run creates the native window and blocks until it closes. The caller can
// terminate its process after Run returns. Events are marshalled to the UI
// thread; producers never call GDI or manipulate window state directly.
func Run(events <-chan Event, opts Options) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	classOnce.Do(func() {
		instance, _, _ := getModuleHandle.Call(0)
		cursor, _, _ := loadCursor.Call(0, 32512) // IDC_ARROW
		name := wide("RocketDesktopWindow")
		wc := wndClass{wndProc: windowCallback, instance: instance, cursor: cursor, className: uintptr(unsafe.Pointer(name))}
		if result, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
			classErr = fmt.Errorf("register Rocket window: %w", err)
		}
		runtime.KeepAlive(name)
	})
	if classErr != nil {
		return classErr
	}

	w := newWindow(opts)
	defer w.ts.close()
	if err := w.presenter.init(); err != nil {
		return err
	}
	if opts.Folder != nil {
		w.folder = opts.Folder()
	}
	instance, _, _ := getModuleHandle.Call(0)
	className, title := wide("RocketDesktopWindow"), wide("Rocket")
	dpi, _, _ := getDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	client := rect{0, 0, int32(designWidth * int(dpi) / 96), int32(designHeight * int(dpi) / 96)}
	adjustWindowRect.Call(uintptr(unsafe.Pointer(&client)), wsOverlappedWindow, 0, 0, dpi)
	creatingMu.Lock()
	creating = w
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, uintptr(client.right-client.left), uintptr(client.bottom-client.top), 0, 0, instance, 0)
	runtime.KeepAlive(className)
	runtime.KeepAlive(title)
	creating = nil
	creatingMu.Unlock()
	if hwnd == 0 {
		w.presenter.close()
		return fmt.Errorf("create Rocket window: %w", err)
	}
	w.hwnd = hwnd
	w.styleWindow(instance)
	setTimer.Call(hwnd, 1, 1000, 0)
	showWindow.Call(hwnd, 5) // SW_SHOW
	updateWindow.Call(hwnd)
	go func() {
		for {
			select {
			case <-w.done:
				return
			case ev, ok := <-events:
				if !ok {
					w.enqueue(notification{kind: "offline"})
					return
				}
				w.enqueue(notification{kind: "event", event: ev})
			}
		}
	}()
	var m msg
	for {
		// Drain queued input before drawing another frame. EndDraw provides the
		// refresh pacing; GetMessage parks the thread whenever animation stops.
		for {
			r, _, _ := peekMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1) // PM_REMOVE
			if r == 0 {
				break
			}
			if m.message == 0x0012 {
				return w.drawErr
			} // WM_QUIT
			translateMessage.Call(uintptr(unsafe.Pointer(&m)))
			dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
		}
		if w.animate(time.Now()) {
			w.tick(time.Now())
			if err := w.present(); err != nil {
				w.drawErr = err
				destroyWindow.Call(hwnd)
				continue
			}
			if w.occluded {
				// An occluded target may return immediately rather than waiting
				// for vsync. Sleep until input or a slow visibility recheck.
				waitMessage.Call(0, 0, 0x04ff, 250, 4) // QS_ALLINPUT, MWMO_INPUTAVAILABLE
			}
			continue
		}
		w.lastTick = time.Time{}
		r, _, e := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 {
			destroyWindow.Call(hwnd)
			return fmt.Errorf("Rocket message loop: %w", e)
		}
		if r == 0 {
			return w.drawErr
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// styleWindow gives the window its icon, a dark title bar and file-drop support.
func (w *window) styleWindow(instance uintptr) {
	for _, spec := range []struct{ kind, metric uintptr }{{1, 11}, {0, 49}} { // ICON_BIG/SM_CXICON, ICON_SMALL/SM_CXSMICON
		size, _, _ := getSystemMetrics.Call(spec.metric)
		icon, _, _ := loadImage.Call(instance, 1, 1, size, size, 0) // resource 1, IMAGE_ICON
		if icon != 0 {
			sendMessage.Call(w.hwnd, 0x0080, spec.kind, icon) // WM_SETICON
		}
	}
	on := uint32(1)
	dwmSetAttribute.Call(w.hwnd, 20, uintptr(unsafe.Pointer(&on)), 4) // DWMWA_USE_IMMERSIVE_DARK_MODE
	caption := uint32(0x1c1110)                                       // 0x00BBGGRR of the scene's top colour (Windows 11)
	dwmSetAttribute.Call(w.hwnd, 35, uintptr(unsafe.Pointer(&caption)), 4)
	border := uint32(0x2b1a1b)
	dwmSetAttribute.Call(w.hwnd, 34, uintptr(unsafe.Pointer(&border)), 4)
	dragAcceptFiles.Call(w.hwnd, 1)
	dpi, _, _ := getDpiForWindow.Call(w.hwnd)
	if dpi != 0 {
		w.scale = float64(dpi) / 96
	}
}

func (w *window) animate(now time.Time) bool {
	if !w.focused || w.width <= 0 || w.height <= 0 {
		return false
	}
	switch w.state {
	case launching, flying, departing:
		return true
	}
	return w.sl.drag || w.sl.springing || w.selA != w.selTarget() ||
		w.setA != map[bool]float64{true: 1, false: 0}[w.screen == scSettings] ||
		now.Before(w.animUntil) || len(w.parts) > 0 || w.thrust > .01 || w.streak > .01
}

func (w *window) invalidate() { invalidateRect.Call(w.hwnd, 0, 0) }

func (w *window) enqueue(n notification) {
	w.mu.Lock()
	select {
	case <-w.done:
		w.mu.Unlock()
		return
	default:
	}
	// A busy sender should not build an unbounded backlog of progress updates.
	if n.kind == "event" && n.event.Kind == "progress" && len(w.queue) != 0 {
		last := &w.queue[len(w.queue)-1]
		if last.kind == "event" && last.event.Kind == "progress" && last.event.SessionID == n.event.SessionID {
			*last = n
		} else {
			w.queue = append(w.queue, n)
		}
	} else {
		w.queue = append(w.queue, n)
	}
	if !w.wake {
		w.wake = true
		postMessage.Call(w.hwnd, wmAppEvent, 0, 0)
	}
	w.mu.Unlock()
}

func windowProc(hwnd uintptr, message uint32, wp, lp uintptr) uintptr {
	if message == wmNCCreate {
		// CreateWindow sends WM_NCCREATE synchronously on its calling thread.
		w := creating
		if w == nil {
			return 0
		}
		w.hwnd = hwnd
		windowMu.Lock()
		windows[hwnd] = w
		windowMu.Unlock()
		// DefWindowProc handles creation bookkeeping, including the window title.
		r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wp, lp)
		return r
	}
	windowMu.Lock()
	w := windows[hwnd]
	windowMu.Unlock()
	if w == nil {
		r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wp, lp)
		return r
	}
	switch message {
	case wmGetMinMaxInfo:
		// MINMAXINFO.ptMinTrackSize is the fourth POINT (byte offset 24).
		// Copy via Win32 so callback uintptrs are not reinterpreted as Go pointers.
		var outer, inner rect
		getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&outer)))
		getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&inner)))
		minimum := point{int32(float64(minWidth)*w.scale) + (outer.right - outer.left - inner.right),
			int32(float64(minHeight)*w.scale) + (outer.bottom - outer.top - inner.bottom)}
		moveMemory.Call(lp+24, uintptr(unsafe.Pointer(&minimum)), unsafe.Sizeof(minimum))
		return 0
	case wmEraseBkgnd:
		return 1
	case wmSize:
		w.width, w.height = int(uint16(lp)), int(uint16(lp>>16))
		w.setScroll = min(w.setScroll, w.maxSettingsScroll(w.layout()))
		w.invalidate()
		return 0
	case wmDpiChanged:
		w.scale = float64(uint16(wp>>16)) / 96
		var suggested rect
		moveMemory.Call(uintptr(unsafe.Pointer(&suggested)), lp, unsafe.Sizeof(suggested))
		setWindowPos.Call(hwnd, 0, uintptr(suggested.left), uintptr(suggested.top),
			uintptr(suggested.right-suggested.left), uintptr(suggested.bottom-suggested.top), 0x14) // NOZORDER|NOACTIVATE
		return 0
	case wmActivateApp:
		w.focused = wp != 0
		w.lastTick = time.Time{}
		if w.focused {
			w.invalidate()
		}
		return 0
	case wmPaint:
		w.paint()
		return 0
	case wmAppEvent:
		w.drain()
		return 0
	case wmTimer:
		w.syncPhones()
		if w.screen == scSettings {
			w.invalidate()
		}
		return 0
	case wmSetCursor:
		if uint16(lp) == 1 && w.hover != hNone { // HTCLIENT over something clickable
			hand, _, _ := loadCursor.Call(0, 32649) // IDC_HAND
			setCursor.Call(hand)
			return 1
		}
	case wmMouseMove:
		x, y := f32(float64(int16(lp))), f32(float64(int16(lp>>16)))
		if w.sl.drag {
			l := w.layout()
			w.sl.pos = clamp(float64((x-w.sl.grab-l.minX)/l.travel), 0, 1)
			w.invalidate()
			return 0
		}
		if h := w.hit(x, y); h != w.hover {
			w.hover = h
			w.invalidate()
		}
		return 0
	case wmLButtonDown:
		x, y := f32(float64(int16(lp))), f32(float64(int16(lp>>16)))
		w.pressed = w.hit(x, y)
		if w.pressed >= 0 {
			setCapture.Call(hwnd)
			if w.pressed == hThumb {
				w.sl.drag, w.sl.springing = true, false
				w.sl.grab = x - w.layout().thumb.x
			}
			w.invalidate()
		}
		return 0
	case wmLButtonUp:
		releaseCapture.Call()
		x, y := f32(float64(int16(lp))), f32(float64(int16(lp>>16)))
		pressed := w.pressed
		w.pressed = hNone
		if w.sl.drag {
			w.releaseSlider()
		} else if h := w.hit(x, y); h == pressed && h >= 0 {
			w.click(h)
		}
		w.hover = w.hit(x, y)
		w.invalidate()
		return 0
	case wmMouseWheel:
		if w.screen == scSettings {
			l := w.layout()
			delta := float32(int16(wp>>16)) / 120
			w.setScroll = clampF(w.setScroll-delta*54*l.u, 0, w.maxSettingsScroll(l))
			w.invalidate()
		}
		return 0
	case wmKeyDown:
		w.key(wp)
		return 0
	case wmDropFiles:
		w.drop(wp)
		return 0
	case wmClose:
		destroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		killTimer.Call(hwnd, 1)
		close(w.done)
		w.presenter.close()
		postQuitMessage.Call(0)
		return 0
	case wmNCDestroy:
		windowMu.Lock()
		delete(windows, hwnd)
		windowMu.Unlock()
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wp, lp)
	return r
}

func clampF(v, lo, hi float32) float32 { return max(lo, min(hi, v)) }

func (w *window) paint() {
	var ps paintStruct
	hdc, _, _ := beginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer endPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&ps)))
	var bounds rect
	getClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&bounds)))
	w.width, w.height = int(bounds.right), int(bounds.bottom)
	if err := w.present(); err != nil {
		w.drawErr = err
		postMessage.Call(w.hwnd, wmClose, 0, 0)
	}
}

func (w *window) drain() {
	w.mu.Lock()
	q := w.queue
	w.queue = nil
	w.wake = false
	w.mu.Unlock()
	for _, n := range q {
		switch n.kind {
		case "event":
			w.onEvent(n.event)
		case "offline":
			w.serverOnline = false
		case "send":
			if n.err != nil {
				if w.state == launching {
					w.fail(fmt.Sprintf(w.tr("Could not queue the files: %v"), n.err))
				}
			} else {
				w.queued = true
				w.launchDeadline = w.frameTime().Add(25 * time.Second)
			}
		case "folder":
			w.folderBusy = false
			if n.err != nil {
				w.showToast(w.tr("Could not change the folder. Check access and try again."))
			} else {
				w.folder = n.folder
				if w.opts.Folder != nil {
					if current := w.opts.Folder(); current != "" {
						w.folder = current
					}
				}
				w.showToast(w.tr("Folder changed"))
			}
		case "fw":
			w.fwBusy = false
			w.fwState = fwMissing
			if n.ok {
				w.fwState = fwPresent
			}
		}
	}
	w.invalidate()
}

func (w *window) showToast(text string) {
	w.toast = text
	w.toastUntil = w.frameTime().Add(3500 * time.Millisecond)
	w.animUntil = w.toastUntil
}

// ---- transfer state --------------------------------------------------------

func (w *window) beginFlight(e Event) {
	now := w.frameTime()
	w.currentSession = e.SessionID
	w.current = e
	if w.state != launching {
		w.flightStart = now
	}
	w.state = flying
	w.started = now
	w.displayProgress = 0
	w.screen = scMain
	w.sheetOpen = false
	w.sl.pos, w.sl.drag, w.sl.springing = 0, false, false
	w.returnAt = time.Time{}
	w.parts = w.parts[:0]
}

func (w *window) fail(message string) {
	now := w.frameTime()
	if w.state != flying && w.state != launching {
		w.flightStart = now.Add(-1500 * time.Millisecond)
	}
	w.failMsg = message
	w.failAt = now
	w.state = resultFail
	w.queued = false
	w.sl.pos = 0
	w.screen = scMain
	w.sheetOpen = false
}

func (w *window) onEvent(e Event) {
	now := w.frameTime()
	// Several phones may transfer simultaneously. Show each flight in turn;
	// never combine another TCP session's bytes with the visible statistics.
	if e.SessionID != 0 && w.currentSession != 0 && e.SessionID != w.currentSession &&
		(w.state == flying || w.state == departing) {
		if e.Kind == "complete" || e.Kind == "error" {
			delete(w.waiting, e.SessionID)
			w.finishedQueue = append(w.finishedQueue, e)
		} else {
			w.waiting[e.SessionID] = e
		}
		return
	}
	switch e.Kind {
	case "start":
		w.beginFlight(e)
	case "progress":
		if w.state != flying {
			w.beginFlight(Event{SessionID: e.SessionID, Kind: "start", Direction: e.Direction, Name: e.Name})
		}
		if e.Direction != "" {
			w.current.Direction = e.Direction
		}
		if e.Name != "" {
			w.current.Name = e.Name
		}
		w.current.Bytes, w.current.Total, w.current.Files = e.Bytes, e.Total, e.Files
	case "complete":
		w.currentSession = e.SessionID
		if w.state != flying && w.state != launching {
			w.started = time.Time{}
			w.current = Event{}
			w.flightStart = now.Add(-1500 * time.Millisecond)
			w.screen = scMain
		}
		if e.Direction == "" {
			e.Direction = w.current.Direction
		}
		if e.Name == "" {
			e.Name = w.current.Name
		}
		if e.Bytes == 0 {
			e.Bytes = w.current.Bytes
		}
		if e.Total == 0 {
			e.Total = w.current.Total
		}
		if e.Files == 0 {
			e.Files = w.current.Files
		}
		if e.Duration <= 0 && !w.started.IsZero() {
			e.Duration = now.Sub(w.started)
		}
		w.current = e
		w.departAt = now
		w.state = departing
		w.sheetOpen = false
		if e.Direction == "outgoing" && w.queued {
			w.paths, w.sizes = nil, nil // a fresh source selection is needed for the next launch
			w.queued = false
		}
	case "error":
		w.currentSession = e.SessionID
		if e.Direction == "outgoing" && w.opts.Unqueue != nil {
			w.opts.Unqueue(w.selectedPhone)
		}
		w.current = e
		w.fail(w.transferError(e.Message))
	}
}

// dismiss closes the result card and either shows the next waiting session or
// brings the idle rocket back.
func (w *window) dismiss() {
	now := w.frameTime()
	w.state = idle
	w.currentSession = 0
	if len(w.finishedQueue) > 0 {
		next := w.finishedQueue[0]
		w.finishedQueue = w.finishedQueue[1:]
		w.onEvent(next)
		return
	}
	for id, next := range w.waiting {
		delete(w.waiting, id)
		w.onEvent(Event{SessionID: id, Kind: "start", Direction: next.Direction, Name: next.Name, Total: next.Total})
		if next.Kind == "progress" {
			w.onEvent(next)
		}
		return
	}
	w.current = Event{}
	w.displayProgress = 0
	w.returnAt = now
	w.animUntil = now.Add(900 * time.Millisecond)
	w.syncPhones()
}

// ---- selection -------------------------------------------------------------

func (w *window) setPaths(paths []string) {
	var valid []string
	var sizes []int64
	skipped := false
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			skipped = true
			continue
		}
		valid = append(valid, p)
		sizes = append(sizes, info.Size())
	}
	if len(valid) == 0 {
		if skipped {
			w.showToast(w.tr("Only regular files can be sent"))
		}
		return
	}
	if len(valid) > 256 {
		valid, sizes = valid[:256], sizes[:256]
		w.showToast(w.tr("Too many files (up to 256)"))
	} else if skipped {
		w.showToast(w.tr("Only regular files can be sent"))
	}
	w.paths, w.sizes = valid, sizes
	w.sl.pos = 0
	w.syncPhones()
}

func (w *window) drop(hdrop uintptr) {
	defer dragFinish.Call(hdrop)
	count, _, _ := dragQueryFile.Call(hdrop, 0xffffffff, 0, 0)
	var paths []string
	for i := uintptr(0); i < count && i < 4096; i++ {
		var buf [32768]uint16
		n, _, _ := dragQueryFile.Call(hdrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			paths = append(paths, syscall.UTF16ToString(buf[:n]))
		}
	}
	if w.state == idle && w.screen == scMain && w.opts.Queue != nil && len(paths) > 0 {
		w.setPaths(paths)
		w.invalidate()
	}
}

func (w *window) syncPhones() {
	if w.opts.Phones == nil {
		return
	}
	phones := w.opts.Phones()
	sort.Slice(phones, func(i, j int) bool {
		if phones[i].Name != phones[j].Name {
			return phones[i].Name < phones[j].Name
		}
		return phones[i].ID < phones[j].ID
	})
	changed := len(phones) != len(w.phones)
	for i := 0; !changed && i < len(phones); i++ {
		changed = phones[i].ID != w.phones[i].ID || phones[i].Name != w.phones[i].Name
	}
	w.phones = phones
	before := w.selectedPhone
	found := false
	for _, p := range phones {
		found = found || p.ID == w.selectedPhone
	}
	if !found {
		w.selectedPhone = ""
		if w.opts.LastPhone != nil {
			last := w.opts.LastPhone()
			for _, p := range phones {
				if p.ID == last {
					w.selectedPhone = last
				}
			}
		}
		if w.selectedPhone == "" && len(phones) == 1 {
			w.selectedPhone = phones[0].ID
		}
	}
	if w.sheetOpen && len(phones) == 0 {
		w.sheetOpen = false
	}
	if changed || before != w.selectedPhone {
		w.invalidate()
	}
}

// ---- input -----------------------------------------------------------------

func (w *window) hit(x, y float32) int {
	l := w.layout()
	if w.sheetOpen && w.state == idle {
		for i, r := range l.rows {
			if r.contains(x, y) {
				return hPhoneBase + i
			}
		}
		if l.sheet.contains(x, y) {
			return hNone
		}
		return hSheetOut
	}
	if w.screen == scSettings && w.setA > .5 {
		st := w.settingsLayout(l)
		if st.back.inset(-4*l.u).contains(x, y) {
			return hBack
		}
		if y < st.top-6*l.u {
			return hNone
		}
		switch {
		case st.folderChange.contains(x, y) && w.opts.ChangeFolder != nil && !w.folderBusy:
			return hFolder
		case st.folderOpen.contains(x, y):
			return hFolderOpen
		case st.fwButton.contains(x, y) && !w.fwBusy:
			if w.opts.FixFirewall != nil && w.fwState == fwMissing {
				return hFirewall
			}
			return hRecheck
		}
		return hNone
	}
	switch w.state {
	case idle:
		if w.uiAlpha(w.frameTime()) < .5 {
			return hNone
		}
		if l.gear.contains(x, y) {
			return hGear
		}
		if w.selA < .5 {
			if l.choose.contains(x, y) {
				return hChoose
			}
			return hNone
		}
		switch {
		case l.clear.contains(x, y):
			return hClear
		case l.summary.contains(x, y):
			return hSummary
		case l.thumb.inset(-8*l.u).contains(x, y):
			return hThumb
		case l.target.contains(x, y):
			if len(w.phones) > 1 || (w.selectedPhone == "" && len(w.phones) > 0) {
				return hTarget
			}
		}
	case resultOK, resultFail, departing:
		if w.state == departing && w.frameTime().Sub(w.departAt) < 1300*time.Millisecond {
			return hNone
		}
		if w.state == resultOK && w.current.Direction == "incoming" && l.openOut.contains(x, y) {
			return hOpenOut
		}
		if l.ok.contains(x, y) {
			return hOK
		}
	}
	return hNone
}

func (w *window) releaseSlider() {
	w.sl.drag = false
	if w.sl.pos >= .85 {
		if w.canLaunch() {
			w.launch()
			return
		}
		w.flashAt = w.frameTime()
		w.animUntil = w.flashAt.Add(time.Second)
	}
	w.sl.springing, w.sl.from, w.sl.t0 = true, w.sl.pos, w.frameTime()
}

func (w *window) launch() {
	now := w.frameTime()
	w.state = launching
	w.flightStart = now
	w.queued = false
	w.sheetOpen = false
	w.displayProgress = 0
	w.current = Event{}
	w.returnAt = time.Time{}
	w.launchDeadline = now.Add(60 * time.Second) // replaced once the queue call returns
	phone, paths := w.selectedPhone, append([]string(nil), w.paths...)
	queue := w.opts.Queue
	go func() {
		status, err := queue(phone, paths)
		w.enqueue(notification{kind: "send", status: status, err: err})
	}()
}

func (w *window) click(id int) {
	switch {
	case id == hGear:
		w.screen = scSettings
		w.setScroll = 0
		w.syncPhones()
		w.checkFirewall()
	case id == hBack:
		w.screen = scMain
	case id == hChoose || id == hSummary:
		if paths := chooseFiles(w.hwnd, w.tr("Select files to send")); len(paths) > 0 {
			w.setPaths(paths)
		}
	case id == hClear:
		w.paths, w.sizes = nil, nil
		w.sheetOpen = false
	case id == hTarget:
		w.sheetOpen = true
	case id == hSheetOut:
		w.sheetOpen = false
	case id >= hPhoneBase && id-hPhoneBase < len(w.phones):
		w.selectedPhone = w.phones[id-hPhoneBase].ID
		if w.opts.RememberPhone != nil {
			w.opts.RememberPhone(w.selectedPhone)
		}
		w.sheetOpen = false
	case id == hOK:
		w.dismiss()
	case id == hOpenOut || id == hFolderOpen:
		w.openFolder()
	case id == hFolder:
		if folder := chooseFolder(w.hwnd, w.tr("Select the Rocket receive folder")); folder != "" {
			w.folderBusy = true
			change := w.opts.ChangeFolder
			go func() { w.enqueue(notification{kind: "folder", folder: folder, err: change(folder)}) }()
		}
	case id == hFirewall:
		w.fwBusy = true
		fix, check := w.opts.FixFirewall, w.opts.FirewallOK
		go func() {
			if err := fix(); err == nil {
				time.Sleep(1500 * time.Millisecond)
			}
			w.enqueue(notification{kind: "fw", ok: check != nil && check()})
		}()
	case id == hRecheck:
		w.checkFirewall()
	}
	w.invalidate()
}

func (w *window) checkFirewall() {
	if w.opts.FirewallOK == nil || w.fwBusy {
		return
	}
	w.fwBusy = true
	w.fwState = fwUnknown
	check := w.opts.FirewallOK
	go func() { w.enqueue(notification{kind: "fw", ok: check()}) }()
}

func (w *window) openFolder() {
	if w.folder == "" {
		w.showToast(w.tr("Could not open the folder"))
		return
	}
	verb, path := wide("open"), wide(w.folder)
	result, _, _ := shellExecute.Call(w.hwnd, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(path)), 0, 0, 1)
	runtime.KeepAlive(verb)
	runtime.KeepAlive(path)
	if result <= 32 {
		w.showToast(w.tr("Could not open the folder"))
	}
}

func (w *window) key(vk uintptr) {
	switch vk {
	case 0x1b: // Escape
		switch {
		case w.sheetOpen:
			w.sheetOpen = false
		case w.screen == scSettings:
			w.screen = scMain
		case w.state == resultOK || w.state == resultFail:
			w.dismiss()
		}
	case 0x0d: // Enter
		switch {
		case w.state == resultOK || w.state == resultFail:
			w.dismiss()
		case w.state == idle && w.screen == scMain && len(w.paths) == 0:
			w.click(hChoose)
		}
	}
	w.invalidate()
}

func chooseFolder(owner uintptr, caption string) string {
	// SHBrowseForFolder is the built-in Windows shell picker. Initialize COM
	// on this UI thread for the lifetime of the modal picker.
	hr, _, _ := coInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	if int32(hr) >= 0 {
		defer coUninitialize.Call()
	}
	var display [260]uint16
	title := wide(caption)
	info := browseInfo{owner: owner, displayName: uintptr(unsafe.Pointer(&display[0])), title: uintptr(unsafe.Pointer(title)), flags: 0x41 | 0x10}
	id, _, _ := browseForFolder.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(title)
	runtime.KeepAlive(&display)
	if id == 0 {
		return ""
	}
	defer coTaskMemFree.Call(id)
	var path [32768]uint16
	ok, _, _ := getPathFromIDList.Call(id, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(path[:])
}
