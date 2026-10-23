// Command genassets turns assets/icon-source.png (the user's 1024x1024 icon)
// into every raster the apps need. Standard library only; run from the repo root:
//
//	go run ./tools/genassets
//
// The source is a flat dark disc with black corners and no alpha, so the rocket is
// cut out by colour distance from the disc colour (edges keep partial alpha) and
// enclosed holes such as the porthole are refilled.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const (
	size       = 1024
	discRadius = 490.0 // ignore the antialiased rim of the disc
	solidDist  = 110.0 // every rocket colour is at least this far from the disc colour
)

var bg = [3]float64{39, 39, 51}

type rgba struct{ r, g, b, a float64 } // straight alpha, 0..255 / 0..1

func main() {
	src := mustDecode(filepath.Join("assets", "icon-source.png"))
	sprite, mono := cutRocket(src)
	box := bounds(sprite, 0.02)
	fmt.Printf("rocket bbox in icon space: %v\n", box)
	nose, tail := extremes(sprite)
	fmt.Printf("nose tip %v, flame tip %v (icon space)\n", nose, tail)

	pad := 6
	crop := image.Rect(box.Min.X-pad, box.Min.Y-pad, box.Max.X+pad, box.Max.Y+pad)
	fmt.Printf("sprite crop origin (%d,%d) size %dx%d\n", crop.Min.X, crop.Min.Y, crop.Dx(), crop.Dy())
	write(filepath.Join("internal", "desktop", "assets", "rocket.png"), toNRGBA(sprite, crop))
	write(filepath.Join("android", "app", "src", "main", "res", "drawable-nodpi", "rocket.png"), toNRGBA(sprite, crop))

	// Android adaptive icon: 108dp canvas at xxxhdpi (432px). The disc is scaled so the
	// rocket stays inside the launcher's safe zone; the background layer is the disc colour.
	res := filepath.Join("android", "app", "src", "main", "res")
	scale := 304.0 / size
	write(filepath.Join(res, "drawable-nodpi", "ic_launcher_foreground.png"), onCanvas(sprite, 432, scale, 1))
	write(filepath.Join(res, "drawable-nodpi", "ic_launcher_monochrome.png"), onCanvas(mono, 432, scale, 0))

	// Windows icon: the full disc with transparent corners.
	disc := circleIcon(src)
	write(filepath.Join("assets", "icon-circle.png"), disc)
	var entries [][]byte
	var sizes []int
	for _, s := range []int{16, 20, 24, 32, 40, 48, 64, 128, 256} {
		var buf bytes.Buffer
		if err := png.Encode(&buf, resize(disc, s, s)); err != nil {
			panic(err)
		}
		entries = append(entries, buf.Bytes())
		sizes = append(sizes, s)
	}
	writeFile(filepath.Join("cmd", "rocket", "rocket.ico"), buildICO(sizes, entries))
}

func mustDecode(path string) *image.NRGBA {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	out := image.NewNRGBA(img.Bounds())
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

func dist(p [3]float64) float64 {
	return math.Sqrt((p[0]-bg[0])*(p[0]-bg[0]) + (p[1]-bg[1])*(p[1]-bg[1]) + (p[2]-bg[2])*(p[2]-bg[2]))
}

func pixel(img *image.NRGBA, x, y int) [3]float64 {
	c := img.NRGBAAt(x, y)
	return [3]float64{float64(c.R), float64(c.G), float64(c.B)}
}

// cutRocket returns the rocket with straight alpha (porthole refilled) and a mono
// variant whose alpha ignores the refill so the porthole stays a hole.
func cutRocket(src *image.NRGBA) (*image.NRGBA, *image.NRGBA) {
	cx, cy := 511.5, 511.5
	inside := func(x, y int) bool {
		return math.Hypot(float64(x)-cx, float64(y)-cy) <= discRadius
	}
	solid := make([]bool, size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			solid[y*size+x] = inside(x, y) && dist(pixel(src, x, y)) >= solidDist
		}
	}
	// The source carries JPEG-like speckle: keep only large connected solid regions
	// (body, nose, fins, flame) so no noise survives outside the rocket.
	seen := make([]bool, size*size)
	for start := range solid {
		if !solid[start] || seen[start] {
			continue
		}
		component := []int{start}
		seen[start] = true
		for head := 0; head < len(component); head++ {
			x, y := component[head]%size, component[head]/size
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= size || ny >= size {
					continue
				}
				if j := ny*size + nx; solid[j] && !seen[j] {
					seen[j] = true
					component = append(component, j)
				}
			}
		}
		if len(component) < 3000 {
			for _, i := range component {
				solid[i] = false
			}
		}
	}
	px := make([]rgba, size*size)
	const reach = 5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if p := pixel(src, x, y); solid[y*size+x] {
				px[y*size+x] = rgba{p[0], p[1], p[2], 1}
			}
		}
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := y*size + x
			p := pixel(src, x, y)
			if !inside(x, y) || solid[i] {
				continue
			}
			if dist(p) < 5 {
				continue
			}
			best, bestD := -1, 1e9
			for dy := -reach; dy <= reach; dy++ {
				for dx := -reach; dx <= reach; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= size || ny >= size || !solid[ny*size+nx] {
						continue
					}
					if d := float64(dx*dx + dy*dy); d < bestD {
						best, bestD = ny*size+nx, d
					}
				}
			}
			if best < 0 {
				continue // not next to the rocket: background noise
			}
			f := px[best]
			fd := [3]float64{f.r - bg[0], f.g - bg[1], f.b - bg[2]}
			den := fd[0]*fd[0] + fd[1]*fd[1] + fd[2]*fd[2]
			a := ((p[0]-bg[0])*fd[0] + (p[1]-bg[1])*fd[1] + (p[2]-bg[2])*fd[2]) / den
			a = math.Max(0, math.Min(1, a))
			px[i] = rgba{f.r, f.g, f.b, a}
		}
	}
	// Mono variant keeps the holes.
	mono := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i, p := range px {
		mono.Pix[i*4], mono.Pix[i*4+1], mono.Pix[i*4+2], mono.Pix[i*4+3] = 255, 255, 255, uint8(math.Round(p.a*255))
	}
	// Flood the transparent exterior from the corner; whatever stays transparent is a hole.
	outside := make([]bool, size*size)
	stack := []int{0}
	outside[0] = true
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := i%size, i/size
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if nx < 0 || ny < 0 || nx >= size || ny >= size {
				continue
			}
			j := ny*size + nx
			if !outside[j] && px[j].a < 0.5 {
				outside[j] = true
				stack = append(stack, j)
			}
		}
	}
	hole := make([]bool, size*size)
	for i := range px {
		hole[i] = !outside[i] && px[i].a < 0.5
	}
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := y*size + x
			p := px[i]
			near := hole[i]
			if !near && p.a < 1 && p.a > 0 {
				for dy := -2; dy <= 2 && !near; dy++ {
					for dx := -2; dx <= 2; dx++ {
						nx, ny := x+dx, y+dy
						if nx >= 0 && ny >= 0 && nx < size && ny < size && hole[ny*size+nx] {
							near = true
							break
						}
					}
				}
			}
			if near {
				s := pixel(src, x, y)
				p = rgba{s[0], s[1], s[2], 1}
			}
			out.SetNRGBA(x, y, color.NRGBA{uint8(math.Round(p.r)), uint8(math.Round(p.g)), uint8(math.Round(p.b)), uint8(math.Round(p.a * 255))})
		}
	}
	return out, mono
}

func clamp255(v float64) float64 { return math.Max(0, math.Min(255, v)) }

func bounds(img *image.NRGBA, minAlpha float64) image.Rectangle {
	x0, y0, x1, y1 := size, size, 0, 0 // image.Rect would silently reorder an inverted start value
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if float64(img.NRGBAAt(x, y).A)/255 > minAlpha {
				x0, y0 = min(x0, x), min(y0, y)
				x1, y1 = max(x1, x+1), max(y1, y+1)
			}
		}
	}
	return image.Rect(x0, y0, x1, y1)
}

// extremes finds the nose and flame tips along the rocket axis (about 37 degrees from vertical).
func extremes(img *image.NRGBA) (image.Point, image.Point) {
	ax, ay := 0.6, -0.8
	var nose, tail image.Point
	hi, lo := -1e9, 1e9
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if img.NRGBAAt(x, y).A < 128 {
				continue
			}
			d := float64(x)*ax + float64(y)*ay
			if d > hi {
				hi, nose = d, image.Pt(x, y)
			}
			if d < lo {
				lo, tail = d, image.Pt(x, y)
			}
		}
	}
	return nose, tail
}

func toNRGBA(src *image.NRGBA, r image.Rectangle) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			out.SetNRGBA(x, y, src.NRGBAAt(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out
}

// onCanvas places the full 1024 image (scaled) centred on a square canvas.
func onCanvas(src *image.NRGBA, canvas int, scale float64, _ int) *image.NRGBA {
	n := int(math.Round(size * scale))
	small := resize(src, n, n)
	out := image.NewNRGBA(image.Rect(0, 0, canvas, canvas))
	off := (canvas - n) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			out.SetNRGBA(off+x, off+y, small.NRGBAAt(x, y))
		}
	}
	return out
}

func circleIcon(src *image.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(src.Bounds())
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r := math.Hypot(float64(x)-511.5, float64(y)-511.5)
			a := math.Max(0, math.Min(1, (506.5-r)/1.5))
			c := src.NRGBAAt(x, y)
			out.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(math.Round(a * 255))})
		}
	}
	return out
}

// resize is an exact box filter on premultiplied colour (the sources are always larger).
func resize(src *image.NRGBA, w, h int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < h; y++ {
		y0, y1 := float64(y)*float64(sh)/float64(h), float64(y+1)*float64(sh)/float64(h)
		for x := 0; x < w; x++ {
			x0, x1 := float64(x)*float64(sw)/float64(w), float64(x+1)*float64(sw)/float64(w)
			var r, g, b, a, wsum float64
			for sy := int(y0); sy < int(math.Ceil(y1)); sy++ {
				wy := math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy))
				for sx := int(x0); sx < int(math.Ceil(x1)); sx++ {
					wx := math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))
					c := src.NRGBAAt(sx, sy)
					wt := wx * wy
					al := float64(c.A) / 255
					r += float64(c.R) * al * wt
					g += float64(c.G) * al * wt
					b += float64(c.B) * al * wt
					a += al * wt
					wsum += wt
				}
			}
			if a <= 0 || wsum <= 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{uint8(math.Round(r / a)), uint8(math.Round(g / a)), uint8(math.Round(b / a)), uint8(math.Round(a / wsum * 255))})
		}
	}
	return out
}

func buildICO(sizes []int, pngs [][]byte) []byte {
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		out.Write(p)
	}
	return out.Bytes()
}

func write(path string, img image.Image) {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		panic(err)
	}
	writeFile(path, buf.Bytes())
}

func writeFile(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
}
