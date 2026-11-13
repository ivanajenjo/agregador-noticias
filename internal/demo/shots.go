package demo

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

const (
	shotW = 1366
	shotH = 2400
)

var (
	paper     = color.RGBA{246, 244, 239, 255}
	ink       = color.RGBA{24, 24, 28, 255}
	greyText  = color.RGBA{112, 112, 118, 255}
	white     = color.RGBA{255, 255, 255, 255}
	mastheads = []color.RGBA{{18, 38, 74, 255}, {160, 28, 38, 255}, {22, 84, 62, 255}, {38, 38, 42, 255}, {112, 24, 62, 255}, {16, 98, 112, 255}, {54, 48, 128, 255}, {164, 112, 22, 255}, {70, 84, 98, 255}, {196, 64, 28, 255}}
	photoPal  = [][2]color.RGBA{
		{{70, 110, 150, 255}, {180, 205, 225, 255}}, {{50, 70, 60, 255}, {150, 175, 140, 255}}, {{120, 70, 50, 255}, {225, 175, 120, 255}},
		{{40, 44, 70, 255}, {120, 130, 175, 255}}, {{90, 90, 96, 255}, {200, 200, 206, 255}}, {{130, 50, 60, 255}, {230, 150, 140, 255}},
		{{30, 90, 100, 255}, {140, 205, 205, 255}}, {{110, 95, 40, 255}, {225, 210, 140, 255}},
	}
	weekdays = []string{"DOMINGO", "LUNES", "MARTES", "MIÉRCOLES", "JUEVES", "VIERNES", "SÁBADO"}
	months   = []string{"", "ENERO", "FEBRERO", "MARZO", "ABRIL", "MAYO", "JUNIO", "JULIO", "AGOSTO", "SEPTIEMBRE", "OCTUBRE", "NOVIEMBRE", "DICIEMBRE"}
)

func fillRect(img *image.RGBA, r image.Rectangle, c color.Color) {
	draw.Draw(img, r.Intersect(img.Bounds()), image.NewUniform(c), image.Point{}, draw.Src)
}

func blend(img *image.RGBA, r image.Rectangle, c color.RGBA, alpha float64) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := img.PixOffset(r.Min.X, y)
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Pix[o] = uint8(float64(img.Pix[o])*(1-alpha) + float64(c.R)*alpha)
			img.Pix[o+1] = uint8(float64(img.Pix[o+1])*(1-alpha) + float64(c.G)*alpha)
			img.Pix[o+2] = uint8(float64(img.Pix[o+2])*(1-alpha) + float64(c.B)*alpha)
			o += 4
		}
	}
}

// photo draws an abstract "photo": gradient + a few shapes, determined by seed.
func photo(img *image.RGBA, r image.Rectangle, seed uint32) {
	pal := photoPal[int(seed)%len(photoPal)]
	h := r.Dy()
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		c := color.RGBA{uint8(float64(pal[0].R)*(1-t) + float64(pal[1].R)*t), uint8(float64(pal[0].G)*(1-t) + float64(pal[1].G)*t), uint8(float64(pal[0].B)*(1-t) + float64(pal[1].B)*t), 255}
		fillRect(img, image.Rect(r.Min.X, r.Min.Y+y, r.Max.X, r.Min.Y+y+1), c)
	}
	s := seed
	rnd := func(n int) int { s = s*1664525 + 1013904223; return int(s>>8) % n }
	dark := color.RGBA{20, 22, 30, 255}
	switch (seed / 7) % 3 {
	case 0: // sun and skyline
		cx, cy, rad := r.Min.X+r.Dx()*(2+rnd(5))/10, r.Min.Y+h/3, h/6
		for y := -rad; y <= rad; y++ {
			dx := int(math.Sqrt(float64(rad*rad - y*y)))
			blend(img, image.Rect(cx-dx, cy+y, cx+dx, cy+y+1), white, .55)
		}
		x := r.Min.X
		for x < r.Max.X {
			w := 20 + rnd(50)
			bh := h/5 + rnd(h/3)
			blend(img, image.Rect(x, r.Max.Y-bh, x+w, r.Max.Y), dark, .7)
			x += w + rnd(8)
		}
	case 1: // diagonal bands
		for i := 0; i < 6; i++ {
			x0 := r.Min.X + rnd(r.Dx())
			for y := 0; y < h; y += 2 {
				blend(img, image.Rect(x0+y/2, r.Min.Y+y, x0+y/2+10+rnd(30), r.Min.Y+y+2), white, .16)
			}
		}
	default: // horizon with water
		blend(img, image.Rect(r.Min.X, r.Min.Y+h*3/5, r.Max.X, r.Max.Y), dark, .45)
		for i := 0; i < 5; i++ {
			y := r.Min.Y + h*3/5 + rnd(h*2/5)
			blend(img, image.Rect(r.Min.X+rnd(r.Dx()/2), y, r.Max.X-rnd(r.Dx()/2), y+2), white, .3)
		}
		blend(img, image.Rect(r.Min.X+r.Dx()/3, r.Min.Y+h/4, r.Min.X+r.Dx()/3+r.Dx()/4, r.Min.Y+h*3/5), dark, .35)
	}
}

// textBlock draws wrapped uppercase text and returns the height used.
func textBlock(img *image.RGBA, x, y, w, scale, maxLines int, s string, c color.Color) int {
	lines := WrapText(s, scale, w, maxLines)
	lh := 8 * scale
	for i, l := range lines {
		DrawText(img, x, y+i*lh+2*scale, scale, l, c)
	}
	return len(lines) * lh
}

func greyBars(img *image.RGBA, x, y, w, n int) {
	for i := 0; i < n; i++ {
		ww := w
		if i == n-1 {
			ww = w * 2 / 3
		}
		fillRect(img, image.Rect(x, y+i*14, x+ww, y+i*14+7), color.RGBA{214, 214, 218, 255})
	}
}

type slot struct {
	kind  string // hero | sec | card | row
	img   image.Rectangle
	txt   image.Rectangle
	scale int
	lines int
	dark  bool // text over a dark band
	red   bool
}

func layoutFor(variant int) (slots []slot, rowsY int) {
	switch variant {
	case 1: // breaking-news banner
		slots = append(slots, slot{kind: "hero", img: image.Rect(40, 386, 1326, 746), txt: image.Rect(40, 254, 1326, 350), scale: 6, lines: 2, red: true, dark: true})
		for k := 0; k < 2; k++ {
			x := 40 + k*666
			slots = append(slots, slot{kind: "sec", img: image.Rect(x, 770, x+620, 1030), txt: image.Rect(x, 1042, x+620, 1170), scale: 4, lines: 4})
		}
		for k := 0; k < 3; k++ {
			x := 40 + k*437
			slots = append(slots, slot{kind: "card", img: image.Rect(x, 1200, x+410, 1370), txt: image.Rect(x, 1382, x+410, 1492), scale: 3, lines: 4})
		}
		return slots, 1540
	case 2: // mosaic with overlay
		slots = append(slots, slot{kind: "hero", img: image.Rect(40, 216, 1326, 736), txt: image.Rect(60, 568, 1306, 724), scale: 6, lines: 3, dark: true})
		for k := 0; k < 4; k++ {
			x := 40 + k*326
			slots = append(slots, slot{kind: "sec", img: image.Rect(x, 760, x+302, 930), txt: image.Rect(x, 942, x+302, 1072), scale: 3, lines: 5})
		}
		for k := 0; k < 2; k++ {
			x := 40 + k*666
			slots = append(slots, slot{kind: "card", img: image.Rect(x, 1100, x+620, 1300), txt: image.Rect(x, 1312, x+620, 1410), scale: 4, lines: 3})
		}
		return slots, 1450
	}
	slots = append(slots, slot{kind: "hero", img: image.Rect(40, 216, 820, 616), txt: image.Rect(40, 628, 820, 770), scale: 5, lines: 3})
	for k := 0; k < 3; k++ {
		y := 216 + k*260
		slots = append(slots, slot{kind: "sec", img: image.Rect(860, y, 1326, y+140), txt: image.Rect(860, y+150, 1326, y+250), scale: 3, lines: 4})
	}
	for k := 0; k < 3; k++ {
		x := 40 + k*437
		slots = append(slots, slot{kind: "card", img: image.Rect(x, 1020, x+410, 1210), txt: image.Rect(x, 1222, x+410, 1332), scale: 3, lines: 4})
	}
	return slots, 1380
}

func hash32(parts ...string) uint32 {
	s := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return binary.BigEndian.Uint32(s[:4])
}

// frontPage renders one capture and returns its JPEG bytes and the items.
func (g *gen) frontPage(src domain.Source, t time.Time, cands []domain.Article) ([]byte, []domain.FrontItem, error) {
	img := image.NewRGBA(image.Rect(0, 0, shotW, shotH))
	fillRect(img, img.Bounds(), paper)
	mc := mastheads[hash32(src.ID)%uint32(len(mastheads))]
	// masthead + nav
	fillRect(img, image.Rect(0, 0, shotW, 150), mc)
	DrawText(img, 40, 46, 8, src.Name, white)
	lt := t.In(g.loc)
	DrawText(img, shotW-40-TextWidth(fmt.Sprintf("%s %d DE %s", weekdays[lt.Weekday()], lt.Day(), months[lt.Month()]), 2), 30, 2,
		fmt.Sprintf("%s %d DE %s", weekdays[lt.Weekday()], lt.Day(), months[lt.Month()]), white)
	DrawText(img, shotW-40-TextWidth(lt.Format("15:04"), 2), 60, 2, lt.Format("15:04"), white)
	fillRect(img, image.Rect(0, 150, shotW, 196), white)
	fillRect(img, image.Rect(0, 196, shotW, 198), color.RGBA{200, 200, 204, 255})
	x := 40
	for _, n := range []string{"PORTADA", "ESPAÑA", "ECONOMÍA", "INTERNACIONAL", "SOCIEDAD", "DEPORTES", "CULTURA", "OPINIÓN"} {
		x = DrawText(img, x, 166, 2, n, ink) + 36
	}

	hero := cands[0]
	variant := int(hash32(src.ID, hero.CanonicalURL) % 3)
	slots, rowsY := layoutFor(variant)

	var items []domain.FrontItem
	dom := 0
	add := func(a domain.Article, rect image.Rectangle, font, prom float64) {
		dom++
		items = append(items, domain.FrontItem{SourceID: src.ID, TakenAt: t.UTC(), DOMOrder: dom, Title: a.Title, URL: a.URL, CanonicalURL: a.CanonicalURL,
			X: float64(rect.Min.X), Y: float64(rect.Min.Y), W: float64(rect.Dx()), H: float64(rect.Dy()), FontPx: font, Prominence: prom, AboveFold: rect.Min.Y < 900})
	}
	idx := 0
	secN, cardN := 0, 0
	for _, s := range slots {
		if idx >= len(cands) {
			break
		}
		a := cands[idx]
		idx++
		seed := hash32(a.CanonicalURL)
		if s.red {
			fillRect(img, image.Rect(0, 216, shotW, 376), color.RGBA{190, 24, 34, 255})
			DrawText(img, 40, 226, 2, "ÚLTIMA HORA", white)
		}
		photo(img, s.img, seed)
		if s.kind == "hero" && variant == 2 {
			blend(img, image.Rect(s.img.Min.X, 556, s.img.Max.X, s.img.Max.Y), color.RGBA{10, 10, 14, 255}, .78)
		}
		c := color.Color(ink)
		if s.dark {
			c = white
		}
		textBlock(img, s.txt.Min.X, s.txt.Min.Y, s.txt.Dx(), s.scale, s.lines, a.Title, c)
		var font, prom float64
		switch s.kind {
		case "hero":
			font, prom = float64(s.scale*8+2), 1.0
			if variant == 0 {
				greyBars(img, 40, 780, 780, 3)
			}
		case "sec":
			secN++
			font, prom = float64(s.scale*8+2), 0.74-0.05*float64(secN-1)
		default:
			cardN++
			font, prom = float64(s.scale*8+2), 0.5-0.04*float64(cardN-1)
		}
		rect := s.txt
		if s.kind == "hero" && s.red {
			rect = image.Rect(40, 254, 1326, 350)
		}
		add(a, rect, font, prom)
	}
	// "more news" list
	fillRect(img, image.Rect(40, rowsY, 1326, rowsY+34), mc)
	DrawText(img, 54, rowsY+9, 2, "MÁS NOTICIAS", white)
	y := rowsY + 48
	row := 0
	for idx < len(cands) && y < shotH-420 {
		a := cands[idx]
		idx++
		fillRect(img, image.Rect(40, y+4, 50, y+14), mc)
		textBlock(img, 66, y, 1180, 2, 1, a.Title, ink)
		DrawText(img, 1326-TextWidth("12:30", 2), y+2, 2, a.PublishedAt.In(g.loc).Format("15:04"), greyText)
		fillRect(img, image.Rect(40, y+30, 1326, y+31), color.RGBA{222, 222, 226, 255})
		add(a, image.Rect(66, y, 1246, y+16), 17, math.Max(0.1, 0.3-0.012*float64(row)))
		row++
		y += 36
	}
	// advert + filler
	y += 10
	fillRect(img, image.Rect(40, y, 1326, y+130), color.RGBA{228, 228, 232, 255})
	DrawText(img, 683-TextWidth("PUBLICIDAD", 3)/2, y+50, 3, "PUBLICIDAD", color.RGBA{170, 170, 176, 255})
	y += 160
	for k := 0; k < 3; k++ {
		for yy := y; yy < shotH-80; yy += 120 {
			greyBars(img, 40+k*437, yy, 410, 5)
		}
	}
	fillRect(img, image.Rect(0, shotH-70, shotW, shotH), mc)

	// ranks by prominence
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Prominence != items[j].Prominence {
			return items[i].Prominence > items[j].Prominence
		}
		return items[i].Y < items[j].Y
	})
	for i := range items {
		items[i].Rank = i + 1
		items[i].Prominence = math.Round(items[i].Prominence*1000) / 1000
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].DOMOrder < items[j].DOMOrder })
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), items, nil
}

// candidates ranks an outlet's articles for a capture time: recent and
// story-backed ones first (with a little deterministic noise).
func (g *gen) candidates(srcID string, t time.Time, capIdx int) []domain.Article {
	type sc struct {
		a domain.Article
		s float64
	}
	var pool []sc
	for _, a := range g.arts {
		if a.SourceID != srcID || a.PublishedAt.After(t) || a.Section == "opinión" {
			continue
		}
		age := t.Sub(a.PublishedAt).Hours()
		if age > 96 {
			continue
		}
		s := 10 * math.Exp(-age/9)
		if st := g.storyOf[a.ID]; st != nil {
			s += math.Min(float64(len(st.Entries)), 10) * 1.3 * math.Exp(-age/16)
		}
		s += float64(hash32(a.CanonicalURL, fmt.Sprint(capIdx))%150) / 100
		pool = append(pool, sc{a, s})
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].s > pool[j].s })
	out := make([]domain.Article, 0, len(pool))
	for _, p := range pool {
		out = append(out, p.a)
	}
	return out
}

func (g *gen) screenshots() error {
	dir := filepath.Join(g.o.Out, "shots")
	days := 7
	if g.o.Days < days {
		days = g.o.Days
	}
	capIdx := 0
	for d := days - 1; d >= 0; d-- {
		for _, hh := range []int{9, 17, 22} {
			capIdx++
			for i, id := range shotOutlets {
				src, ok := g.srcs[id]
				if !ok || !src.IsEnabled() {
					continue
				}
				t := g.day0(d).Add(time.Duration(hh)*time.Hour + time.Duration(i)*time.Minute + time.Duration(i*7%50)*time.Second).UTC()
				if t.After(g.now.Add(-10 * time.Minute)) {
					continue
				}
				cands := g.candidates(id, t, capIdx)
				if len(cands) < 8 {
					continue
				}
				data, items, err := g.frontPage(src, t, cands)
				if err != nil {
					return err
				}
				lt := t.In(g.loc)
				rel := fmt.Sprintf("%s/%04d/%02d/%02d/%02d%02d.jpg", id, lt.Year(), int(lt.Month()), lt.Day(), lt.Hour(), lt.Minute())
				full := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(full, data, 0o644); err != nil {
					return err
				}
				sh := &domain.Screenshot{SourceID: id, TakenAt: t, Path: rel, Width: shotW, Height: shotH, Bytes: int64(len(data))}
				if err := g.st.SaveScreenshot(g.ctx, sh); err != nil {
					return err
				}
				for k := range items {
					items[k].ScreenshotID = sh.ID
				}
				if err := g.st.SaveFrontItems(g.ctx, sh.ID, items); err != nil {
					return err
				}
				fmt.Fprintf(g.h, "S|%s|%d|%x\n", rel, len(data), sha256.Sum256(data))
				g.sum.Screenshots++
				g.sum.FrontItems += len(items)
			}
		}
	}
	g.log("screenshots: %d (%d front items)", g.sum.Screenshots, g.sum.FrontItems)
	return nil
}
