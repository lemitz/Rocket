//go:build windows

package desktop

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

var (
	openClipboard     = user32.NewProc("OpenClipboard")
	closeClipboard    = user32.NewProc("CloseClipboard")
	emptyClipboard    = user32.NewProc("EmptyClipboard")
	getClipboardData  = user32.NewProc("GetClipboardData")
	setClipboardData  = user32.NewProc("SetClipboardData")
	isClipboardFormat = user32.NewProc("IsClipboardFormatAvailable")
	globalAlloc       = kernel32.NewProc("GlobalAlloc")
	globalFree        = kernel32.NewProc("GlobalFree")
	globalLock        = kernel32.NewProc("GlobalLock")
	globalUnlock      = kernel32.NewProc("GlobalUnlock")
	globalSize        = kernel32.NewProc("GlobalSize")
)

const cfUnicodeText = 13

func withClipboard(hwnd uintptr, action func() error) error {
	// Clipboard ownership is system-wide. Another process may briefly hold it.
	for attempt := 0; attempt < 5; attempt++ {
		ok, _, _ := openClipboard.Call(hwnd)
		if ok != 0 {
			defer closeClipboard.Call()
			return action()
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("clipboard is busy")
}

// readClipboardText is called only on explicit Ctrl+V in the focused Rocket window.
func readClipboardText(hwnd uintptr) (string, error) {
	var text string
	err := withClipboard(hwnd, func() error {
		available, _, _ := isClipboardFormat.Call(cfUnicodeText)
		if available == 0 {
			return errors.New("clipboard does not contain text")
		}
		handle, _, _ := getClipboardData.Call(cfUnicodeText)
		if handle == 0 {
			return errors.New("cannot read clipboard")
		}
		size, _, _ := globalSize.Call(handle)
		// Do not materialize huge clips just to discover that they exceed the wire cap.
		if size < 2 || size > 128*1024 || size%2 != 0 {
			return errors.New("clipboard text is too large")
		}
		ptr, _, _ := globalLock.Call(handle)
		if ptr == 0 {
			return errors.New("cannot lock clipboard")
		}
		defer globalUnlock.Call(handle)
		buf := make([]uint16, size/2)
		moveMemory.Call(uintptr(unsafe.Pointer(&buf[0])), ptr, size)
		text = syscall.UTF16ToString(buf)
		return nil
	})
	return text, err
}

// writeClipboardText runs on the Rocket UI thread. SetClipboardData takes ownership
// of the movable global allocation only on success; otherwise we must release it.
func writeClipboardText(hwnd uintptr, text string) error {
	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	return withClipboard(hwnd, func() error {
		size := uintptr(len(utf16) * 2)
		handle, _, _ := globalAlloc.Call(2, size) // GMEM_MOVEABLE
		if handle == 0 {
			return errors.New("cannot allocate clipboard data")
		}
		owned := true
		defer func() {
			if owned {
				globalFree.Call(handle)
			}
		}()
		ptr, _, _ := globalLock.Call(handle)
		if ptr == 0 {
			return errors.New("cannot lock clipboard data")
		}
		moveMemory.Call(ptr, uintptr(unsafe.Pointer(&utf16[0])), size)
		globalUnlock.Call(handle)
		if ok, _, _ := emptyClipboard.Call(); ok == 0 {
			return errors.New("cannot clear clipboard")
		}
		if ok, _, _ := setClipboardData.Call(cfUnicodeText, handle); ok == 0 {
			return errors.New("cannot set clipboard text")
		}
		owned = false
		return nil
	})
}
