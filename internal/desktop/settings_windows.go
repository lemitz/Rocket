//go:build windows

package desktop

import (
	"fmt"
	"time"
)

type settingsLayout struct {
	x, w, top                float32
	back                     rectF
	folderCard, folderChange rectF
	folderOpen               rectF
	pcCard, netCard          rectF
	fwButton                 rectF
	phoneCard, footer        rectF
	contentH                 float32
}

// settingsLayout positions every block for the current scroll offset; drawing and
// hit-testing both use it so they can never disagree.
func (w *window) settingsLayout(l layout) settingsLayout {
	u := l.u
	cw := min(l.w-32*u, 440*u)
	s := settingsLayout{x: l.cx - cw/2, w: cw, top: 84 * u}
	y := s.top - w.setScroll
	gap := 14 * u
	s.back = rectF{s.x, 22 * u, 44 * u, 44 * u}

	s.folderCard = rectF{s.x, y, cw, 128 * u}
	s.folderChange = rectF{s.x + 18*u, y + 76*u, (cw - 36*u - 10*u) / 2, 38 * u}
	s.folderOpen = rectF{s.folderChange.x + s.folderChange.w + 10*u, y + 76*u, s.folderChange.w, 38 * u}
	y += s.folderCard.h + gap

	n := max(1, min(len(w.opts.Addresses), 4))
	s.pcCard = rectF{s.x, y, cw, 74*u + float32(n)*22*u}
	y += s.pcCard.h + gap

	s.netCard = rectF{s.x, y, cw, 176 * u}
	s.fwButton = rectF{s.x + cw - 18*u - 104*u, y + 40*u + 80*u + 3*u, 104 * u, 34 * u}
	y += s.netCard.h + gap

	m := max(1, min(len(w.phones), 4))
	s.phoneCard = rectF{s.x, y, cw, 52*u + float32(m)*28*u}
	y += s.phoneCard.h + gap

	s.footer = rectF{s.x, y, cw, 64 * u}
	y += s.footer.h + 28*u
	s.contentH = y + w.setScroll - s.top
	return s
}

func (w *window) maxSettingsScroll(l layout) float32 {
	s := w.settingsLayout(l)
	return max(0, s.contentH-(l.h-s.top))
}

func (w *window) drawSettings(s *surface, l layout, now time.Time) {
	a := f32(w.setA)
	u := l.u
	s.roundRect(0, 0, l.w, l.h, 0, solid(cBg0), .985*a)
	st := w.settingsLayout(l)
	slide := (1 - a) * 22 * u
	card := func(r rectF, title string) {
		r.y += slide
		s.roundRect(r.x, r.y, r.w, r.h, 22*u, solid(cWhite), .06*a)
		s.strokeRoundRect(r.x, r.y, r.w, r.h, 22*u, u, cWhite, .10*a)
		if title != "" {
			w.txt(s, w.upper(title), r.x+20*u, r.y+26*u, 11.5, 600, cPurple, a, 0, l)
		}
	}
	row := func(r rectF, label string, ok bool, status string, y float32, withButton bool) {
		c := cGood
		if !ok {
			c = cWarn
		}
		s.circle(r.x+24*u, y+slide, 4.2*u, solid(c), a)
		w.txt(s, label, r.x+40*u, y+slide, 15, 500, cWhite, a, 0, l)
		right := r.x + r.w - 20*u
		if withButton {
			right -= 112 * u
		}
		w.txt(s, status, right, y+slide, 13.5, 400, cMuted, a, 2, l)
	}
	button := func(r rectF, label string, id int, primary bool, disabled bool) {
		r.y += slide
		if r.y+r.h < st.top-8*u || r.y > l.h {
			return
		}
		w.button(s, r, label, u*.82, primary, id, a, disabled)
	}

	// Folder
	card(st.folderCard, w.tr("Receive folder"))
	folder := w.folder
	if folder == "" {
		folder = w.tr("Not set")
	}
	w.txt(s, w.ts.fit(folder, int(st.folderCard.w-40*u), int(15*u+.5), 500), st.folderCard.x+20*u, st.folderCard.y+56*u+slide, 15, 500, cWhite, a, 0, l)
	button(st.folderChange, w.tr("Change"), hFolder, false, w.opts.ChangeFolder == nil || w.folderBusy)
	button(st.folderOpen, w.tr("Open"), hFolderOpen, false, false)

	// This PC
	card(st.pcCard, w.tr("This computer"))
	name := w.opts.Name
	if name == "" {
		name = w.tr("This PC")
	}
	w.txt(s, name, st.pcCard.x+20*u, st.pcCard.y+52*u+slide, 16, 600, cWhite, a, 0, l)
	if len(w.opts.Addresses) == 0 {
		w.txt(s, w.tr("No network address found"), st.pcCard.x+20*u, st.pcCard.y+76*u+slide, 13.5, 400, cWarn, a, 0, l)
	}
	for i, addr := range w.opts.Addresses {
		if i == 4 {
			break
		}
		w.txt(s, addr, st.pcCard.x+20*u, st.pcCard.y+78*u+float32(i)*22*u+slide, 13.5, 400, cMuted, a, 0, l)
	}

	// Connection check
	card(st.netCard, w.tr("Connection check"))
	y0 := st.netCard.y + 40*u + 20*u
	srv, srvOK := w.tr("Running"), w.serverOnline
	if !w.serverOnline {
		srv = w.tr("Not running")
	}
	row(st.netCard, w.tr("Rocket server"), srvOK, srv, y0, false)
	disc, discOK := w.tr("Running"), w.opts.UDPOnline
	if !discOK {
		disc = w.tr("Unavailable")
	}
	row(st.netCard, w.tr("Device discovery"), discOK, disc, y0+40*u, false)
	fw, fwOK := w.tr("Checking…"), false
	switch w.fwState {
	case fwPresent:
		fw, fwOK = w.tr("Rules found"), true
	case fwMissing:
		fw = w.tr("Rules missing")
	}
	row(st.netCard, w.tr("Firewall"), fwOK || w.fwState == fwUnknown, fw, y0+80*u, true)
	if w.opts.FixFirewall != nil && w.fwState == fwMissing {
		button(st.fwButton, w.tr("Fix"), hFirewall, true, w.fwBusy)
	} else {
		button(st.fwButton, w.tr("Recheck"), hRecheck, false, w.fwBusy)
	}

	// Phones
	card(st.phoneCard, w.tr("Phones"))
	if len(w.phones) == 0 {
		w.txt(s, w.ts.fit(w.tr("No phones yet. Open Rocket on a phone."), int(st.phoneCard.w-40*u), int(13.5*u+.5), 400),
			st.phoneCard.x+20*u, st.phoneCard.y+60*u+slide, 13.5, 400, cMuted, a, 0, l)
	}
	for i, p := range w.phones {
		if i == 4 {
			break
		}
		y := st.phoneCard.y + 60*u + float32(i)*28*u + slide
		s.circle(st.phoneCard.x+24*u, y, 4*u, solid(cGood), a)
		label := p.Name
		if label == "" {
			label = p.ID
		}
		w.txt(s, w.ts.fit(label, int(st.phoneCard.w-130*u), int(15*u+.5), 500), st.phoneCard.x+40*u, y, 15, 500, cWhite, a, 0, l)
		ago := int(now.Sub(p.LastSeen).Seconds())
		w.txt(s, fmt.Sprintf(w.tr("%d s ago"), max(0, ago)), st.phoneCard.x+st.phoneCard.w-20*u, y, 13, 400, cDim, a, 2, l)
	}

	// Footer
	for i, line := range w.ts.wrap(w.tr("No encryption or passwords: use Rocket only on a network you trust."), int(st.footer.w-8*u), int(12*u+.5), 400, 2) {
		w.txt(s, line, st.footer.x+4*u, st.footer.y+10*u+float32(i)*18*u+slide, 12, 400, cDim, a, 0, l)
	}

	if w.opts.Version != "" {
		w.txt(s, "Rocket "+w.opts.Version, st.footer.x+4*u, st.footer.y+st.footer.h-12*u+slide, 12, 500, cDim, a, 0, l)
	}

	// Header sits above the scrolled content.
	s.roundRect(0, 0, l.w, st.top-6*u, 0, solid(cBg0), .98*a)
	hb := st.back
	if w.hover == hBack {
		s.circle(hb.x+hb.w/2, hb.y+hb.h/2, 20*u, solid(cWhite), .10*a)
	}
	bx, by := hb.x+hb.w/2, hb.y+hb.h/2
	s.capsule(bx+3*u, by-7*u, bx-4*u, by, 2.2*u, solid(cWhite), a)
	s.capsule(bx-4*u, by, bx+3*u, by+7*u, 2.2*u, solid(cWhite), a)
	w.txt(s, w.tr("Settings"), st.x+58*u, by, 22, 700, cWhite, a, 0, l)
}

func (w *window) upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		switch {
		case r >= 'a' && r <= 'z':
			out[i] = r - 32
		case r >= 'а' && r <= 'я':
			out[i] = r - 32
		case r == 'ё':
			out[i] = 'Ё'
		}
	}
	return string(out)
}

const (
	fwUnknown = iota
	fwPresent
	fwMissing
)
