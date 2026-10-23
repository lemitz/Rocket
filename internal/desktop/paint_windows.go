//go:build windows

package desktop

import (
	"fmt"
	"math"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

type canvas struct {
	dc          uintptr
	w, h, stage int
	fonts       [4]uintptr
}

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

func (c canvas) fill(r rect, color uintptr) {
	brush, _, _ := getStockObject.Call(dcBrush)
	setDCBrushColor.Call(c.dc, color)
	fillRect.Call(c.dc, uintptr(unsafe.Pointer(&r)), brush)
}
func (c canvas) shape(r rect, radius int, color uintptr) {
	brush, _, _ := getStockObject.Call(dcBrush)
	pen, _, _ := getStockObject.Call(dcPen)
	ob, _, _ := selectObject.Call(c.dc, brush)
	op, _, _ := selectObject.Call(c.dc, pen)
	setDCBrushColor.Call(c.dc, color)
	setDCPenColor.Call(c.dc, color)
	roundRect.Call(c.dc, uintptr(r.left), uintptr(r.top), uintptr(r.right), uintptr(r.bottom), uintptr(radius), uintptr(radius))
	selectObject.Call(c.dc, ob)
	selectObject.Call(c.dc, op)
}
func (c canvas) oval(x, y, rx, ry int, color uintptr) {
	brush, _, _ := getStockObject.Call(dcBrush)
	pen, _, _ := getStockObject.Call(dcPen)
	ob, _, _ := selectObject.Call(c.dc, brush)
	op, _, _ := selectObject.Call(c.dc, pen)
	setDCBrushColor.Call(c.dc, color)
	setDCPenColor.Call(c.dc, color)
	ellipse.Call(c.dc, uintptr(x-rx), uintptr(y-ry), uintptr(x+rx), uintptr(y+ry))
	selectObject.Call(c.dc, ob)
	selectObject.Call(c.dc, op)
}
func (c canvas) poly(points []point, color uintptr) {
	if len(points) == 0 {
		return
	}
	brush, _, _ := getStockObject.Call(dcBrush)
	pen, _, _ := getStockObject.Call(dcPen)
	ob, _, _ := selectObject.Call(c.dc, brush)
	op, _, _ := selectObject.Call(c.dc, pen)
	setDCBrushColor.Call(c.dc, color)
	setDCPenColor.Call(c.dc, color)
	polygon.Call(c.dc, uintptr(unsafe.Pointer(&points[0])), uintptr(len(points)))
	selectObject.Call(c.dc, ob)
	selectObject.Call(c.dc, op)
}
func (c canvas) line(x1, y1, x2, y2 int, color uintptr) {
	pen, _, _ := getStockObject.Call(dcPen)
	op, _, _ := selectObject.Call(c.dc, pen)
	setDCPenColor.Call(c.dc, color)
	moveToEx.Call(c.dc, uintptr(x1), uintptr(y1), 0)
	lineTo.Call(c.dc, uintptr(x2), uintptr(y2))
	selectObject.Call(c.dc, op)
}
func (c canvas) text(s string, r rect, font int, color uintptr, flags uintptr) {
	if s == "" {
		return
	}
	if c.fonts[font] != 0 {
		old, _, _ := selectObject.Call(c.dc, c.fonts[font])
		defer selectObject.Call(c.dc, old)
	}
	setTextColor.Call(c.dc, color)
	setBkMode.Call(c.dc, 1) // TRANSPARENT
	p := syscall.StringToUTF16Ptr(s)
	drawText.Call(c.dc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&r)), 0x20|0x04|0x800|0x8000|flags) // SINGLELINE | VCENTER | NOPREFIX | END_ELLIPSIS
}
func box(x, y, width, height int) rect {
	return rect{int32(x), int32(y), int32(x + width), int32(y + height)}
}

var (
	white    = rgb(239, 247, 255)
	muted    = rgb(147, 180, 210)
	blue     = rgb(83, 187, 255)
	softBlue = rgb(18, 50, 81)
)

func (w *window) draw(c canvas) {
	// Deep ocean / space gradient, drawn as inexpensive GDI bands into one
	// bitmap to avoid flicker even while the rocket is moving.
	for y := 0; y < c.h; y += 5 {
		t := float64(y) / float64(c.h)
		c.fill(box(0, y, c.stage, min(5, c.h-y)), rgb(5+int(5*t), 22+int(16*t), 51+int(22*t)))
	}
	c.oval(c.stage/2, c.h+150, c.stage/2+190, 290, rgb(9, 41, 78))
	c.oval(c.stage/2, c.h+182, c.stage/2+100, 230, rgb(12, 53, 94))
	c.oval(c.stage/2, c.h+202, c.stage/2+20, 170, rgb(15, 63, 105))
	c.fill(box(c.stage-1, 0, 1, c.h), rgb(34, 67, 100))
	w.stars(c)

	c.shape(box(32, 29, 36, 36), 16, rgb(25, 96, 151))
	c.poly([]point{{49, 35}, {44, 51}, {48, 49}, {53, 41}}, white)
	c.oval(52, 40, 2, 2, blue)
	c.text("ROCKET", box(79, 29, 200, 36), 1, white, 0)
	c.text(w.tr("YOUR DEVICES, ONE ORBIT"), box(36, 75, c.stage-72, 23), 3, muted, 0)
	c.text(w.tr("FAST WI-FI TRANSFERS"), box(36, c.h-38, c.stage-72, 20), 3, rgb(121, 166, 203), 0)

	cx := c.stage / 2
	if w.state == finished || w.state == failed {
		w.resultCard(c)
	} else {
		for y := 130; y < c.h-105; y += 18 {
			c.shape(box(cx-1, y, 2, 7), 2, rgb(31, 79, 120))
		}
		y := int(w.rocketY())
		if w.state == departing {
			t := math.Min(1, time.Since(w.departure).Seconds()/1.35)
			y = int(w.departY + (-125-w.departY)*(t*t*(3-2*t)))
			w.smoke(c, cx, y, t)
		}
		if y > -100 {
			w.rocket(c, cx, y)
		}
		if w.state == departing && time.Since(w.departure) >= 1250*time.Millisecond {
			w.resultCard(c)
		} else if w.state == transferring {
			w.transferCard(c)
		} else if w.state == idle {
			c.text(w.tr("READY FOR LIFTOFF"), box(36, c.h-96, c.stage-72, 34), 1, white, 0)
			c.text(w.tr("Choose PC files and a phone to launch."), box(36, c.h-66, c.stage-72, 25), 2, muted, 0)
		}
	}
	w.sidePanel(c)
}

func (w *window) stars(c canvas) {
	for i := 0; i < 84; i++ {
		x := 14 + (i*173+29*i*i)%(c.stage-27)
		y := 106 + (i*97+11*i*i)%max(1, c.h-235)
		r := 1
		if i%13 == 0 {
			r = 2
		}
		color := rgb(69, 127, 177)
		if i%7 == 0 {
			color = rgb(150, 202, 235)
		}
		c.oval(x, y, r, r, color)
	}
	c.oval(c.stage-85, 137, 26, 26, rgb(15, 57, 99))
	c.oval(c.stage-85, 137, 18, 18, rgb(23, 78, 125))
	c.oval(c.stage-85, 137, 11, 11, rgb(38, 106, 157))
}

func (w *window) rocket(c canvas, x, y int) {
	// Flame, fins, capsule and viewport are all GDI vectors, not assets.
	flame := 18
	if w.state == transferring || w.state == departing {
		flame += int(7 * math.Sin(float64(time.Now().UnixNano())/1e8))
	}
	c.poly([]point{{int32(x - 10), int32(y + 43)}, {int32(x), int32(y + 53 + flame)}, {int32(x + 10), int32(y + 43)}}, rgb(255, 152, 79))
	c.poly([]point{{int32(x - 5), int32(y + 44)}, {int32(x), int32(y + 47 + flame/2)}, {int32(x + 5), int32(y + 44)}}, rgb(255, 229, 142))
	c.poly([]point{{int32(x - 18), int32(y + 20)}, {int32(x - 34), int32(y + 52)}, {int32(x - 14), int32(y + 44)}}, rgb(57, 166, 242))
	c.poly([]point{{int32(x + 18), int32(y + 20)}, {int32(x + 34), int32(y + 52)}, {int32(x + 14), int32(y + 44)}}, rgb(57, 166, 242))
	c.shape(box(x-18, y-22, 36, 69), 23, rgb(224, 240, 255))
	c.poly([]point{{int32(x - 18), int32(y - 11)}, {int32(x), int32(y - 44)}, {int32(x + 18), int32(y - 11)}}, rgb(94, 204, 255))
	c.shape(box(x-18, y+32, 36, 12), 8, rgb(86, 174, 236))
	c.oval(x, y+4, 12, 12, rgb(42, 119, 181))
	c.oval(x, y+4, 8, 8, rgb(120, 218, 255))
	c.oval(x-3, y+1, 2, 2, white)
}

func (w *window) smoke(c canvas, x, y int, t float64) {
	for i := 0; i < 22; i++ {
		v := float64(i) / 22
		spread := int((22 + 100*t) * math.Sin(float64(i)*2.4))
		px := x + spread
		py := y + 61 + int(v*115) + int(t*95)
		if py < 0 || py >= c.h-45 || px < 0 || px >= c.stage {
			continue
		}
		r := 3 + int(7*v+9*t)
		shade := 65 + int(45*(1-v))
		c.oval(px, py, r, r, rgb(shade/2, shade, shade+37))
	}
}

func (w *window) transferCard(c canvas) {
	x := 36
	y := c.h - 142
	width := c.stage - 72
	c.shape(box(x, y, width, 91), 19, rgb(15, 53, 91))
	label := w.tr("TRANSFERRING FILES")
	if w.current.Direction == "incoming" {
		label = w.tr("RECEIVING FROM PHONE")
	} else if w.current.Direction == "outgoing" {
		label = w.tr("SENDING TO PHONE")
	}
	c.text(label, box(x+17, y+10, width-34, 22), 3, blue, 0)
	name := w.current.Name
	if name == "" {
		name = w.tr("Files in transit")
	}
	c.text(name, box(x+17, y+33, width-34, 26), 1, white, 0)
	pct := w.tr("Connecting…")
	if w.current.Total > 0 {
		pct = fmt.Sprintf("%d%%", int(100*math.Min(1, math.Max(0, float64(w.current.Bytes)/float64(w.current.Total)))))
	}
	c.text(w.byteSize(w.current.Bytes)+"  /  "+w.byteSize(w.current.Total)+"    "+pct, box(x+17, y+62, width-34, 20), 3, muted, 0)
	if w.current.Total > 0 {
		c.shape(box(x+17, y+83, width-34, 3), 3, rgb(36, 84, 126))
		c.shape(box(x+17, y+83, int(float64(width-34)*math.Min(1, math.Max(0, w.displayProgress))), 3), 3, blue)
	}
}

func (w *window) resultCard(c canvas) {
	x := 36
	width := c.stage - 72
	y := max(135, c.h/2-108)
	c.shape(box(x, y, width, 223), 24, rgb(14, 55, 96))
	if w.state == failed {
		c.shape(box(x+19, y+20, 124, 27), 13, rgb(105, 62, 86))
		c.text(w.tr("INTERRUPTED"), box(x+28, y+21, 111, 24), 3, rgb(255, 198, 195), 0)
		c.text(w.tr("Flight interrupted"), box(x+20, y+56, width-40, 44), 0, white, 0)
		c.text(w.notice, box(x+20, y+110, width-40, 34), 2, muted, 0)
		c.text(w.tr("You can try again."), box(x+20, y+175, width-40, 27), 2, muted, 0)
		return
	}
	c.shape(box(x+19, y+19, 145, 27), 13, rgb(28, 101, 108))
	c.text(w.tr("DELIVERED"), box(x+29, y+20, 128, 25), 3, rgb(139, 245, 218), 0)
	c.text(w.tr("Flight complete"), box(x+20, y+53, width-40, 45), 0, white, 0)
	c.text(w.tr("Files arrived safely."), box(x+20, y+94, width-40, 25), 2, muted, 0)
	statsY := y + 141
	c.fill(box(x+20, statsY-8, width-40, 1), rgb(49, 97, 132))
	bytes := w.current.Bytes
	if bytes <= 0 && w.current.Total > 0 {
		bytes = w.current.Total
	}
	values := []string{fmt.Sprintf("%d", max(0, w.current.Files)), w.byteSize(bytes), w.duration(w.current.Duration), w.speed(bytes, w.current.Duration)}
	labels := []string{w.tr("FILES"), w.tr("SIZE"), w.tr("TIME"), w.tr("AVG. SPEED")}
	col := (width - 40) / 4
	for i := range labels {
		left := x + 20 + i*col
		c.text(values[i], box(left, statsY+2, col-4, 28), 1, white, 0)
		c.text(labels[i], box(left, statsY+32, col-4, 22), 3, muted, 0)
	}
}

func (w *window) sidePanel(c canvas) {
	x := c.stage
	width := c.w - x
	c.fill(box(x, 0, width, c.h), rgb(11, 36, 65))
	left, inner := x+25, width-50
	c.text(w.tr("THIS COMPUTER"), box(left, 20, inner, 23), 3, blue, 0)
	name := w.opts.Name
	if name == "" {
		name = w.tr("This PC")
	}
	c.text(name, box(left, 45, inner, 38), 0, white, 0)
	id := "ID  " + w.opts.ID
	if w.opts.ID == "" {
		id = w.tr("Available on the local network")
	}
	c.text(id, box(left, 84, inner, 20), 3, muted, 0)
	server := w.tr("SERVER RUNNING")
	if !w.serverOnline {
		server = w.tr("SERVER OFFLINE")
	}
	c.oval(left+6, 117, 5, 5, rgb(103, 220, 175))
	c.text(server, box(left+19, 106, inner-19, 23), 3, white, 0)

	c.fill(box(left, 137, inner, 1), rgb(37, 76, 110))
	c.text(w.tr("1  SELECT FILES ON THIS PC"), box(left, 148, inner, 23), 3, blue, 0)
	fileSummary := w.tr("No files selected")
	if len(w.paths) == 1 {
		fileSummary = filepath.Base(w.paths[0])
	} else if len(w.paths) > 1 {
		fileSummary = fmt.Sprintf(w.tr("%d files selected"), len(w.paths))
	}
	c.text(fileSummary, box(left, 231, inner, 25), 2, muted, 0)

	c.text(w.tr("2  CHOOSE TARGET PHONE"), box(left, 260, inner, 23), 3, blue, 0)
	hint := w.tr("Open Rocket on the target phone.")
	if w.selectedPhone != "" {
		hint = w.tr("Tap to refresh connected phones")
	}
	c.text(hint, box(left, 342, inner, 24), 3, muted, 0)

	statusY := max(450, c.h-195)
	c.shape(box(left, statusY, inner, 55), 14, softBlue)
	status := w.tr("Ready to send")
	if w.phonesBusy {
		status = w.tr("Looking for phones…")
	} else if w.sendBusy {
		status = w.tr("Queueing files…")
	} else if w.queuedSend {
		status = w.tr("Waiting for phone")
	}
	switch w.state {
	case transferring:
		status = w.tr("Transferring")
	case departing, finished:
		status = w.tr("Transfer complete")
	case failed:
		status = w.tr("Transfer interrupted")
	}
	if !w.serverOnline {
		status = w.tr("Server offline")
	}
	c.oval(left+16, statusY+18, 5, 5, blue)
	c.text(status, box(left+29, statusY+4, inner-39, 25), 2, white, 0)
	message := w.tr("Select files and a phone to start")
	if w.notice != "" {
		message = w.notice
	}
	c.text(message, box(left+13, statusY+28, inner-26, 23), 3, muted, 0)

	footer := c.h - 111
	c.fill(box(left, footer-7, inner, 1), rgb(37, 76, 110))
	c.text(w.tr("DOWNLOADS FOLDER"), box(left, footer, inner, 20), 3, blue, 0)
	folder := w.folder
	if folder == "" {
		folder = w.tr("No folder selected")
	}
	c.text(folder, box(left, footer+20, inner, 25), 2, white, 0)

	phoneName := w.tr("Select phone  ▾")
	for _, phone := range w.phones {
		if phone.ID == w.selectedPhone {
			phoneName = phone.Name
			if phoneName == "" {
				phoneName = phone.ID
			}
			phoneName += "  ▾"
			break
		}
	}
	labels := []string{w.tr("Choose files  ···"), phoneName, w.tr("Launch  →"), w.tr("Open  ↗"), w.tr("Change  ···")}
	if w.phonesBusy {
		labels[1] = w.tr("Looking for phones…")
	}
	if w.sendBusy {
		labels[2] = w.tr("Queueing files…")
	}
	if w.folderBusy {
		labels[4] = w.tr("Saving folder…")
	}
	for i, label := range labels {
		r := w.buttonRect(i)
		color := rgb(21, 61, 98)
		if i == 2 {
			color = rgb(33, 123, 193)
		}
		disabled := i == 1 && (w.phonesBusy || w.opts.Phones == nil) ||
			i == 2 && !w.canSend() ||
			i == 4 && (w.folderBusy || w.opts.ChangeFolder == nil)
		if disabled {
			color = rgb(22, 50, 78)
		} else if i == w.pressed {
			color = rgb(20, 85, 139)
		} else if i == w.hover {
			color = rgb(43, 137, 200)
		}
		c.shape(r, 14, color)
		font := 2
		if i == 2 {
			font = 1
		}
		textColor := white
		if disabled {
			textColor = muted
		}
		c.text(label, rect{r.left + 14, r.top, r.right - 8, r.bottom}, font, textColor, 0)
	}
}
