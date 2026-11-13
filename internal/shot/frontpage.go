package shot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chromedp/chromedp"
	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// Front-page prominence ("qué abre cada medio"). While the page is still open
// in the browser, ONE page.evaluate collects the visible headlines (anchors
// inside/containing headings or title-like classes) with their geometry; Go
// then canonicalises, dedupes, scores and ranks them (see domain.FrontItem for
// the formula). Any failure here is logged and ignored: the screenshot is
// always saved.

const (
	maxFrontItems  = 40
	minTitleRunes  = 25
	maxTitleRunes  = 300
	frontEvalLimit = 15 * time.Second
)

// extractJS returns a JSON string {vw,vh,h,items:[{t,u,x,y,w,h,f,o}]}.
// It runs after the overlays were hidden and the page was scrolled through, so
// the geometry equals the screenshot's. Excluded: nav/footer/dialog/cookie/ad
// containers (by tag, role and id/class tokens), fixed/sticky elements, hidden
// or zero-size boxes, boxes outside the page and short texts.
const extractJS = `((maxH) => {
  try {
    const HEAD = 'h1,h2,h3,[class*=title i],[class*=headline i],[class*=titular i]';
    const NOISE = /(^|[\s_-])(nav|navbar|navigation|menu|footer|cookie|cookies|consent|cmp|gdpr|didomi|onetrust|ad|ads|adsbygoogle|advert|advertisement|publi|publicidad|sponsor|sponsored|banner|newsletter|subscribe|suscripcion|breadcrumb|breadcrumbs|share|social|modal|popup|login|taboola|outbrain)([\s_-]|$)/i;
    const NOISE_TAG = {NAV:1, FOOTER:1, DIALOG:1, FORM:1, SCRIPT:1, STYLE:1, NOSCRIPT:1, TEMPLATE:1, IFRAME:1};
    const NOISE_ROLE = /^(navigation|contentinfo|dialog|alertdialog|banner|search|menu|menubar)$/i;
    const noisy = (el) => {
      let p = el;
      for (let i = 0; p && p !== document.body && i < 12; i++, p = p.parentElement) {
        if (NOISE_TAG[p.tagName]) return true;
        const role = p.getAttribute('role');
        if (role && NOISE_ROLE.test(role)) return true;
        if (p.getAttribute('aria-hidden') === 'true' || p.getAttribute('aria-modal') === 'true' || p.hidden) return true;
        const sig = (p.id || '') + ' ' + (typeof p.className === 'string' ? p.className : '');
        if (sig.trim() && NOISE.test(sig)) return true;
        const cs = getComputedStyle(p);
        if (cs.position === 'fixed' || cs.position === 'sticky') return true;
        if (cs.display === 'none' || cs.visibility === 'hidden' || parseFloat(cs.opacity) === 0) return true;
      }
      return false;
    };
    const clean = (s) => (s || '').replace(/\s+/g, ' ').trim();
    const W = Math.max(document.documentElement.scrollWidth, window.innerWidth);
    const out = [];
    const anchors = document.querySelectorAll('a[href]');
    let order = 0;
    for (let k = 0; k < anchors.length && k < 4000; k++) {
      const a = anchors[k];
      let he = null, txt = '', box = null, fe = a;
      const inner = a.querySelector(HEAD);
      const outer = a.closest(HEAD);
      if (inner) { he = inner; txt = clean(inner.innerText || inner.textContent); box = inner.getBoundingClientRect(); fe = inner; }
      else if (outer) {
        he = outer; txt = clean(a.innerText || a.textContent); box = a.getBoundingClientRect();
        if (txt.length < 25 && outer.querySelectorAll('a[href]').length === 1) { txt = clean(outer.innerText || outer.textContent); box = outer.getBoundingClientRect(); fe = outer; }
      }
      if (!he) continue;
      const n = Array.from(txt).length;
      if (n < 25 || n > 300 || txt.split(' ').length < 3) continue;
      if (!box || box.width < 60 || box.height < 12) continue;
      const x = box.left + window.scrollX, y = box.top + window.scrollY;
      if (y >= maxH || x >= W || x + box.width <= 0 || y + box.height <= 0) continue;
      if (noisy(a)) continue;
      const fs = parseFloat(getComputedStyle(fe).fontSize) || 0;
      out.push({t: txt, u: a.href, x: x, y: y, w: box.width, h: box.height, f: fs, o: ++order});
    }
    return JSON.stringify({vw: window.innerWidth, vh: window.innerHeight, h: document.documentElement.scrollHeight, items: out});
  } catch (e) { return JSON.stringify({error: String(e)}); }
})(%d)`

type rawItem struct {
	T string  `json:"t"`
	U string  `json:"u"`
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
	F float64 `json:"f"`
	O int     `json:"o"`
}

type rawPage struct {
	VW    float64   `json:"vw"`
	VH    float64   `json:"vh"`
	H     float64   `json:"h"`
	Items []rawItem `json:"items"`
	Error string    `json:"error"`
}

// extractFrontItems runs extractJS in the current page.
func (t *Taker) extractFrontItems(ctx context.Context, homepage string) ([]domain.FrontItem, error) {
	ectx, cancel := context.WithTimeout(ctx, frontEvalLimit)
	defer cancel()
	js, err := chromedp.Run(ectx, chromedp.Evaluate[string](fmt.Sprintf(extractJS, t.opts.MaxHeight)))
	if err != nil {
		return nil, err
	}
	return parseFrontItems(js, homepage)
}

func parseFrontItems(js, homepage string) ([]domain.FrontItem, error) {
	var rp rawPage
	if err := json.Unmarshal([]byte(js), &rp); err != nil {
		return nil, err
	}
	if rp.Error != "" {
		return nil, errors.New(rp.Error)
	}
	return buildFrontItems(rp, homepage), nil
}

// buildFrontItems canonicalises URLs, drops other-site links, dedupes by
// canonical URL (keeping the most prominent), scores and ranks (cap 40).
//
// Score (see domain.FrontItem): raw = .40*font/maxFont + .30*sqrt(area/maxArea)
// + .20*(1 - min(y,4*vh)/(4*vh)) + .10*[y < vh]; prominence = raw/max(raw).
func buildFrontItems(rp rawPage, homepage string) []domain.FrontItem {
	vh := rp.VH
	if vh <= 0 {
		vh = 900
	}
	type cand struct {
		it  domain.FrontItem
		raw float64
	}
	var cs []cand
	var maxFont, maxArea float64
	for _, r := range rp.Items {
		canon, err := canonicalURL(r.U, homepage)
		if err != nil || !sameSite(canon, homepage) || samePage(canon, homepage) {
			continue
		}
		title := strings.Join(strings.Fields(r.T), " ")
		if n := utf8.RuneCountInString(title); n < minTitleRunes || n > maxTitleRunes {
			continue
		}
		cs = append(cs, cand{it: domain.FrontItem{
			DOMOrder: r.O, Title: title, URL: r.U, CanonicalURL: canon,
			X: r.X, Y: r.Y, W: r.W, H: r.H, FontPx: r.F, AboveFold: r.Y < vh,
		}})
		maxFont = math.Max(maxFont, r.F)
		maxArea = math.Max(maxArea, r.W*r.H)
	}
	for i := range cs {
		it := &cs[i].it
		f, a := 0.0, 0.0
		if maxFont > 0 {
			f = it.FontPx / maxFont
		}
		if maxArea > 0 {
			a = math.Sqrt(it.W * it.H / maxArea)
		}
		pos := 1 - math.Min(math.Max(it.Y, 0), 4*vh)/(4*vh)
		fold := 0.0
		if it.AboveFold {
			fold = 1
		}
		cs[i].raw = 0.40*f + 0.30*a + 0.20*pos + 0.10*fold
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].raw != cs[j].raw {
			return cs[i].raw > cs[j].raw
		}
		return cs[i].it.DOMOrder < cs[j].it.DOMOrder
	})
	seen := map[string]bool{}
	var out []domain.FrontItem
	var top float64
	for _, c := range cs {
		if seen[c.it.CanonicalURL] {
			continue
		}
		seen[c.it.CanonicalURL] = true
		if top == 0 {
			top = c.raw
		}
		it := c.it
		if top > 0 {
			it.Prominence = math.Round(c.raw/top*1000) / 1000
		}
		it.Rank = len(out) + 1
		out = append(out, it)
		if len(out) >= maxFrontItems {
			break
		}
	}
	return out
}

// ---- URL helpers (local copy of the ingest normalisation: shot must not import ingest) ----

var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "yclid": true, "mc_cid": true, "mc_eid": true, "igshid": true,
	"ocid": true, "cmpid": true, "xtor": true, "ito": true, "s_kwcid": true,
	"_ga": true, "_gl": true, "ref_src": true, "mkt_tok": true, "wt_mc": true,
	"at_medium": true, "at_campaign": true, "at_custom1": true, "at_custom2": true,
	"at_custom3": true, "at_custom4": true, "outputType": true, "ns_source": true,
	"ns_mchannel": true, "ns_campaign": true, "ns_linkname": true, "ns_fee": true,
	"intcmp": true, "int_cmp": true, "rss": true, "ssm": true, "sref": true,
}

// canonicalURL mirrors ingest.CanonicalURL: https scheme, lowercase host
// without default port, no fragment, no tracking parameters, sorted query, no
// trailing slash.
func canonicalURL(raw, base string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if base != "" {
		if b, err := url.Parse(base); err == nil {
			u = b.ResolveReference(u)
		}
	}
	if s := strings.ToLower(u.Scheme); s != "http" && s != "https" {
		return "", errors.New("unsupported scheme: " + u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("missing host")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "80" || port == "443" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || trackingParams[lk] {
			q.Del(k)
		}
	}
	out := url.URL{Scheme: "https", Host: host, Path: u.EscapedPath()}
	if p, err := url.PathUnescape(out.Path); err == nil {
		out.Path = p
		out.RawPath = u.EscapedPath()
	}
	if len(out.Path) > 1 {
		out.Path = strings.TrimRight(out.Path, "/")
		out.RawPath = strings.TrimRight(out.RawPath, "/")
	} else {
		out.Path, out.RawPath = "", ""
	}
	if len(q) > 0 {
		out.RawQuery = q.Encode()
	}
	return out.String(), nil
}

// siteKey is the last two host labels (three for co.uk-like suffixes); IPs and
// single-label hosts are returned whole.
func siteKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	parts := strings.Split(h, ".")
	if len(parts) <= 2 || strings.Trim(h, "0123456789.") == "" {
		return h
	}
	n := 2
	if l := parts[len(parts)-2]; len(parts[len(parts)-1]) == 2 && (l == "co" || l == "com" || l == "org" || l == "gob") {
		n = 3
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

func sameSite(a, b string) bool { return siteKey(a) != "" && siteKey(a) == siteKey(b) }

// samePage reports links that point at the homepage itself (logo, "inicio").
func samePage(canon, homepage string) bool {
	h, err := canonicalURL(homepage, "")
	return err == nil && h == canon
}
