//go:build windows && amd64

package desktop

import (
	"testing"
	"unsafe"
)

func TestWin32StructureLayouts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"WNDCLASSW", unsafe.Sizeof(wndClass{}), 72},
		{"MSG", unsafe.Sizeof(msg{}), 48},
		{"PAINTSTRUCT", unsafe.Sizeof(paintStruct{}), 72},
		{"BROWSEINFOW", unsafe.Sizeof(browseInfo{}), 64},
		{"BROWSEINFOW.lpfn", unsafe.Offsetof(browseInfo{}.callback), 40},
		{"OPENFILENAMEW", unsafe.Sizeof(openFileName{}), 152},
		{"OPENFILENAMEW.lpstrFile", unsafe.Offsetof(openFileName{}.file), 48},
		{"OPENFILENAMEW.Flags", unsafe.Offsetof(openFileName{}.flags), 96},
		{"D2D1_RENDER_TARGET_PROPERTIES", unsafe.Sizeof(d2dTargetProperties{}), 28},
		{"D2D1_HWND_RENDER_TARGET_PROPERTIES", unsafe.Sizeof(d2dHwndProperties{}), 24},
		{"D2D1_BITMAP_PROPERTIES", unsafe.Sizeof(d2dBitmapProperties{}), 16},
		{"BITMAPINFOHEADER", unsafe.Sizeof(dibHeader{}), 40},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %d bytes, want %d", tc.name, tc.got, tc.want)
		}
	}
}
