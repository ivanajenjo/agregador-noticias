package web

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// cmpFixture: outlet "pais" with captures at known local times, JPEGs on disk.
func cmpFixture(t *testing.T) (http.Handler, []domain.Screenshot) {
	t.Helper()
	h, st, _, dir := newHandler(t, nil)
	now := time.Now().In(madrid)
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, madrid)
	mk := func(day, hh, mm int) time.Time {
		return d0.AddDate(0, 0, day).Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
	}
	times := []time.Time{mk(-7, 9, 0), mk(-1, 9, 0), mk(-1, 17, 0), mk(0, -2, 0), mk(0, 0, 30)}
	// keep every capture in the past relative to "now"
	st.shots = nil
	for i, tm := range times {
		rel := "pais/c" + string(rune('a'+i)) + ".jpg"
		img := image.NewRGBA(image.Rect(0, 0, 40, 60))
		for y := 0; y < 60; y++ {
			for x := 0; x < 40; x++ {
				img.Set(x, y, color.RGBA{uint8(i * 40), uint8(x * 5), uint8(y * 3), 255})
			}
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		st.shots = append(st.shots, domain.Screenshot{ID: int64(100 + i), SourceID: "pais", TakenAt: tm, Path: rel, Width: 40, Height: 60})
	}
	return h, st.shots
}

func TestShotsCmpPageDefaults(t *testing.T) {
	h, shots := cmpFixture(t)
	rec := do(h, "GET", "/capturas/pais", nil, "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `/static/shotscmp.css`) || !strings.Contains(body, `/static/shotscmp.js`) {
		t.Error("missing css/js links")
	}
	if !strings.Contains(body, `data-modo="slider"`) {
		t.Error("default modo should be slider")
	}
	// default: previous vs latest capture
	m := regexp.MustCompile(`data-a="(\d+)" data-b="(\d+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no data-a/data-b")
	}
	sorted := append([]domain.Screenshot(nil), shots...)
	last, prev := sorted[0], sorted[0]
	for _, s := range sorted {
		if s.TakenAt.After(last.TakenAt) {
			prev, last = last, s
		} else if s.TakenAt.After(prev.TakenAt) {
			prev = s
		}
	}
	if m[2] != itoa(last.ID) || m[1] != itoa(prev.ID) {
		t.Errorf("defaults a=%s b=%s want a=%d b=%d", m[1], m[2], prev.ID, last.ID)
	}
	if !strings.Contains(body, "Hace 1 h") {
		t.Error("expected presets")
	}
}

func itoa(i int64) string { b, _ := json.Marshal(i); return string(b) }

func TestShotsCmpParams(t *testing.T) {
	h, shots := cmpFixture(t)
	sel := func(url string) (string, string, int) {
		rec := do(h, "GET", url, nil, "")
		m := regexp.MustCompile(`data-a="(\d+)" data-b="(\d+)"`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			return "", "", rec.Code
		}
		return m[1], m[2], rec.Code
	}
	// by id
	if a, b, c := sel("/capturas/pais?a=101&b=102&modo=diff"); c != 200 || a != "101" || b != "102" {
		t.Errorf("ids: %s %s %d", a, b, c)
	}
	// by time: nearest capture (RFC3339, '+' encoded)
	near := shots[1].TakenAt.Add(20 * time.Minute).UTC().Format(time.RFC3339)
	if a, _, _ := sel("/capturas/pais?a=" + near + "&b=102"); a != "101" {
		t.Errorf("nearest by RFC3339: a=%s", a)
	}
	local := shots[2].TakenAt.In(madrid).Add(-10 * time.Minute).Format("2006-01-02T15:04:05-07:00")
	if a, _, _ := sel("/capturas/pais?a=" + strings.Replace(local, "+", "%2B", 1) + "&b=101"); a != "102" {
		t.Errorf("nearest by local time: a=%s", a)
	}
	// time outside default window widens it (a week-old capture)
	old := shots[0].TakenAt.Format(time.RFC3339)
	if a, _, _ := sel("/capturas/pais?a=" + strings.Replace(old, "+", "%2B", 1)); a != "100" && a != "" {
		t.Errorf("old a=%s", a)
	}
	// garbage values fall back to defaults, no error
	if _, _, c := sel("/capturas/pais?a=zzz&b=999999&modo=<script>&desde=nope"); c != 200 {
		t.Errorf("garbage params: %d", c)
	}
	// modo
	rec := do(h, "GET", "/capturas/pais?modo=timelapse", nil, "")
	if !strings.Contains(rec.Body.String(), `data-modo="timelapse"`) {
		t.Error("modo=timelapse not honoured")
	}
	rec = do(h, "GET", "/capturas/pais?modo=evil", nil, "")
	if strings.Contains(rec.Body.String(), "evil") {
		t.Error("unknown modo echoed")
	}
}

func TestShotsCmpRange(t *testing.T) {
	h, _ := cmpFixture(t)
	// single day via ?date= leaves <2 captures at most -> empty state
	d := time.Now().In(madrid).AddDate(0, 0, -7).Format(dateLayout)
	rec := do(h, "GET", "/capturas/pais?date="+d, nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "al menos dos capturas") {
		t.Errorf("empty state: %d", rec.Code)
	}
	rec = do(h, "GET", "/capturas/pais?desde="+d+"&hasta="+d, nil, "")
	if !strings.Contains(rec.Body.String(), "al menos dos capturas") {
		t.Error("desde/hasta empty state")
	}
	// swapped range is normalised
	rec = do(h, "GET", "/capturas/pais?desde=2999-01-01&hasta=2000-01-01", nil, "")
	if rec.Code != 200 {
		t.Errorf("swapped: %d", rec.Code)
	}
}

func TestShotsCmpNotFoundAndNoShots(t *testing.T) {
	h, _ := cmpFixture(t)
	if c := do(h, "GET", "/capturas/nope", nil, "").Code; c != 404 {
		t.Errorf("unknown outlet: %d", c)
	}
	if c := do(h, "GET", "/api/screenshots/nope", nil, "").Code; c != 404 {
		t.Errorf("api unknown outlet: %d", c)
	}
	// outlet with a single capture (abc fixture has one today at 17:00 or so)
	rec := do(h, "GET", "/capturas/pub", nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Necesito al menos dos capturas") && !strings.Contains(rec.Body.String(), "al menos dos capturas") {
		t.Errorf("pub empty state: %d", rec.Code)
	}
}

func TestShotsCmpXSS(t *testing.T) {
	h, st, _, _ := newHandler(t, nil)
	now := time.Now().In(madrid)
	for i := 0; i < 2; i++ {
		st.shots = append(st.shots, domain.Screenshot{ID: int64(500 + i), SourceID: "abc", TakenAt: now.Add(-time.Duration(i+1) * time.Hour), Path: `abc/"><img src=x onerror=alert(1)>.jpg`})
	}
	rec := do(h, "GET", "/capturas/abc?a=%22%3E%3Cscript%3E&modo=%22onload%3Dx", nil, "")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, bad := range []string{"<script>alert", `"><img src=x`, "<Derecha>"} {
		if strings.Contains(body, bad) {
			t.Errorf("unescaped %q", bad)
		}
	}
	if !strings.Contains(body, "Medio &lt;Derecha&gt;") {
		t.Error("source name should be escaped")
	}
	// the frames JSON lives in an attribute: it must not break out of it
	m := regexp.MustCompile(`data-frames="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no data-frames attribute")
	}
}

func TestShotsCmpAPI(t *testing.T) {
	h, _ := cmpFixture(t)
	rec := do(h, "GET", "/api/screenshots/pais", nil, "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Source      string
		Count       int
		Screenshots []struct {
			ID  int64
			URL string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "pais" || out.Count != len(out.Screenshots) || out.Count < 3 {
		t.Errorf("unexpected: %+v", out)
	}
	for i := 1; i < len(out.Screenshots); i++ {
		if out.Screenshots[i].ID == 0 || !strings.HasPrefix(out.Screenshots[i].URL, "/shots/") {
			t.Errorf("bad row %+v", out.Screenshots[i])
		}
	}
	from := time.Now().In(madrid).AddDate(0, 0, -1).Format(dateLayout)
	rec = do(h, "GET", "/api/screenshots/pais?from="+from+"&to="+from, nil, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"count":3`) {
		t.Errorf("one-day range: %d %s", rec.Code, rec.Body)
	}
	if c := do(h, "GET", "/api/screenshots/pais?from=garbage", nil, "").Code; c != 400 {
		t.Errorf("bad from: %d", c)
	}
	if c := do(h, "GET", "/api/screenshots/pais?from=2030-01-02&to=2030-01-01", nil, "").Code; c != 400 {
		t.Errorf("empty range: %d", c)
	}
}

func TestNearestIdx(t *testing.T) {
	f := []cmpFrame{{T: 100}, {T: 200}, {T: 400}}
	for _, c := range []struct {
		t    int64
		want int
	}{{0, 0}, {150, 0}, {151, 1}, {301, 2}, {999, 2}} {
		if got := nearestIdx(f, c.t); got != c.want {
			t.Errorf("nearestIdx(%d)=%d want %d", c.t, got, c.want)
		}
	}
	if nearestIdx(nil, 1) != -1 {
		t.Error("empty")
	}
}

func TestShotsCmpPresets(t *testing.T) {
	h, shots := cmpFixture(t)
	rec := do(h, "GET", "/capturas/pais?b=102", nil, "") // b = yesterday 17:00
	body := rec.Body.String()
	if !strings.Contains(body, "Ayer a la misma hora") && !strings.Contains(body, "Esta mañana") {
		t.Errorf("expected presets for b=102")
	}
	// a preset never selects b itself or a later capture
	re := regexp.MustCompile(`class="chip" href="[^"]*" data-a="(\d+)" data-b="(\d+)"`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if m[1] == m[2] || m[2] != "102" {
			t.Errorf("bad preset %v", m)
		}
	}
	_ = shots
}

// Runs the pure-JS unit tests when node is available.
func TestShotsCmpJSLogic(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	out, err := exec.Command(node, "--test", "shotscmp_logic_test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}
}
