package shot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/ivanajenjo/agregador-noticias/internal/domain"
)

// genericConsentSelectors are accept buttons of common CMPs.
var genericConsentSelectors = []string{
	"#didomi-notice-agree-button",
	"#onetrust-accept-btn-handler",
	"button.fc-cta-consent",
	".fc-consent-root .fc-primary-button",
	"#CybotCookiebotDialogBodyLevelButtonLevelOptinAllowAll",
	"#CybotCookiebotDialogBodyButtonAccept",
	".qc-cmp2-summary-buttons button[mode='primary']",
	"button#accept-all",
	"#truste-consent-button",
	"button[data-testid='uc-accept-all-button']",
	"#cmpwelcomebtnyes a",
	".cmp-accept",
}

// clickFirstJS clicks the first visible element matching one of the selectors.
const clickFirstJS = `(sels => {
  for (const s of sels) {
    let el; try { el = document.querySelector(s); } catch (e) { continue; }
    if (!el) continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) continue;
    el.click(); return s;
  }
  return "";
})(%s)`

// heuristicJS finds an accept-like button inside a consent-looking container.
const heuristicJS = `(() => {
  const txt = /^\s*(aceptar( y cerrar| todo| todas| cookies)?|accept( all| cookies| & close)?|acepto|consentir|de acuerdo|entendido|agree|i agree|ok)\s*[.!]?\s*$/i;
  const looks = /cookie|consent|cmp|gdpr|privacy|privacidad|didomi|onetrust|cookiebot|qc-cmp|banner|policy/i;
  const btns = document.querySelectorAll('button, a[role=button], [role=button], input[type=button], input[type=submit], a');
  for (const b of btns) {
    const t = (b.innerText || b.value || b.getAttribute('aria-label') || '').trim();
    if (!t || t.length > 40 || !txt.test(t)) continue;
    const r = b.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    let p = b, ok = false;
    for (let i = 0; i < 8 && p; i++, p = p.parentElement) {
      const sig = (p.id || '') + ' ' + (typeof p.className === 'string' ? p.className : '') + ' ' + (p.getAttribute && p.getAttribute('role') || '') + ' ' + (p.getAttribute && p.getAttribute('aria-label') || '');
      const pos = getComputedStyle(p).position;
      if (looks.test(sig) || p.tagName === 'DIALOG' || (p.getAttribute && p.getAttribute('aria-modal') === 'true') || (pos === 'fixed' && /cookie|consent|cookies/i.test(p.innerText || '') )) { ok = true; break; }
    }
    if (ok) { b.click(); return (b.innerText || b.value || '').trim(); }
  }
  return "";
})()`

// hideOverlaysJS hides fixed-position overlays / consent containers and
// restores page scrolling (banners often lock body overflow).
const hideOverlaysJS = `(() => {
  const st = document.createElement('style');
  st.textContent = '[id*=cookie i],[class*=cookie i],[id*=consent i],[class*=consent i],[id^=didomi],[id*=onetrust],[class*=cmp-],[id^=sp_message],[class*=qc-cmp],[id*=Cookiebot i],.fc-consent-root{display:none !important}' +
    'html,body{overflow:visible !important;position:static !important;height:auto !important}';
  document.head.appendChild(st);
  let n = 0;
  for (const el of document.body.querySelectorAll('*')) {
    const cs = getComputedStyle(el);
    if (cs.position === 'fixed') {
      const r = el.getBoundingClientRect();
      // hide banners/overlays; keep nothing fixed so full-page capture has no duplicates
      el.style.setProperty('display', 'none', 'important'); n++;
    }
  }
  return n;
})()`

const scrollJS = `(async (maxH) => {
  const step = Math.max(300, Math.floor(window.innerHeight * 0.8));
  let y = 0;
  while (y < Math.min(document.documentElement.scrollHeight, maxH)) {
    window.scrollTo(0, y);
    await new Promise(r => setTimeout(r, 120));
    y += step;
  }
  window.scrollTo(0, document.documentElement.scrollHeight);
  await new Promise(r => setTimeout(r, 250));
  window.scrollTo(0, 0);
  await new Promise(r => setTimeout(r, 250));
  return true;
})(%d)`

func (t *Taker) dismissCookies(ctx context.Context, s domain.Source) {
	try := func(sels []string) string {
		if len(sels) == 0 {
			return ""
		}
		b, _ := json.Marshal(sels)
		var clicked string
		if err := chromedp.Run(ctx, chromedp.Evaluate(sprintf(clickFirstJS, string(b)), &clicked)); err != nil {
			t.log.Debug("cookie selector eval failed", "source", s.ID, "err", err)
		}
		return clicked
	}
	if c := try(s.CookieSelectors); c != "" {
		t.log.Debug("cookie banner dismissed (configured selector)", "source", s.ID, "selector", c)
		_ = chromedp.Run(ctx, chromedp.Sleep(700*time.Millisecond))
		return
	}
	if c := try(genericConsentSelectors); c != "" {
		t.log.Debug("cookie banner dismissed (CMP selector)", "source", s.ID, "selector", c)
		_ = chromedp.Run(ctx, chromedp.Sleep(700*time.Millisecond))
		return
	}
	var txt string
	if err := chromedp.Run(ctx, chromedp.Evaluate(heuristicJS, &txt)); err == nil && txt != "" {
		t.log.Debug("cookie banner dismissed (text heuristic)", "source", s.ID, "button", txt)
		_ = chromedp.Run(ctx, chromedp.Sleep(700*time.Millisecond))
		return
	}
	t.log.Warn("could not dismiss cookie banner (continuing)", "source", s.ID)
}

func (t *Taker) hideOverlays(ctx context.Context) {
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(hideOverlaysJS, &n)); err != nil {
		t.log.Debug("hide overlays failed", "err", err)
	}
}

func (t *Taker) scrollThrough(ctx context.Context) {
	var ok bool
	err := chromedp.Run(ctx, chromedp.Evaluate(sprintf(scrollJS, t.opts.MaxHeight), &ok, awaitPromise))
	if err != nil {
		t.log.Debug("scroll failed", "err", err)
	}
}
