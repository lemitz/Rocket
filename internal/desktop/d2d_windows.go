//go:build windows

package desktop

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

// Vtable offsets and structures follow the Windows SDK d2d1.h interfaces
// ID2D1Factory, ID2D1RenderTarget, ID2D1HwndRenderTarget and ID2D1Bitmap.
// All resources belong to the locked UI thread. GDI never draws on the HWND.
var (
	d2d1             = syscall.NewLazyDLL("d2d1.dll")
	d2dCreateFactory = d2d1.NewProc("D2D1CreateFactory")
	createDIBSection = gdi32.NewProc("CreateDIBSection")
	peekMessage      = user32.NewProc("PeekMessageW")
	waitMessage      = user32.NewProc("MsgWaitForMultipleObjectsEx")
)

const d2dRecreateTarget = 0x8899000c

type d2dSize struct{ w, h uint32 }
type d2dRect struct{ left, top, right, bottom float32 }
type d2dTargetProperties struct {
	targetType, dxgiFormat, alphaMode uint32
	dpiX, dpiY                        float32
	usage, minLevel                   uint32
}
type d2dHwndProperties struct {
	hwnd           uintptr
	size           d2dSize
	presentOptions uint32 // D2D1_PRESENT_OPTIONS_NONE: wait for display refresh
}
type d2dBitmapProperties struct {
	format, alpha uint32
	dpiX, dpiY    float32
}
type dibHeader struct {
	size                   uint32
	width, height          int32
	planes, bitCount       uint16
	compression, imageSize uint32
	xppm, yppm             int32
	used, important        uint32
}
type dibInfo struct {
	header dibHeader
	colors [3]uint32
}

type presenter struct {
	factory, target, bitmap    uintptr
	dc, dib, oldBitmap, pixels uintptr
	width, height              int
	recreateCount              int
}

func nativePointer(address uintptr) uintptr {
	// COM objects and vtables live in unmanaged memory. Copy their pointer
	// values through Win32 rather than manufacturing Go pointers from uintptrs.
	var value uintptr
	moveMemory.Call(uintptr(unsafe.Pointer(&value)), address, unsafe.Sizeof(value))
	return value
}

func comMethod(obj uintptr, index uintptr) uintptr {
	return nativePointer(nativePointer(obj) + index*unsafe.Sizeof(uintptr(0)))
}
func comCall(obj uintptr, index uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(comMethod(obj, index), append([]uintptr{obj}, args...)...)
	return r
}
func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}
func hresult(op string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("Direct2D %s: HRESULT 0x%08x", op, uint32(hr))
	}
	return nil
}

func (p *presenter) init() error {
	// IID_ID2D1Factory, D2D1_FACTORY_TYPE_SINGLE_THREADED.
	iid := [16]byte{0x47, 0x22, 0x15, 0x06, 0x50, 0x6f, 0x5a, 0x46, 0x92, 0x45, 0x11, 0x8b, 0xfd, 0x3b, 0x60, 0x07}
	if err := d2dCreateFactory.Find(); err != nil {
		return fmt.Errorf("Direct2D is unavailable: %w", err)
	}
	hr, _, _ := d2dCreateFactory.Call(0, uintptr(unsafe.Pointer(&iid)), 0, uintptr(unsafe.Pointer(&p.factory)))
	runtime.KeepAlive(iid)
	return hresult("create factory", hr)
}

func (p *presenter) releaseSurface() {
	comRelease(p.bitmap)
	p.bitmap = 0
	if p.dc != 0 {
		selectObject.Call(p.dc, p.oldBitmap)
		deleteDC.Call(p.dc)
		p.dc, p.oldBitmap = 0, 0
	}
	if p.dib != 0 {
		deleteObject.Call(p.dib)
		p.dib = 0
	}
	p.pixels = 0
	p.width, p.height = 0, 0
}
func (p *presenter) close() {
	p.releaseSurface()
	comRelease(p.target)
	p.target = 0
	comRelease(p.factory)
	p.factory = 0
}
func (p *presenter) discardTarget() {
	p.releaseSurface()
	comRelease(p.target)
	p.target = 0
}

func (p *presenter) size(hwnd uintptr, width, height int) error {
	if width <= 0 || height <= 0 {
		return nil
	} // minimized
	if p.width == width && p.height == height && p.bitmap != 0 {
		return nil
	}
	p.releaseSurface()
	if p.target != 0 {
		size := d2dSize{uint32(width), uint32(height)}
		if err := hresult("resize render target", comCall(p.target, 56, uintptr(unsafe.Pointer(&size)))); err != nil {
			p.discardTarget() // Resize can leave the target in a zombie state.
		}
	}
	if p.target == 0 {
		props := d2dTargetProperties{dpiX: 96, dpiY: 96} // pixel-perfect DIP coordinates
		hwndProps := d2dHwndProperties{hwnd: hwnd, size: d2dSize{uint32(width), uint32(height)}}
		if err := hresult("create vsynced HWND render target", comCall(p.factory, 14,
			uintptr(unsafe.Pointer(&props)), uintptr(unsafe.Pointer(&hwndProps)), uintptr(unsafe.Pointer(&p.target)))); err != nil {
			return err
		}
	}
	// Top-down 32bpp BGRA DIB. The alpha channel is ignored by the D2D bitmap;
	// GDI writes RGB, and the entire canvas is repainted before every upload.
	info := dibInfo{header: dibHeader{size: uint32(unsafe.Sizeof(dibHeader{})), width: int32(width), height: -int32(height), planes: 1, bitCount: 32}}
	p.dib, _, _ = createDIBSection.Call(0, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&p.pixels)), 0, 0)
	if p.dib == 0 || p.pixels == 0 {
		p.releaseSurface()
		return fmt.Errorf("Direct2D staging DIB creation failed")
	}
	p.dc, _, _ = createCompatibleDC.Call(0)
	if p.dc == 0 {
		p.releaseSurface()
		return fmt.Errorf("Direct2D staging DC creation failed")
	}
	p.oldBitmap, _, _ = selectObject.Call(p.dc, p.dib)
	if p.oldBitmap == 0 || p.oldBitmap == ^uintptr(0) {
		p.oldBitmap = 0
		p.releaseSurface()
		return fmt.Errorf("Direct2D staging bitmap selection failed")
	}
	bitmapProps := d2dBitmapProperties{format: 87, alpha: 3, dpiX: 96, dpiY: 96} // DXGI_FORMAT_B8G8R8A8_UNORM, D2D1_ALPHA_MODE_IGNORE
	if err := hresult("create bitmap", comCall(p.target, 4, uintptr(uint64(uint32(width))|uint64(uint32(height))<<32),
		0, 0, uintptr(unsafe.Pointer(&bitmapProps)), uintptr(unsafe.Pointer(&p.bitmap)))); err != nil {
		p.releaseSurface()
		return err
	}
	p.width, p.height = width, height
	return nil
}

// draw presents through Direct2D EndDraw, not GDI BitBlt or a timer. An
// occluded window need not block at vsync; the event loop parks instead.
func (w *window) present() error {
	if w.width <= 0 || w.height <= 0 {
		return nil
	}
	p := &w.presenter
	if err := p.size(w.hwnd, w.width, w.height); err != nil {
		return err
	}
	w.draw(canvas{p.dc, p.width, p.height, p.width - 344, w.fonts})
	if err := hresult("upload bitmap", comCall(p.bitmap, 10, 0, p.pixels, uintptr(p.width*4))); err != nil {
		if uint32(comCall(p.target, 55))&1 != 0 {
			w.occluded = true
		}
		return err
	}
	comCall(p.target, 48) // BeginDraw
	// DrawBitmap(bitmap, destinationRect, opacity, NEAREST_NEIGHBOR, sourceRect).
	// Go's float arguments to SyscallN are raw IEEE-754 bits.
	dest := d2dRect{right: float32(p.width), bottom: float32(p.height)}
	comCall(p.target, 26, p.bitmap, uintptr(unsafe.Pointer(&dest)), uintptr(math.Float32bits(1)), 1, 0)
	hr := comCall(p.target, 49, 0, 0) // EndDraw presents at display refresh with PRESENT_OPTIONS_NONE.
	if uint32(hr) == d2dRecreateTarget {
		p.discardTarget()
		p.recreateCount++
		if p.recreateCount > 3 {
			return hresult("recreate render target (repeated device loss)", hr)
		}
		invalidateRect.Call(w.hwnd, 0, 0) // also retry if no animation is active
		return nil
	}
	if err := hresult("present", hr); err != nil {
		return err
	}
	p.recreateCount = 0
	w.occluded = uint32(comCall(p.target, 55))&1 != 0
	return nil
}
