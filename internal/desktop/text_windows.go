//go:build windows

package desktop

import (
	"math"
	"syscall"
	"unsafe"
)

var (
	getTextExtent = gdi32.NewProc("GetTextExtentPoint32W")
	patBlt        = gdi32.NewProc("PatBlt")
)

const textPad = 2 // transparent margin around every mask (glyph overhang)

type fontKey struct{ px, weight int }
type maskKey struct {
	font fontKey
	text string
}

// textRenderer rasterizes strings with GDI (grayscale antialiasing, white on black) into a
// scratch DIB and keeps the coverage as a reusable mask. Colour, alpha and position are
// applied by the CPU renderer, so text can fade and sit on any background.
type textRenderer struct {
	face                 *uint16
	fonts                map[fontKey]uintptr
	masks                map[maskKey]*textMask
	dc, dib, old, pixels uintptr
	cw, ch               int
	scratch              []uint32
}

func newTextRenderer() *textRenderer {
	return &textRenderer{face: wide("Segoe UI"), fonts: map[fontKey]uintptr{}, masks: map[maskKey]*textMask{}}
}

func (t *textRenderer) close() {
	t.release()
	for _, f := range t.fonts {
		deleteObject.Call(f)
	}
	t.fonts = map[fontKey]uintptr{}
	t.masks = map[maskKey]*textMask{}
}

func (t *textRenderer) release() {
	if t.dc != 0 {
		selectObject.Call(t.dc, t.old)
		deleteDC.Call(t.dc)
		t.dc = 0
	}
	if t.dib != 0 {
		deleteObject.Call(t.dib)
		t.dib = 0
	}
	t.pixels, t.cw, t.ch = 0, 0, 0
}

func (t *textRenderer) font(k fontKey) uintptr {
	if f, ok := t.fonts[k]; ok {
		return f
	}
	// CLEARTYPE would colour-fringe; ANTIALIASED_QUALITY (4) is plain grayscale coverage.
	f, _, _ := createFont.Call(uintptr(int64(-k.px)), 0, 0, 0, uintptr(k.weight), 0, 0, 0, 1, 0, 0, 4, 0, uintptr(unsafe.Pointer(t.face)))
	t.fonts[k] = f
	return f
}

func (t *textRenderer) ensure(w, h int) bool {
	if t.dc != 0 && w <= t.cw && h <= t.ch {
		return true
	}
	t.release()
	t.cw, t.ch = max(w, 1024), max(h, 96)
	info := dibInfo{header: dibHeader{size: uint32(unsafe.Sizeof(dibHeader{})), width: int32(t.cw), height: -int32(t.ch), planes: 1, bitCount: 32}}
	t.dib, _, _ = createDIBSection.Call(0, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&t.pixels)), 0, 0)
	t.dc, _, _ = createCompatibleDC.Call(0)
	if t.dib == 0 || t.dc == 0 || t.pixels == 0 {
		t.release()
		return false
	}
	t.old, _, _ = selectObject.Call(t.dc, t.dib)
	t.scratch = make([]uint32, t.cw*t.ch)
	return true
}

// get returns the cached mask for text drawn at px pixels tall (em height) and weight.
func (t *textRenderer) get(s string, px, weight int) *textMask {
	if s == "" || px < 4 {
		return nil
	}
	key := maskKey{fontKey{px, weight}, s}
	if m, ok := t.masks[key]; ok {
		return m
	}
	if len(t.masks) > 700 {
		t.masks = map[maskKey]*textMask{}
	}
	m := t.render(s, key.font)
	t.masks[key] = m
	return m
}

func (t *textRenderer) render(s string, fk fontKey) *textMask {
	font := t.font(fk)
	if font == 0 || !t.ensure(1024, 96) {
		return nil
	}
	text := syscall.StringToUTF16(s)
	text = text[:len(text)-1] // drop the terminator
	if len(text) == 0 {
		return nil
	}
	old, _, _ := selectObject.Call(t.dc, font)
	defer selectObject.Call(t.dc, old)
	var size struct{ cx, cy int32 }
	getTextExtent.Call(t.dc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), uintptr(unsafe.Pointer(&size)))
	w, h := int(size.cx)+textPad*2+2, int(size.cy)+textPad*2
	if w > 4096 {
		w = 4096
	}
	if !t.ensure(w, h) {
		return nil
	}
	patBlt.Call(t.dc, 0, 0, uintptr(w), uintptr(h), 0x00000042) // BLACKNESS
	setTextColor.Call(t.dc, 0xffffff)
	setBkMode.Call(t.dc, 1)
	r := rect{textPad, textPad, int32(w - textPad), int32(h - textPad)}
	drawText.Call(t.dc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)), uintptr(unsafe.Pointer(&r)), 0x800|0x20|0x100) // NOPREFIX|SINGLELINE|NOCLIP
	moveMemory.Call(uintptr(unsafe.Pointer(&t.scratch[0])), t.pixels, uintptr(t.cw*h*4))
	m := &textMask{w: w, h: h, cov: make([]uint8, w*h)}
	for y := 0; y < h; y++ {
		row := t.scratch[y*t.cw : y*t.cw+w]
		for x, v := range row {
			// Gamma lift: coverage on dark backgrounds otherwise looks too thin.
			c := uint8(v >> 8)
			m.cov[y*w+x] = gammaLift[c]
		}
	}
	return m
}

var gammaLift = func() [256]uint8 {
	var table [256]uint8
	for i := range table {
		f := float64(i) / 255
		table[i] = uint8(255*math.Pow(f, 0.82) + .5)
	}
	return table
}()

// width is the advance of the unclipped text in pixels (without the mask padding).
func (t *textRenderer) width(s string, px, weight int) int {
	if m := t.get(s, px, weight); m != nil {
		return m.w - textPad*2 - 2
	}
	return 0
}

// fit shortens s with an ellipsis until it is no wider than max pixels.
func (t *textRenderer) fit(s string, max, px, weight int) string {
	if t.width(s, px, weight) <= max {
		return s
	}
	runes := []rune(s)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		cand := string(runes) + "…"
		if t.width(cand, px, weight) <= max {
			return cand
		}
	}
	return "…"
}

// wrap breaks s on spaces into at most maxLines lines no wider than max pixels.
func (t *textRenderer) wrap(s string, max, px, weight, maxLines int) []string {
	var lines []string
	line := ""
	for _, word := range splitWords(s) {
		cand := word
		if line != "" {
			cand = line + " " + word
		}
		if t.width(cand, px, weight) <= max || line == "" {
			line = cand
			continue
		}
		lines = append(lines, line)
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = t.fit(lines[maxLines-1]+"…", max, px, weight)
	}
	for i, l := range lines {
		lines[i] = t.fit(l, max, px, weight)
	}
	return lines
}

func splitWords(s string) []string {
	var words []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			if start >= 0 {
				words = append(words, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		words = append(words, s[start:])
	}
	return words
}
