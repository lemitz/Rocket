//go:build windows

package desktop

import (
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	comdlg32        = syscall.NewLazyDLL("comdlg32.dll")
	getOpenFileName = comdlg32.NewProc("GetOpenFileNameW")
	createPopupMenu = user32.NewProc("CreatePopupMenu")
	appendMenu      = user32.NewProc("AppendMenuW")
	trackPopupMenu  = user32.NewProc("TrackPopupMenuEx")
	destroyMenu     = user32.NewProc("DestroyMenu")
	clientToScreen  = user32.NewProc("ClientToScreen")
)

// OPENFILENAMEW, including the trailing Vista-era fields. Keep all Go-backed
// buffers alive until the synchronous common dialog returns.
type openFileName struct {
	size             uint32
	owner, instance  uintptr
	filter, custom   uintptr
	maxCustom, index uint32
	file             uintptr
	maxFile          uint32
	fileTitle        uintptr
	maxFileTitle     uint32
	initialDir       uintptr
	title            uintptr
	flags            uint32
	fileOffset       uint16
	fileExtension    uint16
	defaultExtension uintptr
	customData       uintptr
	hook, template   uintptr
	reserved         uintptr
	reservedDWord    uint32
	flagsEx          uint32
}

func chooseFiles(owner uintptr, caption string) []string {
	// Explorer multi-select returns either a full path followed by NUL NUL,
	// or a directory followed by each filename and a final NUL NUL.
	buf := make([]uint16, 65536)
	filter := []uint16{'A', 'l', 'l', ' ', 'f', 'i', 'l', 'e', 's', 0, '*', '.', '*', 0, 0}
	title := wide(caption)
	info := openFileName{
		size: uint32(unsafe.Sizeof(openFileName{})), owner: owner,
		filter: uintptr(unsafe.Pointer(&filter[0])), file: uintptr(unsafe.Pointer(&buf[0])),
		maxFile: uint32(len(buf)), title: uintptr(unsafe.Pointer(title)),
		flags: 0x80000 | 0x200 | 0x1000 | 0x800 | 0x8 | 0x4, // EXPLORER, ALLOWMULTISELECT, FILEMUSTEXIST, PATHMUSTEXIST, NOCHANGEDIR, HIDEREADONLY
	}
	ok, _, _ := getOpenFileName.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(buf)
	runtime.KeepAlive(filter)
	runtime.KeepAlive(title)
	if ok == 0 {
		return nil // cancellation leaves the previous selection untouched
	}
	return parseSelectedFiles(buf)
}

func parseSelectedFiles(buf []uint16) []string {
	var parts []string
	for start := 0; start < len(buf) && buf[start] != 0; {
		end := start
		for end < len(buf) && buf[end] != 0 {
			end++
		}
		if end == len(buf) { // malformed / unterminated output
			return nil
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:end]))
		start = end + 1
	}
	if len(parts) <= 1 {
		return parts
	}
	paths := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		paths = append(paths, filepath.Join(parts[0], name))
	}
	return paths
}

func (w *window) choosePhone() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		w.notice = w.tr("Could not show connected phones.")
		return
	}
	defer destroyMenu.Call(menu)
	for i, phone := range w.phones {
		name := phone.Name
		if name == "" {
			name = phone.ID
		}
		if name != phone.ID {
			name += "  ·  " + phone.ID
		}
		label := wide(name)
		flags := uintptr(0) // MF_STRING
		if phone.ID == w.selectedPhone {
			flags = 0x8 // MF_CHECKED
		}
		appendMenu.Call(menu, flags, uintptr(i+1), uintptr(unsafe.Pointer(label)))
		runtime.KeepAlive(label)
	}
	r := w.buttonRect(1)
	pt := point{r.left, r.bottom}
	clientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&pt)))
	// TPM_RETURNCMD; no WM_COMMAND handling or separate dropdown control.
	choice, _, _ := trackPopupMenu.Call(menu, 0x100, uintptr(pt.x), uintptr(pt.y), w.hwnd, 0)
	if choice > 0 && choice <= uintptr(len(w.phones)) {
		w.selectedPhone = w.phones[choice-1].ID
		w.queuedSend = false
		w.notice = ""
	}
}
