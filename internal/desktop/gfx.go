package desktop

import (
	"image"
	"math"
)

// A tiny CPU renderer: antialiased shapes, glows, a premultiplied sprite and text
// coverage masks drawn into one BGRA buffer that Direct2D then presents. It exists
// because GDI cannot antialias or alpha-blend, and it keeps the exe dependency-free.

// col is a straight-alpha colour: r, g, b in 0..255, a in 0..1.
type col struct{ r, g, b, a float32 }

func rgbCol(hex uint32) col {
	return col{float32(hex >> 16 & 255), float32(hex >> 8 & 255), float32(hex & 255), 1}
}
func (c col) withAlpha(a float32) col { c.a = a; return c }
func mix(a, b col, t float32) col {
	return col{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

type surface struct {
	w, h int
	pix  []uint32 // 0xAARRGGBB in a little-endian word = BGRA bytes, as Direct2D expects
}

func newSurface(w, h int) *surface { return &surface{w: w, h: h, pix: make([]uint32, w*h)} }

func (s *surface) copyFrom(o *surface) { copy(s.pix, o.pix) }

func (s *surface) put(i int, r, g, b, a float32) {
	if a <= 0 {
		return
	}
	d := s.pix[i]
	dr, dg, db := float32(d>>16&255), float32(d>>8&255), float32(d&255)
	if a > 1 {
		a = 1
	}
	s.pix[i] = 0xff000000 |
		uint32(dr+(r-dr)*a+.5)<<16 | uint32(dg+(g-dg)*a+.5)<<8 | uint32(db+(b-db)*a+.5)
}

func (s *surface) gradientV(top, bottom col) {
	for y := 0; y < s.h; y++ {
		c := mix(top, bottom, float32(y)/float32(max(1, s.h-1)))
		v := 0xff000000 | uint32(c.r+.5)<<16 | uint32(c.g+.5)<<8 | uint32(c.b+.5)
		row := s.pix[y*s.w : (y+1)*s.w]
		for x := range row {
			row[x] = v
		}
	}
}

// paint is a solid colour or a two-stop linear gradient.
type paint struct {
	a, b           col
	x0, y0, x1, y1 float32
	grad           bool
}

func solid(c col) paint { return paint{a: c} }
func linear(x0, y0, x1, y1 float32, a, b col) paint {
	return paint{a: a, b: b, x0: x0, y0: y0, x1: x1, y1: y1, grad: true}
}
func (p *paint) at(x, y float32) col {
	if !p.grad {
		return p.a
	}
	dx, dy := p.x1-p.x0, p.y1-p.y0
	t := ((x-p.x0)*dx + (y-p.y0)*dy) / (dx*dx + dy*dy + 1e-6)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return mix(p.a, p.b, t)
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (s *surface) clip(x0, y0, x1, y1 float32) (int, int, int, int) {
	return max(0, int(math.Floor(float64(x0)))), max(0, int(math.Floor(float64(y0)))),
		min(s.w, int(math.Ceil(float64(x1)))), min(s.h, int(math.Ceil(float64(y1))))
}

// roundRect fills an antialiased rounded rectangle (radius is clamped to half the short side).
func (s *surface) roundRect(x, y, w, h, r float32, p paint, opacity float32) {
	if opacity <= 0 || w <= 0 || h <= 0 {
		return
	}
	r = min(r, min(w, h)/2)
	cx, cy := x+w/2, y+h/2
	hx, hy := w/2-r, h/2-r
	ix0, iy0, ix1, iy1 := s.clip(x-1, y-1, x+w+1, y+h+1)
	for py := iy0; py < iy1; py++ {
		fy := float32(py) + .5
		qy := float32(math.Abs(float64(fy-cy))) - hy
		row := py * s.w
		for px := ix0; px < ix1; px++ {
			fx := float32(px) + .5
			qx := float32(math.Abs(float64(fx-cx))) - hx
			var d float32
			switch {
			case qx > 0 && qy > 0:
				d = float32(math.Sqrt(float64(qx*qx+qy*qy))) - r
			case qx > qy:
				d = qx - r
			default:
				d = qy - r
			}
			cov := clamp01(.5 - d)
			if cov == 0 {
				continue
			}
			c := p.at(fx, fy)
			s.put(row+px, c.r, c.g, c.b, c.a*cov*opacity)
		}
	}
}

// strokeRoundRect draws a hairline border inside the rounded rectangle.
func (s *surface) strokeRoundRect(x, y, w, h, r, thick float32, c col, opacity float32) {
	if opacity <= 0 {
		return
	}
	r = min(r, min(w, h)/2)
	cx, cy := x+w/2, y+h/2
	hx, hy := w/2-r, h/2-r
	ix0, iy0, ix1, iy1 := s.clip(x-1, y-1, x+w+1, y+h+1)
	for py := iy0; py < iy1; py++ {
		fy := float32(py) + .5
		qy := float32(math.Abs(float64(fy-cy))) - hy
		for px := ix0; px < ix1; px++ {
			fx := float32(px) + .5
			qx := float32(math.Abs(float64(fx-cx))) - hx
			var d float32
			switch {
			case qx > 0 && qy > 0:
				d = float32(math.Sqrt(float64(qx*qx+qy*qy))) - r
			case qx > qy:
				d = qx - r
			default:
				d = qy - r
			}
			// ring: |d + thick/2| < thick/2, anchored on the inside edge
			cov := clamp01(.5 - float32(math.Abs(float64(d+thick/2))) + thick/2)
			if cov > 0 {
				s.put(py*s.w+px, c.r, c.g, c.b, c.a*cov*opacity)
			}
		}
	}
}

func (s *surface) circle(cx, cy, r float32, p paint, opacity float32) {
	if opacity <= 0 {
		return
	}
	ix0, iy0, ix1, iy1 := s.clip(cx-r-1, cy-r-1, cx+r+1, cy+r+1)
	for py := iy0; py < iy1; py++ {
		fy := float32(py) + .5
		for px := ix0; px < ix1; px++ {
			fx := float32(px) + .5
			d := float32(math.Hypot(float64(fx-cx), float64(fy-cy))) - r
			if cov := clamp01(.5 - d); cov > 0 {
				c := p.at(fx, fy)
				s.put(py*s.w+px, c.r, c.g, c.b, c.a*cov*opacity)
			}
		}
	}
}

func (s *surface) ring(cx, cy, r, thick float32, p paint, opacity float32) {
	if opacity <= 0 {
		return
	}
	out := r + thick/2
	ix0, iy0, ix1, iy1 := s.clip(cx-out-1, cy-out-1, cx+out+1, cy+out+1)
	for py := iy0; py < iy1; py++ {
		fy := float32(py) + .5
		for px := ix0; px < ix1; px++ {
			fx := float32(px) + .5
			d := float32(math.Abs(math.Hypot(float64(fx-cx), float64(fy-cy))-float64(r))) - thick/2
			if cov := clamp01(.5 - d); cov > 0 {
				c := p.at(fx, fy)
				s.put(py*s.w+px, c.r, c.g, c.b, c.a*cov*opacity)
			}
		}
	}
}

// capsule is a line segment with round caps.
func (s *surface) capsule(x0, y0, x1, y1, width float32, p paint, opacity float32) {
	if opacity <= 0 {
		return
	}
	half := width / 2
	ix0, iy0, ix1, iy1 := s.clip(min(x0, x1)-half-1, min(y0, y1)-half-1, max(x0, x1)+half+1, max(y0, y1)+half+1)
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	for py := iy0; py < iy1; py++ {
		fy := float32(py) + .5
		for px := ix0; px < ix1; px++ {
			fx := float32(px) + .5
			t := float32(0)
			if l2 > 0 {
				t = clamp01(((fx-x0)*dx + (fy-y0)*dy) / l2)
			}
			ex, ey := fx-(x0+dx*t), fy-(y0+dy*t)
			d := float32(math.Sqrt(float64(ex*ex+ey*ey))) - half
			if cov := clamp01(.5 - d); cov > 0 {
				c := p.at(fx, fy)
				s.put(py*s.w+px, c.r, c.g, c.b, c.a*cov*opacity)
			}
		}
	}
}

// glow paints a radial falloff without any square roots. hard is the fraction of the
// radius that stays fully opaque (0 = pure glow, 0.6 = soft smoke puff).
func (s *surface) glow(cx, cy, r float32, c col, hard, opacity float32) {
	if opacity <= 0 || r <= 0 {
		return
	}
	ix0, iy0, ix1, iy1 := s.clip(cx-r, cy-r, cx+r+1, cy+r+1)
	inv := 1 / (r * r)
	h2 := hard * hard
	for py := iy0; py < iy1; py++ {
		dy := float32(py) + .5 - cy
		for px := ix0; px < ix1; px++ {
			dx := float32(px) + .5 - cx
			t2 := (dx*dx + dy*dy) * inv
			if t2 >= 1 {
				continue
			}
			var k float32
			switch {
			case hard == 0:
				k = 1 - t2 // peaked, gaussian-like core for flames and halos
				k *= k
			case t2 <= h2:
				k = 1
			default:
				u := (t2 - h2) / (1 - h2)
				k = 1 - u*u*(3-2*u)
				k *= k
			}
			s.put(py*s.w+px, c.r, c.g, c.b, c.a*k*opacity)
		}
	}
}

// bitmap is premultiplied BGRA (same word layout as surface).
type bitmap struct {
	w, h int
	pix  []uint32
}

// newBitmap box-filters src to w x h on premultiplied colour.
func newBitmap(src *image.NRGBA, w, h int) *bitmap {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	out := &bitmap{w: w, h: h, pix: make([]uint32, w*h)}
	for y := 0; y < h; y++ {
		y0, y1 := float64(y)*float64(sh)/float64(h), float64(y+1)*float64(sh)/float64(h)
		for x := 0; x < w; x++ {
			x0, x1 := float64(x)*float64(sw)/float64(w), float64(x+1)*float64(sw)/float64(w)
			var r, g, b, a, total float64
			for sy := int(y0); sy < int(math.Ceil(y1)) && sy < sh; sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); sx < int(math.Ceil(x1)) && sx < sw; sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					o := src.PixOffset(sx, sy)
					al := float64(src.Pix[o+3]) / 255
					wt := wx * wy
					r += float64(src.Pix[o]) * al * wt
					g += float64(src.Pix[o+1]) * al * wt
					b += float64(src.Pix[o+2]) * al * wt
					a += al * wt
					total += wt
				}
			}
			if total <= 0 {
				continue
			}
			out.pix[y*w+x] = uint32(a/total*255+.5)<<24 | uint32(r/total+.5)<<16 | uint32(g/total+.5)<<8 | uint32(b/total+.5)
		}
	}
	return out
}

// draw composites the bitmap with its top-left corner at (x, y); opacity scales it.
func (s *surface) draw(b *bitmap, x, y int, opacity float32) {
	if opacity <= 0 || b == nil {
		return
	}
	op := int(opacity*256 + .5)
	for by := 0; by < b.h; by++ {
		dy := y + by
		if dy < 0 || dy >= s.h {
			continue
		}
		for bx := 0; bx < b.w; bx++ {
			dx := x + bx
			if dx < 0 || dx >= s.w {
				continue
			}
			p := b.pix[by*b.w+bx]
			sa := int(p>>24) * op >> 8
			if sa == 0 {
				continue
			}
			sr, sg, sb := int(p>>16&255)*op>>8, int(p>>8&255)*op>>8, int(p&255)*op>>8
			i := dy*s.w + dx
			d := s.pix[i]
			ia := 255 - sa
			nr := sr + int(d>>16&255)*ia/255
			ng := sg + int(d>>8&255)*ia/255
			nb := sb + int(d&255)*ia/255
			s.pix[i] = 0xff000000 | uint32(min(nr, 255))<<16 | uint32(min(ng, 255))<<8 | uint32(min(nb, 255))
		}
	}
}

// textMask is 8-bit coverage of one rendered string.
type textMask struct {
	w, h int
	cov  []uint8
}

func (s *surface) mask(m *textMask, x, y int, c col, opacity float32) {
	if m == nil || opacity <= 0 {
		return
	}
	a := c.a * opacity / 255
	for my := 0; my < m.h; my++ {
		dy := y + my
		if dy < 0 || dy >= s.h {
			continue
		}
		for mx := 0; mx < m.w; mx++ {
			v := m.cov[my*m.w+mx]
			if v == 0 {
				continue
			}
			if dx := x + mx; dx >= 0 && dx < s.w {
				s.put(dy*s.w+dx, c.r, c.g, c.b, float32(v)*a)
			}
		}
	}
}
