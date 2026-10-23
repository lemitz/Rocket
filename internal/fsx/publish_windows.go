package fsx

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func moveNoReplace(from, to string) error {
	src, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// Flags=0: no REPLACE_EXISTING and no COPY_ALLOWED (same-volume move only).
	r, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), 0)
	if r != 0 {
		return nil
	}
	if errors.Is(callErr, syscall.ERROR_ALREADY_EXISTS) || errors.Is(callErr, syscall.ERROR_FILE_EXISTS) {
		return os.ErrExist
	}
	return fmt.Errorf("publish file: %w", callErr)
}
