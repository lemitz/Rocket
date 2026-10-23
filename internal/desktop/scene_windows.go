//go:build windows

package desktop

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"path/filepath"
	"time"
)

//go:embed assets/rocket.png
var rocketPNG []byte

// The sprite is the user's icon artwork cut out of its disc (tools/genassets). These are
// pixel coordinates inside assets/rocket.png: the exhaust tip, and the unit vector along
// the nose (the rocket is drawn pointing up and to the right).
const (
	spriteW, spriteH   = 587, 773
	flameTipX          = 25.0 // inside the flame teardrop, a little before its very tip
	flameTipY          = 745.0
	axisX, axisY       = 0.597, -0.802
	rocketHeightFactor = 0.40 // sprite height relative to the window height
)

var (
	cBg0    = rgbCol(0x10111A)
	cBg1    = rgbCol(0x1A1B2B)
	cBg2    = rgbCol(0x2A2548)
	cWhite  = rgbCol(0xF5F6FF)
	cMuted  = rgbCol(0xAEB0CE)
	cDim    = rgbCol(0x7E80A2)
	cPurple = rgbCol(0xA487EC)
	cBlue   = rgbCol(0x5B8DEF)
	cGood   = rgbCol(0x7CE3B8)
	cBad    = rgbCol(0xFF8D98)
	cWarn   = rgbCol(0xFFC56B)
	cPanel  = rgbCol(0x1C1D30)
)

type phase uint8

const (
	idle      phase = iota
	launching       // slider released, waiting for the first byte
	flying          // a transfer session is running
	departing       // finished: rocket leaves, smoke gathers, card appears
	resultOK
	resultFail
)

type rectF struct{ x, y, w, h float32 }

func (r rectF) contains(x, y float32) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }
func (r rectF) inset(d float32) rectF      { return rectF{r.x + d, r.y + d, r.w - 2*d, r.h - 2*d} }

type particle struct{ x, y, vx, vy, age, life, r0, r1, tone float32 }

type slider struct {
	pos       float64
	drag      bool
	grab      float32
	springing bool
	from      float64
	t0        time.Time
}

const (
	hNone = -1 + iota
	hGear
	hChoose
	hSummary
	hClear
	hTarget
	hThumb
	hOK
	hOpenOut
	hBack
	hFolder
	hFolderOpen
	hFirewall
	hRecheck
	hSheetOut
	hPhoneBase = 100
)

func ease(t float64) float64      { t = clamp(t, 0, 1); return 1 - (1-t)*(1-t)*(1-t) }
func easeInOut(t float64) float64 { t = clamp(t, 0, 1); return t * t * (3 - 2*t) }
func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
func f32(v float64) float32 { return float32(v) }

func loadRocket() *image.NRGBA {
	img, err := png.Decode(bytes.NewReader(rocketPNG))
	if err != nil {
		return image.NewNRGBA(image.Rect(0, 0, 1, 1))
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// ---- layout ----------------------------------------------------------------

type layout struct {
	u, w, h, cx                          float32
	gear                                 rectF
	choose, summary, clear, target       rectF
	track, thumb                         rectF
	minX, travel                         float32
	card, ok, openOut                    rectF
	rocketCx, rocketCy, rocketW, rocketH float32
	sheet                                rectF
	rows                                 []rectF
}

func (w *window) layout() layout {
	fw, fh := float32(w.width), float32(w.height)
	u := float32(math.Min(float64(fw)/420, float64(fh)/760))
	if u < .6 {
		u = .6
	}
	l := layout{u: u, w: fw, h: fh, cx: fw / 2}
	base := fh - 48*u
	colW := min(fw-48*u, 332*u)
	l.gear = rectF{fw - 64*u, 14 * u, 48 * u, 48 * u}
	l.choose = rectF{l.cx - min(colW, 300*u)/2, base - 58*u, min(colW, 300*u), 58 * u}
	l.track = rectF{l.cx - colW/2, base - 62*u, colW, 62 * u}
	thumb := 54 * u
	l.minX = l.track.x + 4*u
	l.travel = l.track.w - 8*u - thumb
	l.thumb = rectF{l.minX + float32(w.sl.pos)*l.travel, l.track.y + 4*u, thumb, thumb}
	l.target = rectF{l.cx - colW/2, l.track.y - 12*u - 32*u, colW, 32 * u}
	l.summary = rectF{l.cx - colW/2, l.target.y - 10*u - 46*u, colW, 46 * u}
	l.clear = rectF{l.summary.x + l.summary.w - 42*u, l.summary.y + 3*u, 40 * u, 40 * u}
	rh := float32(math.Min(float64(fh)*rocketHeightFactor, float64(fw)*.80/(spriteW/float64(spriteH))))
	l.rocketH = rh
	l.rocketW = rh * spriteW / spriteH
	l.rocketCx, l.rocketCy = l.cx, fh*.40
	cw := min(fw-40*u, 372*u)
	ch := 418 * u
	l.card = rectF{l.cx - cw/2, max(24*u, fh*.47-ch/2), cw, ch}
	l.ok = rectF{l.card.x + 22*u, l.card.y + l.card.h - 76*u, l.card.w - 44*u, 54 * u}
	l.openOut = l.ok
	if w.state == resultOK && w.current.Direction == "incoming" {
		half := (l.ok.w - 10*u) / 2
		l.openOut = rectF{l.ok.x, l.ok.y, half, l.ok.h}
		l.ok = rectF{l.ok.x + half + 10*u, l.ok.y, half, l.ok.h}
	}
	if w.sheetOpen {
		n := min(len(w.phones), 5)
		sh := float32(n)*48*u + 16*u
		l.sheet = rectF{l.target.x, l.target.y - 8*u - sh, l.target.w, sh}
		for i := 0; i < n; i++ {
			l.rows = append(l.rows, rectF{l.sheet.x + 8*u, l.sheet.y + 8*u + float32(i)*48*u, l.sheet.w - 16*u, 48 * u})
		}
	}
	return l
}

func (w *window) canLaunch() bool {
	return len(w.paths) > 0 && w.selectedPhone != "" && w.opts.Queue != nil && w.state == idle
}

func (w *window) selTarget() float64 {
	if len(w.paths) > 0 {
		return 1
	}
	return 0
}

// uiAlpha is how visible the idle controls are (they vanish when the rocket launches).
func (w *window) uiAlpha(now time.Time) float32 {
	switch w.state {
	case idle:
		if w.returnAt.IsZero() {
			return 1
		}
		return f32(ease(now.Sub(w.returnAt).Seconds()/.5 - .4))
	case launching, flying:
		return f32(1 - ease(now.Sub(w.flightStart).Seconds()/.3))
	}
	return 0
}

// ---- simulation ------------------------------------------------------------

func (w *window) tick(now time.Time) {
	dt := 1 / 60.0
	if !w.lastTick.IsZero() {
		dt = math.Min(.05, now.Sub(w.lastTick).Seconds())
	}
	w.lastTick = now

	// slider spring-back
	if w.sl.springing {
		t := now.Sub(w.sl.t0).Seconds() / .32
		w.sl.pos = w.sl.from * (1 - ease(t))
		if t >= 1 {
			w.sl.pos, w.sl.springing = 0, false
		}
	}
	// cross-fades
	w.selA = approach(w.selA, w.selTarget(), dt*5)
	setT := 0.0
	if w.screen == scSettings {
		setT = 1
	}
	w.setA = approach(w.setA, setT, dt*6)

	if w.state == launching && w.queued && now.After(w.launchDeadline) {
		w.fail(w.tr("The phone did not respond. Open Rocket on it and try again."))
		if w.opts.Unqueue != nil {
			w.opts.Unqueue(w.selectedPhone)
		}
	}
	if w.state == flying && w.current.Total > 0 {
		target := clamp(float64(w.current.Bytes)/float64(w.current.Total), 0, 1)
		w.displayProgress += (target - w.displayProgress) * (1 - math.Exp(-6*dt))
	}
	if w.state == departing && now.Sub(w.departAt) >= 2400*time.Millisecond {
		w.state = resultOK
		w.sheetOpen = false
	}

	// thrust and streak speed
	thrustT, speedT := 0.0, 0.0
	switch w.state {
	case launching, flying:
		L := now.Sub(w.flightStart).Seconds()
		thrustT = 1
		speedT = clamp((L-.25)/1.1, 0, 1)
	case departing:
		D := now.Sub(w.departAt).Seconds()
		if D < .95 {
			thrustT = 1.7
			speedT = 2.6
		} else {
			speedT = 0
		}
	}
	w.thrust = approach(w.thrust, thrustT, dt*3.5)
	w.streak = approach(w.streak, speedT, dt*2.2)
	w.scroll += w.streak * dt

	// exhaust particles
	if w.thrust > .12 && w.width > 0 {
		l := w.layout()
		fx, fy := w.flameTip(l, now)
		w.spawnAcc += dt * 170 * w.thrust
		for w.spawnAcc >= 1 {
			w.spawnAcc--
			w.spawn(fx, fy, l.u, float32(w.thrust))
		}
	}
	alive := w.parts[:0]
	for _, p := range w.parts {
		p.age += f32(dt)
		if p.age < p.life {
			p.x += p.vx * f32(dt)
			p.y += p.vy * f32(dt)
			alive = append(alive, p)
		}
	}
	w.parts = alive
}

func approach(v, target, step float64) float64 {
	if v < target {
		return math.Min(target, v+step)
	}
	return math.Max(target, v-step)
}

func (w *window) rnd() float32 {
	w.seed ^= w.seed << 13
	w.seed ^= w.seed >> 17
	w.seed ^= w.seed << 5
	return float32(w.seed&0xffff) / 65535
}

func (w *window) spawn(x, y, u, thrust float32) {
	if len(w.parts) > 260 {
		return
	}
	speed := (190 + w.rnd()*190) * u * (.7 + .3*thrust)
	spread := (w.rnd() - .5) * 90 * u
	// Exhaust leaves opposite to the nose; spread acts along the perpendicular (axisY, -axisX).
	vx := -axisX*speed - axisY*spread
	vy := -axisY*speed + axisX*spread
	w.parts = append(w.parts, particle{x: x + (w.rnd()-.5)*8*u, y: y + (w.rnd()-.5)*8*u, vx: vx, vy: vy,
		life: .35 + w.rnd()*.5, r0: (3 + w.rnd()*3) * u, r1: (10 + w.rnd()*10) * u, tone: w.rnd()})
}

// rocketOffsets returns the rocket centre, opacity and the sprite-axis travel in pixels.
func (w *window) rocketState(l layout, now time.Time) (x, y float32, alpha float32, flame float32) {
	d, dy, shx, shy := 0.0, 0.0, 0.0, 0.0
	alpha = 1
	liftMax := float64(l.h) * .085
	hover := func(L float64) float64 { return easeInOut((L-.15)/1.2) * liftMax }
	switch w.state {
	case idle:
		if !w.returnAt.IsZero() {
			r := ease(now.Sub(w.returnAt).Seconds() / .6)
			alpha = f32(r)
			dy = (1 - r) * float64(36*l.u)
		}
	case launching, flying:
		L := now.Sub(w.flightStart).Seconds()
		d = hover(L)
		amp := float64(l.u) * (2.2*math.Min(1, L/.6) - 1.4*easeInOut((L-1.2)/1))
		shx, shy = math.Sin(L*71)*amp, math.Cos(L*93)*amp
		dy = math.Sin(L*2.6) * 3 * float64(l.u) * easeInOut(L-1.2)
		flame = 1
	case departing:
		D := now.Sub(w.departAt).Seconds()
		hoverAt := hover(w.departAt.Sub(w.flightStart).Seconds())
		exit := math.Hypot(float64(l.w), float64(l.h)) * 1.15
		d = hoverAt + exit*math.Pow(clamp(D/.95, 0, 1), 2.3)
		flame = 1
		if D > .95 {
			alpha = 0
		}
	case resultFail:
		F := now.Sub(w.failAt).Seconds()
		hoverAt := hover(w.failAt.Sub(w.flightStart).Seconds())
		d = hoverAt * (1 - easeInOut(F/.7))
		flame = f32(1 - ease(F/.3))
		alpha = f32(1 - ease((F-.05)/.35)) // fades out as the card appears; it returns on dismiss
	case resultOK:
		alpha = 0
	}
	x = l.rocketCx + float32(axisX*d+shx)
	y = l.rocketCy + float32(axisY*d+dy+shy)
	return
}

func (w *window) rocketBitmap(l layout) *bitmap {
	hpx := int(l.rocketH + .5)
	if w.spriteBmp == nil || w.spriteH != hpx {
		w.spriteBmp = newBitmap(w.sprite, max(1, int(float32(hpx)*spriteW/spriteH+.5)), hpx)
		w.spriteH = hpx
	}
	return w.spriteBmp
}

func (w *window) flameTip(l layout, now time.Time) (float32, float32) {
	x, y, _, _ := w.rocketState(l, now)
	bmp := w.rocketBitmap(l)
	sc := float32(bmp.h) / spriteH
	return x - float32(bmp.w)/2 + flameTipX*sc, y - float32(bmp.h)/2 + flameTipY*sc
}

// ---- drawing ---------------------------------------------------------------

func (w *window) ensureBackground() {
	if w.bg != nil && w.bg.w == w.width && w.bg.h == w.height {
		return
	}
	s := newSurface(w.width, w.height)
	fw, fh := float32(w.width), float32(w.height)
	// near-black at the top, drifting towards violet at the bottom (one continuous ramp)
	for y := 0; y < w.height; y++ {
		t := float32(y) / fh
		c := mix(cBg0, cBg2, float32(math.Pow(float64(t), 1.7))) // one smooth ramp: no visible kink
		v := 0xff000000 | uint32(c.r+.5)<<16 | uint32(c.g+.5)<<8 | uint32(c.b+.5)
		row := s.pix[y*w.width : (y+1)*w.width]
		for x := range row {
			row[x] = v
		}
	}
	l := w.layout()
	s.glow(l.rocketCx+20*l.u, l.rocketCy, max(fw, fh)*.55, rgbCol(0x6C55D8), 0, .26)
	s.glow(fw*.15, fh*.78, fw*.9, rgbCol(0x3E6AE0), 0, .10)
	s.glow(fw*.9, fh*.08, fw*.5, rgbCol(0x8C6BE0), 0, .10)
	var seed uint32 = 7
	rnd := func() float32 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return float32(seed&0xffff) / 65535
	}
	for i := 0; i < 70+int(fw*fh/18000); i++ {
		x, y := rnd()*fw, rnd()*fh*.92
		r := (.5 + rnd()*rnd()*1.3) * l.u
		a := .18 + rnd()*.6
		c := cWhite
		if rnd() < .3 {
			c = cPurple
		}
		s.circle(x, y, r, solid(c), a)
	}
	// Dither: a dark gradient in 8 bits would otherwise show visible bands.
	var noise uint32 = 0x9e3779b9
	for i, p := range s.pix {
		noise ^= noise << 13
		noise ^= noise >> 17
		noise ^= noise << 5
		d := int(noise&3) - 1 // -1..2, mean +0.5 keeps the average colour
		ch := func(v uint32) uint32 { return uint32(max(0, min(255, int(v)+d))) }
		s.pix[i] = 0xff000000 | ch(p>>16&255)<<16 | ch(p>>8&255)<<8 | ch(p&255)
	}
	w.bg = s
}

func (w *window) draw(now time.Time) {
	w.ensureBackground()
	if w.fb == nil || w.fb.w != w.width || w.fb.h != w.height {
		w.fb = newSurface(w.width, w.height)
	}
	s := w.fb
	s.copyFrom(w.bg)
	l := w.layout()
	u := l.u
	uiA := w.uiAlpha(now)

	w.drawStreaks(s, l)
	// particles behind the rocket
	for _, p := range w.parts {
		t := p.age / p.life
		r := p.r0 + (p.r1-p.r0)*t
		c := mix(rgbCol(0xFFFFFF), mix(cPurple, cBlue, p.tone), clamp01(t*1.6))
		s.glow(p.x, p.y, r, c, 0, (1-t)*(1-t)*.5)
	}
	rx, ry, ra, flame := w.rocketState(l, now)
	if ra > 0 {
		bmp := w.rocketBitmap(l)
		sc := float32(bmp.h) / spriteH
		tipX := rx - float32(bmp.w)/2 + flameTipX*sc
		tipY := ry - float32(bmp.h)/2 + flameTipY*sc
		if flame > 0 {
			f := float32(.9 + .1*math.Sin(float64(now.UnixNano())/1.2e7))
			s.glow(tipX, tipY, 120*u*f*(.6+.5*f32(w.thrust)), rgbCol(0x9A7BFF), 0, .55*flame*ra)
		}
		s.draw(bmp, int(rx-float32(bmp.w)/2+.5), int(ry-float32(bmp.h)/2+.5), ra)
		if flame > 0 {
			s.glow(tipX+4*u, tipY-5*u, 46*u, rgbCol(0xFFFFFF), 0, .5*flame*ra)
		}
	}

	if w.state == departing || w.state == resultOK {
		w.drawSmoke(s, l, now, false)
	}
	switch w.state {
	case idle:
		w.drawIdleUI(s, l, now, uiA)
	case launching, flying:
		w.drawFlightCaption(s, l, now)
	case departing:
		D := now.Sub(w.departAt).Seconds()
		w.drawCard(s, l, f32(ease((D-.9)/.55)), f32(.94+.06*ease((D-.9)/.55)), true)
		w.drawSmoke(s, l, now, true)
	case resultOK:
		w.drawCard(s, l, 1, 1, true)
	case resultFail:
		F := now.Sub(w.failAt).Seconds()
		w.drawCard(s, l, f32(ease((F-.25)/.4)), f32(.95+.05*ease((F-.25)/.4)), false)
	}

	if w.toast != "" && now.Before(w.toastUntil) {
		a := f32(clamp(w.toastUntil.Sub(now).Seconds()/.4, 0, 1))
		w.txt(s, w.toast, l.cx, l.h*.66, 14, 500, cWarn, a, 1, l)
	}
	if w.setA > .004 {
		w.drawSettings(s, l, now)
	}
	if w.sheetOpen && w.state == idle {
		w.drawSheet(s, l)
	}
	_ = u
}

func (w *window) drawStreaks(s *surface, l layout) {
	if w.streak < .01 {
		return
	}
	diag := float32(math.Hypot(float64(l.w), float64(l.h)))
	track := diag * 1.15
	dx, dy := f32(-axisX), f32(-axisY) // streaks move opposite to the nose: down and to the left
	px, py := -dy, dx                  // perpendicular
	for i := 0; i < 38; i++ {
		seed := float32(i)*.6180339 + .13
		lane := frac(seed * 7.31)
		phase := frac(seed * 3.77)
		mult := .55 + frac(seed*5.19)*.9
		alpha := (.10 + frac(seed*2.13)*.30) * f32(math.Min(1, w.streak))
		length := (26 + frac(seed*9.7)*90) * l.u * f32(.35+math.Min(w.streak, 1.5))
		pos := frac(phase + f32(w.scroll)*mult*.55*l.u/track*60)
		hx := l.w/2 + px*(lane-.5)*diag + dx*(pos-.5)*track
		hy := l.h/2 + py*(lane-.5)*diag + dy*(pos-.5)*track
		if hx < -80 || hy < -80 || hx > l.w+80 || hy > l.h+80 {
			continue
		}
		c := cWhite
		if i%3 == 0 {
			c = cPurple
		}
		width := (.9 + frac(seed*4.4)*1.5) * l.u
		s.capsule(hx, hy, hx-dx*length, hy-dy*length, width, solid(c), alpha)
	}
}

func frac(v float32) float32 { return v - float32(math.Floor(float64(v))) }

type smokePuff struct{ x, y, r, a float32 }

func (w *window) drawSmoke(s *surface, l layout, now time.Time, front bool) {
	D := now.Sub(w.departAt).Seconds()
	if w.state != departing {
		D = 10 // the cloud stays behind the card until it is dismissed
	}
	if D < .5 {
		return
	}
	cardCx, cardCy := l.card.x+l.card.w/2, l.card.y+l.card.h/2
	if front {
		// A bank of smoke rises over the card's lower half, then thins out and uncovers it.
		for i := 0; i < 8; i++ {
			seed := float32(i)*.754877 + .2
			fx := clampF(cardCx+(float32(i)-3.5)/3.5*l.card.w*.52, l.w*.06, l.w*.94)
			fy := l.card.y + l.card.h*(.78+.13*float32(math.Sin(float64(i)*2.1)))
			r := (52 + frac(seed*8.1)*62) * l.u
			p := ease((D - .45 - .03*float64(i)) / .7)
			a := .42 * f32(p) * f32(1-ease((D-1.0)/.9))
			c := mix(rgbCol(0xD9D1FA), rgbCol(0x9A92DE), frac(seed*3.3))
			s.glow(fx, fy, r, c, .25, a)
		}
		return
	}
	for i := 0; i < 18; i++ {
		seed := float32(i)*.754877 + .2
		angle := float64(frac(seed*5.3)) * 2 * math.Pi
		dist := .8 + .5*frac(seed*2.9)
		fx := clampF(cardCx+float32(math.Cos(angle))*l.card.w*.46*dist, l.w*.06, l.w*.94)
		fy := cardCy + float32(math.Sin(angle))*l.card.h*.50*dist + l.card.h*.06
		r := (46 + frac(seed*8.1)*56) * l.u
		p := ease((D - .5 - .025*float64(i)) / 1.2)
		a := (.12 + frac(seed*6.7)*.10) * f32(p)
		c := mix(rgbCol(0xD9D1FA), rgbCol(0x8E86D6), frac(seed*3.3))
		// Puffs squeezed against the window edge would pile up into a bright bar: fade them out.
		edge := clamp01(min(fx, l.w-fx) / (r * .8))
		s.glow(fx, fy, r*(.6+.4*f32(p)), c, .2, a*edge)
	}
}

func (w *window) drawFlightCaption(s *surface, l layout, now time.Time) {
	L := now.Sub(w.flightStart).Seconds()
	a := f32(ease((L - 1.0) / .6))
	if a <= 0 {
		return
	}
	text := w.tr("Waiting for the phone…")
	if w.state == flying {
		verb := w.tr("Sending…")
		if w.current.Direction == "incoming" {
			verb = w.tr("Receiving…")
		}
		text = verb
		if w.current.Total > 0 {
			text = fmt.Sprintf("%s %d%%", verb, int(100*clamp(float64(w.current.Bytes)/float64(w.current.Total), 0, 1)))
		}
	}
	w.txt(s, text, l.cx, l.h-54*l.u, 15, 500, cMuted, a*.9, 1, l)
}

func (w *window) txt(s *surface, text string, x, y, size float32, weight int, c col, op float32, align int, l layout) {
	px := int(size*l.u + .5)
	m := w.ts.get(text, px, weight)
	if m == nil {
		return
	}
	tw := float32(m.w - textPad*2)
	switch align {
	case 1:
		x -= tw / 2
	case 2:
		x -= tw
	}
	s.mask(m, int(x+.5)-textPad, int(y+.5)-m.h/2, c, op)
}

func (w *window) button(s *surface, r rectF, label string, u float32, primary bool, id int, op float32, disabled bool) {
	hot := w.hover == id && !disabled
	down := w.pressed == id && !disabled
	if primary {
		a, b := cPurple, cBlue
		if hot {
			a, b = mix(a, cWhite, .12), mix(b, cWhite, .12)
		}
		if down {
			a, b = mix(a, cBg0, .18), mix(b, cBg0, .18)
		}
		s.glow(r.x+r.w/2, r.y+r.h*.7, r.w*.62, cPurple, 0, .22*op)
		s.roundRect(r.x, r.y, r.w, r.h, r.h/2, linear(r.x, r.y, r.x+r.w, r.y+r.h, a, b), op)
	} else {
		bg := float32(.08)
		if hot {
			bg = .14
		}
		if down {
			bg = .05
		}
		s.roundRect(r.x, r.y, r.w, r.h, r.h/2, solid(cWhite), bg*op)
		s.strokeRoundRect(r.x, r.y, r.w, r.h, r.h/2, u, cWhite, .12*op)
	}
	c := cWhite
	if disabled {
		c = cDim
	}
	w.txt(s, label, r.x+r.w/2, r.y+r.h/2, 17, 600, c, op, 1, layout{u: u})
}

func (w *window) drawIdleUI(s *surface, l layout, now time.Time, a float32) {
	if a <= 0 {
		return
	}
	u := l.u
	// gear
	gc := cMuted
	if w.hover == hGear {
		gc = cWhite
	}
	gx, gy := l.gear.x+l.gear.w/2, l.gear.y+l.gear.h/2
	s.ring(gx, gy, 6.2*u, 4.2*u, solid(gc), a)
	for k := 0; k < 8; k++ {
		ang := float64(k) * math.Pi / 4
		cs, sn := f32(math.Cos(ang)), f32(math.Sin(ang))
		s.capsule(gx+cs*8.6*u, gy+sn*8.6*u, gx+cs*10.6*u, gy+sn*10.6*u, 3.6*u, solid(gc), a)
	}

	fileA := f32(w.selA)
	if fileA < 1 {
		bo := a * (1 - fileA)
		w.button(s, l.choose, w.tr("Choose files"), u, true, hChoose, bo, false)
		if w.opts.Queue != nil {
			w.txt(s, w.tr("or drop files here"), l.cx, l.choose.y+l.choose.h+22*u, 12.5, 400, cDim, bo, 1, l)
		}
	}
	if fileA > 0 {
		fa := a * fileA
		w.drawSummary(s, l, fa)
		w.drawTargetChip(s, l, now, fa)
		w.drawSlider(s, l, fa)
	}
}

func (w *window) drawSummary(s *surface, l layout, a float32) {
	u := l.u
	r := l.summary
	bg := float32(.08)
	if w.hover == hSummary {
		bg = .13
	}
	s.roundRect(r.x, r.y, r.w, r.h, r.h/2, solid(cWhite), bg*a)
	s.strokeRoundRect(r.x, r.y, r.w, r.h, r.h/2, u, cWhite, .12*a)
	// file glyph
	fx, fy := r.x+22*u, r.y+r.h/2
	s.roundRect(fx-6*u, fy-8*u, 12*u, 16*u, 3*u, linear(fx, fy-8*u, fx, fy+8*u, cPurple, cBlue), a)
	s.roundRect(fx-3*u, fy-3*u, 6*u, 1.6*u, .8*u, solid(cPanel), a*.8)
	s.roundRect(fx-3*u, fy+1*u, 6*u, 1.6*u, .8*u, solid(cPanel), a*.8)
	label := w.summaryText()
	label = w.ts.fit(label, int(r.w-110*u), int(16*u+.5), 600)
	w.txt(s, label, fx+18*u, fy, 16, 600, cWhite, a, 0, l)
	// clear button
	cc := cMuted
	if w.hover == hClear {
		cc = cWhite
	}
	cx, cy := l.clear.x+l.clear.w/2, l.clear.y+l.clear.h/2
	if w.hover == hClear {
		s.circle(cx, cy, 14*u, solid(cWhite), .12*a)
	}
	s.capsule(cx-4.5*u, cy-4.5*u, cx+4.5*u, cy+4.5*u, 1.9*u, solid(cc), a)
	s.capsule(cx-4.5*u, cy+4.5*u, cx+4.5*u, cy-4.5*u, 1.9*u, solid(cc), a)
}

func (w *window) summaryText() string {
	var total int64
	for _, sz := range w.sizes {
		total += sz
	}
	if len(w.paths) == 1 {
		return filepath.Base(w.paths[0]) + " · " + w.byteSize(total)
	}
	return w.filesLabel(len(w.paths)) + " · " + w.byteSize(total)
}

func (w *window) targetText() (string, col, bool) {
	for _, p := range w.phones {
		if p.ID == w.selectedPhone {
			name := p.Name
			if name == "" {
				name = p.ID
			}
			return name, cGood, len(w.phones) > 1
		}
	}
	if len(w.phones) == 0 {
		return w.tr("Open Rocket on the phone"), cWarn, false
	}
	return w.tr("Choose a phone"), cWarn, true
}

func (w *window) drawTargetChip(s *surface, l layout, now time.Time, a float32) {
	u := l.u
	r := l.target
	name, dot, more := w.targetText()
	pulse := f32(clamp(1-now.Sub(w.flashAt).Seconds()/.9, 0, 1))
	if w.flashAt.IsZero() {
		pulse = 0
	}
	hot := w.hover == hTarget && more
	bg := float32(.045)
	if hot {
		bg = .10
	}
	s.roundRect(r.x, r.y, r.w, r.h, r.h/2, solid(cWhite), (bg+pulse*.05)*a)
	if pulse > 0 {
		s.strokeRoundRect(r.x, r.y, r.w, r.h, r.h/2, 1.4*u, cWarn, pulse*a)
	}
	s.circle(r.x+18*u, r.y+r.h/2, 3.6*u, solid(dot), a)
	label := name
	if dot == cGood {
		label = w.tr("To") + " " + name
	}
	label = w.ts.fit(label, int(r.w-66*u), int(14*u+.5), 500)
	w.txt(s, label, r.x+32*u, r.y+r.h/2, 14, 500, cMuted, a, 0, l)
	if more {
		cx, cy := r.x+r.w-20*u, r.y+r.h/2
		s.capsule(cx-4.5*u, cy-2*u, cx, cy+2.6*u, 1.7*u, solid(cMuted), a)
		s.capsule(cx, cy+2.6*u, cx+4.5*u, cy-2*u, 1.7*u, solid(cMuted), a)
	}
}

func (w *window) drawSlider(s *surface, l layout, a float32) {
	u := l.u
	t := l.track
	ok := w.canLaunch()
	pos := f32(w.sl.pos)
	s.roundRect(t.x, t.y, t.w, t.h, t.h/2, solid(cWhite), .07*a)
	s.strokeRoundRect(t.x, t.y, t.w, t.h, t.h/2, u, cWhite, .12*a)
	// progress fill grows with the thumb
	fillW := (l.thumb.x + l.thumb.w + 4*u) - t.x
	fa := a * (.25 + .6*pos)
	if !ok {
		fa *= .25
	}
	s.roundRect(t.x, t.y, fillW, t.h, t.h/2, linear(t.x, t.y, t.x+t.w, t.y, cPurple, cBlue), fa)
	labelA := a * f32(1-clamp(float64(pos)*2.2, 0, 1))
	c := cWhite
	if !ok {
		c = cDim
	}
	w.txt(s, w.tr("Let's go!"), t.x+t.w/2+16*u, t.y+t.h/2, 18, 600, c, labelA, 1, l)
	// thumb
	th := l.thumb
	cx, cy := th.x+th.w/2, th.y+th.h/2
	pa, pb := cPurple, cBlue
	if !ok {
		pa, pb = mix(pa, cDim, .55), mix(pb, cDim, .55)
	} else if w.sl.drag || w.hover == hThumb {
		pa, pb = mix(pa, cWhite, .15), mix(pb, cWhite, .15)
	}
	if ok {
		s.glow(cx, cy+4*u, 46*u, cPurple, 0, .4*a)
	}
	s.circle(cx, cy, th.w/2, linear(th.x, th.y, th.x+th.w, th.y+th.h, pa, pb), a)
	for k := 0; k < 2; k++ {
		ox := cx - 5*u + float32(k)*9*u
		s.capsule(ox-2.6*u, cy-6.5*u, ox+2.6*u, cy, 2.5*u, solid(cWhite), a)
		s.capsule(ox+2.6*u, cy, ox-2.6*u, cy+6.5*u, 2.5*u, solid(cWhite), a)
	}
}

func (w *window) drawSheet(s *surface, l layout) {
	u := l.u
	s.roundRect(0, 0, l.w, l.h, 0, solid(cBg0), .55)
	r := l.sheet
	s.roundRect(r.x, r.y, r.w, r.h, 22*u, solid(cPanel), .98)
	s.strokeRoundRect(r.x, r.y, r.w, r.h, 22*u, u, cWhite, .12)
	for i, row := range l.rows {
		p := w.phones[i]
		if w.hover == hPhoneBase+i {
			s.roundRect(row.x, row.y, row.w, row.h, 16*u, solid(cWhite), .09)
		}
		name := p.Name
		if name == "" {
			name = p.ID
		}
		name = w.ts.fit(name, int(row.w-70*u), int(15*u+.5), 500)
		w.txt(s, name, row.x+18*u, row.y+row.h/2, 15, 500, cWhite, 1, 0, l)
		if p.ID == w.selectedPhone {
			cx, cy := row.x+row.w-24*u, row.y+row.h/2
			s.capsule(cx-6*u, cy, cx-2*u, cy+4*u, 2.2*u, solid(cGood), 1)
			s.capsule(cx-2*u, cy+4*u, cx+6*u, cy-4*u, 2.2*u, solid(cGood), 1)
		}
	}
}

// drawCard renders the result panel. ok selects the success or the error look.
func (w *window) drawCard(s *surface, l layout, a, scale float32, ok bool) {
	if a <= 0 {
		return
	}
	u := l.u
	r := l.card
	cx, cy := r.x+r.w/2, r.y+r.h/2
	r = rectF{cx - r.w*scale/2, cy - r.h*scale/2, r.w * scale, r.h * scale}
	glowC := cPurple
	if !ok {
		glowC = cBad
	}
	s.glow(cx, cy, r.w*.95, glowC, 0, .2*a)
	s.roundRect(r.x, r.y, r.w, r.h, 30*u, solid(cPanel), .95*a)
	s.strokeRoundRect(r.x, r.y, r.w, r.h, 30*u, u, cWhite, .13*a)
	bx, by := cx, r.y+58*u*scale
	if ok {
		s.glow(bx, by, 70*u, cPurple, 0, .45*a)
		s.circle(bx, by, 30*u, linear(bx-30*u, by-30*u, bx+30*u, by+30*u, cPurple, cBlue), a)
		s.capsule(bx-11*u, by+1*u, bx-3.5*u, by+8.5*u, 4.2*u, solid(cWhite), a)
		s.capsule(bx-3.5*u, by+8.5*u, bx+12*u, by-8*u, 4.2*u, solid(cWhite), a)
		verb := w.tr("Files sent")
		if w.current.Direction == "incoming" {
			verb = w.tr("Files received")
		}
		w.txt(s, w.tr("Delivered!"), cx, r.y+118*u*scale, 28, 700, cWhite, a, 1, l)
		w.txt(s, verb, cx, r.y+146*u*scale, 14.5, 400, cMuted, a, 1, l)
		bytes := w.current.Bytes
		if bytes <= 0 && w.current.Total > 0 {
			bytes = w.current.Total
		}
		values := []string{fmt.Sprintf("%d", max(0, w.current.Files)), w.byteSize(bytes), w.duration(w.current.Duration), w.speed(bytes, w.current.Duration)}
		labels := []string{w.tr("Files"), w.tr("Size"), w.tr("Time"), w.tr("Avg. speed")}
		gap := 10 * u
		tw := (r.w - 44*u - gap) / 2
		th := 66 * u * scale
		for i := range values {
			tx := r.x + 22*u + float32(i%2)*(tw+gap)
			ty := r.y + 172*u*scale + float32(i/2)*(th+gap)
			s.roundRect(tx, ty, tw, th, 18*u, solid(cWhite), .06*a)
			w.txt(s, w.ts.fit(values[i], int(tw-20*u), int(21*u+.5), 700), tx+tw/2, ty+th*.40, 21, 700, cWhite, a, 1, l)
			w.txt(s, labels[i], tx+tw/2, ty+th*.77, 12, 400, cMuted, a, 1, l)
		}
	} else {
		s.glow(bx, by, 70*u, cBad, 0, .35*a)
		s.circle(bx, by, 30*u, linear(bx-30*u, by-30*u, bx+30*u, by+30*u, mix(cBad, cWhite, .1), mix(cBad, cPurple, .6)), a)
		s.capsule(bx-9*u, by-9*u, bx+9*u, by+9*u, 4.2*u, solid(cWhite), a)
		s.capsule(bx-9*u, by+9*u, bx+9*u, by-9*u, 4.2*u, solid(cWhite), a)
		w.txt(s, w.tr("Didn't make it"), cx, r.y+118*u*scale, 26, 700, cWhite, a, 1, l)
		for i, line := range w.ts.wrap(w.failMsg, int(r.w-56*u), int(15*u+.5), 400, 4) {
			w.txt(s, line, cx, r.y+156*u*scale+float32(i)*24*u, 15, 400, cMuted, a, 1, l)
		}
		w.txt(s, w.tr("Your files are still selected."), cx, r.y+r.h-112*u*scale, 13, 400, cDim, a, 1, l)
	}
	if l.ok.w > 0 && (w.state == resultOK || w.state == resultFail || w.state == departing && a > .7) {
		if w.state == resultOK && w.current.Direction == "incoming" {
			w.button(s, l.openOut, w.tr("Open folder"), u, false, hOpenOut, a, false)
		}
		w.button(s, l.ok, w.tr("OK"), u, true, hOK, a, false)
	}
}
