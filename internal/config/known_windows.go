package config

import (
	"fmt"
	"syscall"
	"unsafe"
)

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

var downloadsID = guid{0x374de290, 0x123f, 0x4565, [8]byte{0x91, 0x64, 0x39, 0xc4, 0x92, 0x5e, 0x46, 0x7b}}
var shell32 = syscall.NewLazyDLL("shell32.dll")
var ole32 = syscall.NewLazyDLL("ole32.dll")
var shGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
var coTaskMemFree = ole32.NewProc("CoTaskMemFree")
var moveFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func knownDownloads() (string, error) {
	var path *uint16
	hresult, _, _ := shGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&downloadsID)), 0, 0, uintptr(unsafe.Pointer(&path)))
	if int32(hresult) < 0 {
		return "", fmt.Errorf("SHGetKnownFolderPath(Downloads): HRESULT 0x%08x", uint32(hresult))
	}
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(path)))
	if path == nil {
		return "", fmt.Errorf("SHGetKnownFolderPath(Downloads): empty path")
	}
	for i, r := range unsafe.Slice(path, 32768) {
		if r == 0 {
			return syscall.UTF16ToString(unsafe.Slice(path, i)), nil
		}
	}
	return "", fmt.Errorf("SHGetKnownFolderPath(Downloads): path too long")
}

func replaceConfig(from, to string) error {
	src, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// The config is an application-owned file: replacing its previous version is intentional.
	r, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(src)), uintptr(unsafe.Pointer(dst)), 1)
	if r == 0 {
		return fmt.Errorf("replace config: %w", callErr)
	}
	return nil
}
