package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// "Capturas a lo largo del tiempo": per-outlet page to compare two front-page
// captures (slider / side by side / pixel diff, computed in the browser) and
// to play the captures as a timelapse. The server only lists metadata and
// resolves the a/b selection; it never decodes images.

const (
	cmpDefaultDays = 7
	cmpMaxDays     = 92
	cmpMaxFrames   = 400
	cmpAPIMax      = 1000
)

var cmpModes = map[string]bool{"slider": true, "lado": true, "diff": true, "timelapse": true}

// cmpFrame is one capture as the browser needs it (compact JSON keys).
type cmpFrame struct {
	ID int64  `json:"id"`
	T  int64  `json:"t"` // unix seconds
	U  string `json:"u"`
	W  int    `json:"w,omitempty"`
	H  int    `json:"h,omitempty"`
	L  string `json:"l"` // "jue 12/11 09:00"
	D  string `json:"d"` // local day, YYYY-MM-DD
}

// cmpThumb is a frame in the time strip; Href works without JavaScript
// (keeps a, sets b to this capture).
type cmpThumb struct {
	cmpFrame
	Idx        int
	Time       string
	Href       string
	SelA, SelB bool
}

type cmpDay struct {
	Key, Label string
	Frames     []cmpThumb
}

type cmpPreset struct {
	Label, Href string
	A, B        int64
}

type cmpOpt struct {
	cmpFrame
	SelA, SelB bool
}

type cmpView struct {
	Source           domain.Source
	SourceURL        string
	Modo             string
	Desde, Hasta     string
	Today            string
	Total            int
	Empty            bool
	Truncated        bool
	FramesJSON       string
	Opts             []cmpOpt
	Version          string
	Days             []cmpDay
	A, B             *cmpFrame
	Presets          []cmpPreset
	TabHref          map[string]string
	SwapHref         string
	AIdx, BIdx       int
	SameFrame        bool
	RangeDays        int
	ShotsPageHref    string
	PrevDay, NextDay string
}

func cmpRegister() {
	registerPage("shotscmp")
	registerRoutes(func(s *server, m *http.ServeMux) {
		m.HandleFunc("GET /capturas/{source}", s.pageShotsCmp)
		m.HandleFunc("GET /api/screenshots/{source}", s.apiShotsSource)
	})
}

func init() { cmpRegister() }

// findSource returns the configured source with that id.
func (s *server) findSource(r *http.Request, id string) (domain.Source, bool, error) {
	src, err := s.d.Store.ListSources(r.Context())
	if err != nil {
		return domain.Source{}, false, err
	}
	for _, so := range src {
		if so.ID == id {
			return so, true, nil
		}
	}
	return domain.Source{}, false, nil
}

// parseCmpTime accepts RFC3339 (a "+" decoded as a space is repaired), a local
// "2006-01-02T15:04[:05]" or a plain date (midnight).
func (s *server) parseCmpTime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 40 {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, strings.Replace(v, " ", "+", 1)); err == nil {
		return t, true
	}
	for _, l := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", dateLayout} {
		if t, err := time.ParseInLocation(l, v, s.loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func allDigits(v string) bool {
	if v == "" || len(v) > 18 {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// nearestIdx returns the index of the frame closest to t (frames sorted by T).
func nearestIdx(frames []cmpFrame, t int64) int {
	if len(frames) == 0 {
		return -1
	}
	i := sort.Search(len(frames), func(i int) bool { return frames[i].T >= t })
	switch {
	case i == 0:
		return 0
	case i == len(frames):
		return len(frames) - 1
	}
	if t-frames[i-1].T <= frames[i].T-t {
		return i - 1
	}
	return i
}

// resolveFrame maps an `a`/`b` parameter (screenshot id or time) to a frame index.
func (s *server) resolveFrame(frames []cmpFrame, v string) int {
	v = strings.TrimSpace(v)
	if v == "" || len(frames) == 0 {
		return -1
	}
	if allDigits(v) {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return -1
		}
		for i := range frames {
			if frames[i].ID == id {
				return i
			}
		}
		return -1
	}
	if t, ok := s.parseCmpTime(v); ok {
		return nearestIdx(frames, t.Unix())
	}
	return -1
}

func (s *server) dayStart(t time.Time) time.Time {
	t = t.In(s.loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.loc)
}

func (s *server) cmpFrameOf(sh domain.Screenshot) cmpFrame {
	t := sh.TakenAt.In(s.loc)
	return cmpFrame{ID: sh.ID, T: sh.TakenAt.Unix(), U: shotURL(sh.Path), W: sh.Width, H: sh.Height,
		L: diasES[t.Weekday()][:3] + " " + t.Format("02/01 15:04"), D: t.Format(dateLayout)}
}

// cmpRange resolves desde/hasta (inclusive days) with the /capturas ?date= convention
// as a shorthand for a single day. Returns [from,to) instants and the day strings.
func (s *server) cmpRange(q url.Values, extra ...time.Time) (from, to time.Time, desde, hasta string) {
	today := s.dayStart(s.now())
	parse := func(v string) (time.Time, bool) {
		t, err := time.ParseInLocation(dateLayout, v, s.loc)
		return t, err == nil
	}
	d0, ok0 := parse(q.Get("desde"))
	d1, ok1 := parse(q.Get("hasta"))
	if dd, ok := parse(q.Get("date")); ok && !ok0 && !ok1 {
		d0, d1, ok0, ok1 = dd, dd, true, true
	}
	explicit := ok0 || ok1
	switch {
	case ok0 && !ok1:
		d1 = today
	case ok1 && !ok0:
		d0 = d1.AddDate(0, 0, -(cmpDefaultDays - 1))
	case !ok0 && !ok1:
		d1 = today
		d0 = today.AddDate(0, 0, -(cmpDefaultDays - 1))
	}
	if d1.Before(d0) {
		d0, d1 = d1, d0
	}
	if !explicit { // a time given in a/b widens the default window
		for _, t := range extra {
			if d := s.dayStart(t); d.Before(d0) {
				d0 = d
			}
			if d := s.dayStart(t); d.After(d1) && !d.After(today) {
				d1 = d
			}
		}
	}
	if d1.After(today) && d0.After(today) {
		d0, d1 = today, today
	}
	if d1.Sub(d0) > time.Duration(cmpMaxDays-1)*24*time.Hour+time.Hour {
		d0 = d1.AddDate(0, 0, -(cmpMaxDays - 1))
	}
	return d0, d1.AddDate(0, 0, 1), d0.Format(dateLayout), d1.Format(dateLayout)
}

func (s *server) loadFrames(r *http.Request, id string, from, to time.Time) ([]cmpFrame, bool, error) {
	shots, err := s.d.Store.ListScreenshots(r.Context(), id, from, to)
	if err != nil {
		return nil, false, err
	}
	sort.Slice(shots, func(i, j int) bool { return shots[i].TakenAt.Before(shots[j].TakenAt) })
	trunc := false
	if len(shots) > cmpMaxFrames {
		shots, trunc = shots[len(shots)-cmpMaxFrames:], true
	}
	out := make([]cmpFrame, len(shots))
	for i, sh := range shots {
		out[i] = s.cmpFrameOf(sh)
	}
	return out, trunc, nil
}

func (s *server) pageShotsCmp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("source")
	so, ok, err := s.findSource(r, id)
	if err != nil {
		s.log.Error("shotscmp: sources", "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer la lista de medios.")
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	q := r.URL.Query()
	var extra []time.Time
	for _, k := range []string{"a", "b"} {
		if v := q.Get(k); v != "" && !allDigits(v) {
			if t, ok := s.parseCmpTime(v); ok {
				extra = append(extra, t)
			}
		}
	}
	from, to, desde, hasta := s.cmpRange(q, extra...)
	frames, trunc, err := s.loadFrames(r, id, from, to)
	if err != nil {
		s.log.Error("shotscmp: list", "source", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "No se pudieron leer las capturas.")
		return
	}
	today := s.dayStart(s.now())
	v := cmpView{
		Source: so, SourceURL: "/capturas/" + url.PathEscape(id), Modo: "slider",
		Desde: desde, Hasta: hasta, Today: today.Format(dateLayout), Total: len(frames),
		Truncated: trunc, Version: s.o.Version, AIdx: -1, BIdx: -1,
		ShotsPageHref: "/capturas?date=" + url.QueryEscape(hasta),
		RangeDays:     int(to.Sub(from).Round(24*time.Hour) / (24 * time.Hour)),
	}
	if m := q.Get("modo"); cmpModes[m] {
		v.Modo = m
	}
	if len(frames) < 2 {
		v.Empty = true
		s.render(w, r, http.StatusOK, "shotscmp", so.Name+" · capturas", "shots", v)
		return
	}
	ai, bi := s.resolveFrame(frames, q.Get("a")), s.resolveFrame(frames, q.Get("b"))
	last := len(frames) - 1
	switch {
	case ai < 0 && bi < 0:
		ai, bi = last-1, last
	case bi < 0:
		bi = last
	case ai < 0:
		if bi > 0 {
			ai = bi - 1
		} else {
			ai = 1
		}
	}
	v.AIdx, v.BIdx = ai, bi
	v.A, v.B = &frames[ai], &frames[bi]
	v.SameFrame = ai == bi
	jb, _ := json.Marshal(frames)
	v.FramesJSON = string(jb)
	for i, f := range frames {
		v.Opts = append(v.Opts, cmpOpt{cmpFrame: f, SelA: i == ai, SelB: i == bi})
	}
	v.Days = groupDays(s, frames, v)
	v.Presets = s.cmpPresets(frames, bi, v)
	v.TabHref = map[string]string{}
	for m := range cmpModes {
		v.TabHref[m] = s.cmpHref(v, m, v.A.ID, v.B.ID)
	}
	v.SwapHref = s.cmpHref(v, v.Modo, v.B.ID, v.A.ID)
	s.render(w, r, http.StatusOK, "shotscmp", so.Name+" · capturas", "shots", v)
}

func (s *server) cmpHref(v cmpView, modo string, a, b int64) string {
	p := url.Values{}
	p.Set("modo", modo)
	p.Set("a", strconv.FormatInt(a, 10))
	p.Set("b", strconv.FormatInt(b, 10))
	p.Set("desde", v.Desde)
	p.Set("hasta", v.Hasta)
	return v.SourceURL + "?" + p.Encode()
}

func groupDays(s *server, frames []cmpFrame, v cmpView) []cmpDay {
	var out []cmpDay
	for i, f := range frames {
		if n := len(out); n == 0 || out[n-1].Key != f.D {
			t, _ := time.ParseInLocation(dateLayout, f.D, s.loc)
			out = append(out, cmpDay{Key: f.D, Label: s.longDate(t)})
		}
		out[len(out)-1].Frames = append(out[len(out)-1].Frames, cmpThumb{cmpFrame: f, Idx: i,
			Time: time.Unix(f.T, 0).In(s.loc).Format("15:04"), Href: s.cmpHref(v, v.Modo, v.A.ID, f.ID),
			SelA: i == v.AIdx, SelB: i == v.BIdx})
	}
	return out
}

// cmpPresets builds the quick comparisons relative to frame b (usually the
// latest capture). Presets that would pick b itself are left out.
func (s *server) cmpPresets(frames []cmpFrame, bi int, v cmpView) []cmpPreset {
	b := frames[bi]
	bt := time.Unix(b.T, 0).In(s.loc)
	morning := time.Date(bt.Year(), bt.Month(), bt.Day(), 8, 0, 0, 0, s.loc)
	yday := bt.AddDate(0, 0, -1)
	defs := []struct {
		label string
		t     time.Time
		tol   time.Duration // how far the nearest capture may be from the target
	}{
		{"Hace 1 h", bt.Add(-time.Hour), 6 * time.Hour},
		{"Esta mañana", morning, 5 * time.Hour},
		{"Ayer a la misma hora", yday, 6 * time.Hour},
		{"Hace 1 semana", bt.AddDate(0, 0, -7), 12 * time.Hour},
	}
	var out []cmpPreset
	seen := map[int64]bool{}
	for _, d := range defs {
		if d.t.After(bt) {
			continue
		}
		i := nearestIdx(frames, d.t.Unix())
		if i < 0 || i == bi || frames[i].T >= b.T || seen[frames[i].ID] {
			continue
		}
		// A preset is only meaningful when a capture exists near its target.
		if absDur(frames[i].T-d.t.Unix()) > int64(d.tol.Seconds()) {
			continue
		}
		seen[frames[i].ID] = true
		out = append(out, cmpPreset{Label: d.label, A: frames[i].ID, B: b.ID,
			Href: s.cmpHref(v, v.Modo, frames[i].ID, b.ID)})
	}
	return out
}

func absDur(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// ---- JSON API ----

type shotsSourceJSON struct {
	Source      string     `json:"source"`
	From        time.Time  `json:"from"`
	To          time.Time  `json:"to"`
	Count       int        `json:"count"`
	Truncated   bool       `json:"truncated,omitempty"`
	Screenshots []shotJSON `json:"screenshots"`
}

// apiShotsSource: GET /api/screenshots/{source}?from=&to= (dates or RFC3339;
// default: last 7 days; `to` as a bare date is inclusive).
func (s *server) apiShotsSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("source")
	_, ok, err := s.findSource(r, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Error al listar medios")
		return
	}
	if !ok {
		s.fail(w, r, http.StatusNotFound, "Medio desconocido")
		return
	}
	q := r.URL.Query()
	now := s.now()
	to := now.Add(time.Minute)
	from := s.dayStart(now).AddDate(0, 0, -(cmpDefaultDays - 1))
	if v := q.Get("from"); v != "" {
		t, ok := s.parseCmpTime(v)
		if !ok {
			s.fail(w, r, http.StatusBadRequest, "Parámetro from no válido (usa AAAA-MM-DD o RFC3339)")
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, ok := s.parseCmpTime(v)
		if !ok {
			s.fail(w, r, http.StatusBadRequest, "Parámetro to no válido (usa AAAA-MM-DD o RFC3339)")
			return
		}
		if _, err := time.ParseInLocation(dateLayout, v, s.loc); err == nil {
			t = t.AddDate(0, 0, 1)
		}
		to = t
	}
	if !to.After(from) {
		s.fail(w, r, http.StatusBadRequest, "El rango está vacío")
		return
	}
	if to.Sub(from) > cmpMaxDays*24*time.Hour {
		from = to.Add(-cmpMaxDays * 24 * time.Hour)
	}
	shots, err := s.d.Store.ListScreenshots(r.Context(), id, from, to)
	if err != nil {
		s.log.Warn("api shots source", "source", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, "Error al leer capturas")
		return
	}
	sort.Slice(shots, func(i, j int) bool { return shots[i].TakenAt.Before(shots[j].TakenAt) })
	out := shotsSourceJSON{Source: id, From: from, To: to, Screenshots: []shotJSON{}}
	if len(shots) > cmpAPIMax {
		shots, out.Truncated = shots[len(shots)-cmpAPIMax:], true
	}
	for _, sh := range shots {
		out.Screenshots = append(out.Screenshots, shotJSON{sh.ID, sh.SourceID, sh.TakenAt, shotURL(sh.Path), sh.Width, sh.Height, sh.Bytes})
	}
	out.Count = len(out.Screenshots)
	writeJSON(w, http.StatusOK, out)
}
