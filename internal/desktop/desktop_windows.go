//go:build windows

package desktop

import (
	"fmt"
	"math"
	"runtime"
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
	beginPaint       = user32.NewProc("BeginPaint")
	endPaint         = user32.NewProc("EndPaint")
	getClientRect    = user32.NewProc("GetClientRect")
	invalidateRect   = user32.NewProc("InvalidateRect")
	drawText         = user32.NewProc("DrawTextW")
	fillRect         = user32.NewProc("FillRect")
	setCapture       = user32.NewProc("SetCapture")
	releaseCapture   = user32.NewProc("ReleaseCapture")
	sendMessage      = user32.NewProc("SendMessageW")

	createCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	selectObject       = gdi32.NewProc("SelectObject")
	deleteObject       = gdi32.NewProc("DeleteObject")
	deleteDC           = gdi32.NewProc("DeleteDC")
	getStockObject     = gdi32.NewProc("GetStockObject")
	setDCBrushColor    = gdi32.NewProc("SetDCBrushColor")
	setDCPenColor      = gdi32.NewProc("SetDCPenColor")
	setTextColor       = gdi32.NewProc("SetTextColor")
	setBkMode          = gdi32.NewProc("SetBkMode")
	roundRect          = gdi32.NewProc("RoundRect")
	ellipse            = gdi32.NewProc("Ellipse")
	polygon            = gdi32.NewProc("Polygon")
	moveToEx           = gdi32.NewProc("MoveToEx")
	lineTo             = gdi32.NewProc("LineTo")
	createFont         = gdi32.NewProc("CreateFontW")

	browseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	getPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	shellExecute      = shell32.NewProc("ShellExecuteW")
	coTaskMemFree     = ole32.NewProc("CoTaskMemFree")
	coInitializeEx    = ole32.NewProc("CoInitializeEx")
	coUninitialize    = ole32.NewProc("CoUninitialize")
	getModuleHandle   = kernel32.NewProc("GetModuleHandleW")
	moveMemory        = kernel32.NewProc("RtlMoveMemory")
)

const (
	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmPaint         = 0x000f
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmGetMinMaxInfo = 0x0024
	wmActivateApp   = 0x001c
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmNCCreate      = 0x0081
	wmNCDestroy     = 0x0082
	wmAppEvent      = 0x8001

	wsOverlappedWindow = 0x00cf0000
	cwUseDefault       = 0x80000000
	dcBrush            = 18
	dcPen              = 19
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

type phase uint8

const (
	idle phase = iota
	transferring
	departing
	finished
	failed
)

type notification struct {
	kind   string
	event  Event
	folder string
	phones []Phone
	status string
	err    error
}

type window struct {
	hwnd                             uintptr
	opts                             Options
	done                             chan struct{}
	mu                               sync.Mutex
	queue                            []notification
	queued                           bool
	state                            phase
	serverOnline                     bool
	current                          Event
	currentSession                   uint64
	waiting                          map[uint64]Event
	finishedQueue                    []Event
	started, departure               time.Time
	displayProgress                  float64
	departY                          float64
	folder, notice                   string
	folderBusy                       bool
	paths                            []string
	phones                           []Phone
	selectedPhone                    string
	phonesBusy, sendBusy, queuedSend bool
	hover, pressed                   int
	width, height                    int
	fonts                            [4]uintptr
	presenter                        presenter
	russian, focused, occluded       bool
	lastFrame                        time.Time
	drawErr                          error
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

func wide(s string) *uint16   { return syscall.StringToUTF16Ptr(s) }
func rgb(r, g, b int) uintptr { return uintptr(uint32(r) | uint32(g)<<8 | uint32(b)<<16) }

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
		wc := wndClass{style: 0x0003, wndProc: windowCallback, instance: instance, cursor: cursor, className: uintptr(unsafe.Pointer(name))}
		if result, _, err := registerClass.Call(uintptr(unsafe.Pointer(&wc))); result == 0 {
			classErr = fmt.Errorf("register Rocket window: %w", err)
		}
		runtime.KeepAlive(name)
	})
	if classErr != nil {
		return classErr
	}

	w := &window{opts: opts, done: make(chan struct{}), serverOnline: true, hover: -1, pressed: -1, focused: true, russian: preferredRussian()}
	if err := w.presenter.init(); err != nil {
		return err
	}
	if opts.Folder != nil {
		w.folder = opts.Folder()
	}
	instance, _, _ := getModuleHandle.Call(0)
	className, title := wide("RocketDesktopWindow"), wide("Rocket")
	creatingMu.Lock()
	creating = w
	hwnd, _, err := createWindow.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow, cwUseDefault, cwUseDefault, 990, 675, 0, 0, instance, 0)
	runtime.KeepAlive(className)
	runtime.KeepAlive(title)
	creating = nil
	creatingMu.Unlock()
	if hwnd == 0 {
		w.presenter.close()
		return fmt.Errorf("create Rocket window: %w", err)
	}
	w.hwnd = hwnd
	w.initFonts()
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
		if w.animate() {
			w.tick()
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

func (w *window) animate() bool {
	return w.focused && w.width > 0 && w.height > 0 && (w.state == transferring || w.state == departing)
}

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
	if !w.queued {
		w.queued = true
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
		minimum := point{800, 660}
		moveMemory.Call(lp+24, uintptr(unsafe.Pointer(&minimum)), unsafe.Sizeof(minimum))
		return 0
	case wmEraseBkgnd:
		return 1
	case wmSize:
		w.width, w.height = int(uint16(lp)), int(uint16(lp>>16))
		invalidateRect.Call(hwnd, 0, 0)
		return 0
	case wmActivateApp:
		w.focused = wp != 0
		w.lastFrame = time.Time{}
		if w.focused {
			invalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case wmPaint:
		w.paint()
		return 0
	case wmAppEvent:
		w.drain()
		return 0
	case wmMouseMove:
		x, y := int(int16(lp)), int(int16(lp>>16))
		h := w.hit(x, y)
		if h != w.hover {
			w.hover = h
			invalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case wmLButtonDown:
		w.pressed = w.hit(int(int16(lp)), int(int16(lp>>16)))
		if w.pressed >= 0 {
			setCapture.Call(hwnd)
			invalidateRect.Call(hwnd, 0, 0)
		}
		return 0
	case wmLButtonUp:
		releaseCapture.Call()
		h := w.hit(int(int16(lp)), int(int16(lp>>16)))
		pressed := w.pressed
		w.pressed = -1
		invalidateRect.Call(hwnd, 0, 0)
		if h == pressed && h >= 0 {
			w.click(h)
		}
		return 0
	case wmClose:
		destroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		close(w.done)
		w.presenter.close()
		for _, font := range w.fonts {
			if font != 0 {
				deleteObject.Call(font)
			}
		}
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

func (w *window) drain() {
	w.mu.Lock()
	q := w.queue
	w.queue = nil
	w.queued = false
	w.mu.Unlock()
	for _, n := range q {
		switch n.kind {
		case "event":
			w.onEvent(n.event)
		case "offline":
			w.serverOnline = false
		case "phones":
			w.phonesBusy = false
			w.phones = nil
			for _, phone := range n.phones {
				if phone.ID != "" {
					w.phones = append(w.phones, phone)
				}
			}
			found := false
			for _, phone := range w.phones {
				if phone.ID == w.selectedPhone {
					found = true
				}
			}
			if !found {
				w.selectedPhone = ""
				w.queuedSend = false
			}
			if len(w.phones) == 0 {
				w.notice = w.tr("Open Rocket on the target phone.")
			} else {
				w.choosePhone()
			}
		case "send":
			w.sendBusy = false
			if n.err != nil {
				w.queuedSend = false
				w.notice = fmt.Sprintf(w.tr("Could not queue files: %v"), n.err)
			} else {
				w.queuedSend = true
				w.notice = n.status
				if w.notice == "" {
					w.notice = w.tr("Queued. Waiting for phone to connect.")
				}
			}
		case "folder":
			w.folderBusy = false
			if n.err != nil {
				w.notice = w.tr("Could not change folder. Check access and try again.")
			} else {
				w.folder = n.folder
				if w.opts.Folder != nil {
					if current := w.opts.Folder(); current != "" {
						w.folder = current
					}
				}
				w.notice = w.tr("Folder updated for new transfers.")
			}
		}
	}
	invalidateRect.Call(w.hwnd, 0, 0)
}

func (w *window) onEvent(e Event) {
	// Several phones may transfer simultaneously. Show each flight in turn;
	// never combine another TCP session's bytes with the visible statistics.
	if e.SessionID != 0 && w.currentSession != 0 && e.SessionID != w.currentSession &&
		(w.state == transferring || w.state == departing) {
		if w.waiting == nil {
			w.waiting = make(map[uint64]Event)
		}
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
		w.currentSession = e.SessionID
		w.current = e
		w.state = transferring
		w.started = time.Now()
		w.displayProgress = 0
		w.notice = ""
		w.lastFrame = time.Time{}
	case "progress":
		if w.state != transferring {
			w.onEvent(Event{SessionID: e.SessionID, Kind: "start", Direction: e.Direction, Name: e.Name})
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
		if w.state != transferring {
			w.started = time.Time{}
			w.displayProgress = 0
			w.current = Event{}
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
			e.Duration = time.Since(w.started)
		}
		w.departY = w.rocketY()
		w.departure = time.Now()
		w.current = e
		w.state = departing
		if e.Direction == "outgoing" && w.queuedSend {
			w.paths = nil // Require a fresh source selection for the next launch.
			w.queuedSend = false
		}
		w.lastFrame = time.Time{}
	case "error":
		w.currentSession = e.SessionID
		w.state = failed
		w.current = e
		w.notice = w.transferError(e.Message)
	}
}

func (w *window) tick() {
	now := time.Now() // time.Sub uses Go's monotonic clock, not the wall clock.
	if w.state == transferring && w.current.Total > 0 {
		target := math.Min(1, math.Max(0, float64(w.current.Bytes)/float64(w.current.Total)))
		if !w.lastFrame.IsZero() {
			dt := math.Min(.25, now.Sub(w.lastFrame).Seconds())
			w.displayProgress += (target - w.displayProgress) * (1 - math.Exp(-6*dt))
		}
	}
	w.lastFrame = now
	if w.state == departing && time.Since(w.departure) >= 2*time.Second {
		if len(w.finishedQueue) > 0 {
			next := w.finishedQueue[0]
			w.finishedQueue = w.finishedQueue[1:]
			w.state = idle
			w.currentSession = 0
			w.onEvent(next)
			return
		}
		for id, next := range w.waiting {
			delete(w.waiting, id)
			w.state = idle
			w.currentSession = 0
			w.onEvent(Event{SessionID: id, Kind: "start", Direction: next.Direction, Name: next.Name, Total: next.Total})
			if next.Kind == "progress" {
				w.onEvent(next)
			}
			return
		}
		w.state = finished
		invalidateRect.Call(w.hwnd, 0, 0)
	}
}

func (w *window) rocketY() float64 {
	return float64(w.height)*.65 - w.displayProgress*float64(w.height)*.30
}

// Actions: files, destination, launch, open downloads, change downloads.
func (w *window) buttonRect(i int) rect {
	left, right := int32(w.width-319), int32(w.width-25)
	switch i {
	case 0:
		return rect{left, 180, right, 226}
	case 1:
		return rect{left, 292, right, 338}
	case 2:
		return rect{left, 376, right, 439}
	case 3:
		return rect{left, int32(w.height - 49), left + 143, int32(w.height - 15)}
	default:
		return rect{left + 150, int32(w.height - 49), right, int32(w.height - 15)}
	}
}
func (w *window) canSend() bool {
	return len(w.paths) > 0 && w.selectedPhone != "" && w.opts.Queue != nil && !w.sendBusy && !w.queuedSend
}
func (w *window) hit(x, y int) int {
	for i := 0; i < 5; i++ {
		r := w.buttonRect(i)
		if x >= int(r.left) && x < int(r.right) && y >= int(r.top) && y < int(r.bottom) {
			if i == 1 && (w.phonesBusy || w.opts.Phones == nil) ||
				i == 2 && !w.canSend() ||
				i == 4 && (w.folderBusy || w.opts.ChangeFolder == nil) {
				return -1
			}
			return i
		}
	}
	return -1
}

func (w *window) click(i int) {
	switch i {
	case 0:
		if paths := chooseFiles(w.hwnd, w.tr("Select files to send")); len(paths) > 0 {
			w.paths = paths
			w.queuedSend = false
			w.notice = ""
		}
	case 1:
		w.phonesBusy = true
		w.notice = w.tr("Looking for phones…")
		go func() { w.enqueue(notification{kind: "phones", phones: w.opts.Phones()}) }()
	case 2:
		w.sendBusy = true
		w.queuedSend = false
		// Clear an old result without starting a flight; only transfer events
		// drive animation and statistics for the new send.
		if w.state == finished || w.state == failed {
			w.state = idle
			w.currentSession = 0
			w.current = Event{}
			w.displayProgress = 0
		}
		w.notice = w.tr("Queueing files…")
		phoneID, paths := w.selectedPhone, append([]string(nil), w.paths...)
		go func() {
			status, err := w.opts.Queue(phoneID, paths)
			w.enqueue(notification{kind: "send", status: status, err: err})
		}()
	case 3:
		if w.folder == "" {
			w.notice = w.tr("No downloads folder selected.")
			break
		}
		verb, path := wide("open"), wide(w.folder)
		result, _, _ := shellExecute.Call(w.hwnd, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(path)), 0, 0, 1)
		runtime.KeepAlive(verb)
		runtime.KeepAlive(path)
		if result <= 32 {
			w.notice = w.tr("Could not open downloads folder.")
		}
	case 4:
		folder := chooseFolder(w.hwnd, w.tr("Select Rocket downloads folder"))
		if folder != "" {
			w.folderBusy = true
			w.notice = w.tr("Saving downloads folder…")
			go func() { w.enqueue(notification{kind: "folder", folder: folder, err: w.opts.ChangeFolder(folder)}) }()
		}
	}
	invalidateRect.Call(w.hwnd, 0, 0)
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

func (w *window) initFonts() {
	fontName := wide("Segoe UI")
	for i, spec := range []struct{ size, weight int32 }{{34, 700}, {21, 600}, {16, 400}, {13, 600}} {
		w.fonts[i], _, _ = createFont.Call(uintptr(int64(-spec.size)), 0, 0, 0, uintptr(spec.weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(fontName)))
	}
	runtime.KeepAlive(fontName)
}
