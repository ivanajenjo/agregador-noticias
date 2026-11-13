/* Portadas: overlay "mostrar titulares detectados" en el visor de capturas.
   Se engancha al visor de app.js sin modificarlo; si la API no devuelve titulares
   (sin datos o sección desactivada) no aparece ningún botón. Sin dependencias. */
(function () {
  "use strict";
  var viewer, stage, img, bar, btn, layer, info, data = null, key = "", on = false, cache = {}, css = false, down = null;
  try { on = localStorage.getItem("fp-overlay") === "1"; } catch (e) { /* storage unavailable */ }

  function loadCSS() {
    if (css) return;
    css = true;
    if (document.querySelector('link[href^="/static/frontpage.css"]')) return;
    var l = document.createElement("link");
    l.rel = "stylesheet"; l.href = "/static/frontpage.css";
    document.head.appendChild(l);
  }

  function parseSrc(src) {
    var u;
    try { u = new URL(src, location.href); } catch (e) { return null; }
    var m = /^\/shots\/(.+)$/.exec(u.pathname);
    if (!m) return null;
    var p;
    try { p = decodeURIComponent(m[1]); } catch (e) { return null; }
    return { source: p.split("/")[0], path: p };
  }

  function safeHref(u) {
    try { var x = new URL(u); return (x.protocol === "http:" || x.protocol === "https:") ? x.href : ""; } catch (e) { return ""; }
  }

  function clearLayer() {
    if (layer) { layer.remove(); layer = null; }
    if (info) { info.remove(); info = null; }
  }

  function refresh() {
    var p = img && img.getAttribute("src") ? parseSrc(img.getAttribute("src")) : null;
    var k = p ? p.source + "|" + p.path : "";
    if (k === key) return;
    key = k; data = null;
    clearLayer();
    if (btn) btn.hidden = true;
    if (!p) return;
    var ready = function (d) {
      if (key !== k) return; // the viewer moved on
      data = d;
      if (!d || !d.items || !d.items.length) return;
      loadCSS();
      ensureButton();
      btn.hidden = false;
      btn.textContent = "Titulares (" + d.items.length + ")";
      btn.setAttribute("aria-pressed", on ? "true" : "false");
      if (on) render();
    };
    if (k in cache) { ready(cache[k]); return; }
    fetch("/api/frontpage?source=" + encodeURIComponent(p.source) + "&path=" + encodeURIComponent(p.path), { headers: { Accept: "application/json" } })
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; })
      .then(function (d) { cache[k] = d; ready(d); });
  }

  function ensureButton() {
    if (btn) return;
    btn = document.createElement("button");
    btn.type = "button";
    btn.className = "fp-toggle";
    btn.title = "Mostrar titulares detectados";
    btn.setAttribute("aria-label", "Mostrar titulares detectados");
    btn.addEventListener("click", function () {
      on = !on;
      try { localStorage.setItem("fp-overlay", on ? "1" : "0"); } catch (e) { /* ignore */ }
      btn.setAttribute("aria-pressed", on ? "true" : "false");
      if (on) render(); else clearLayer();
    });
    var close = bar.querySelector('[data-v="close"]');
    bar.insertBefore(btn, close);
  }

  function syncLayer() {
    if (!layer || !img) return;
    layer.style.height = img.offsetHeight + "px";
    layer.style.transform = img.style.transform || "";
  }

  function render() {
    clearLayer();
    if (!data || !data.items) return;
    var W = data.width || img.naturalWidth, H = data.height || img.naturalHeight;
    if (!W || !H) { img.addEventListener("load", function f() { img.removeEventListener("load", f); if (on) render(); }); return; }
    layer = document.createElement("div");
    layer.className = "fp-layer";
    layer.setAttribute("aria-hidden", "true");
    data.items.forEach(function (it, i) {
      if (!(it.w > 0 && it.h > 0) || it.y >= H) return;
      var b = document.createElement("div");
      b.className = "fp-box" + (it.rank === 1 ? " lead" : "");
      b.style.left = (it.x / W * 100) + "%";
      b.style.top = (it.y / H * 100) + "%";
      b.style.width = (it.w / W * 100) + "%";
      b.style.height = (it.h / H * 100) + "%";
      b.dataset.i = String(i);
      if (it.rank <= 10) { var s = document.createElement("span"); s.textContent = "#" + it.rank; b.appendChild(s); }
      layer.appendChild(b);
    });
    stage.appendChild(layer);
    syncLayer();
  }

  function showInfo(i) {
    var it = data && data.items[i];
    if (!it || !layer) return;
    if (info) info.remove();
    layer.querySelectorAll(".sel").forEach(function (e) { e.classList.remove("sel"); });
    var box = layer.querySelector('[data-i="' + i + '"]');
    if (box) box.classList.add("sel");
    info = document.createElement("div");
    info.className = "fp-info";
    // keep the stage's pan/zoom (pointer capture) away from the panel so its buttons and links work
    info.addEventListener("pointerdown", function (e) { e.stopPropagation(); });
    var x = document.createElement("button");
    x.type = "button"; x.className = "fp-x"; x.setAttribute("aria-label", "Cerrar detalle"); x.textContent = "✕";
    x.addEventListener("click", function (e) { e.stopPropagation(); info.remove(); info = null; if (box) box.classList.remove("sel"); });
    var t = document.createElement("p");
    t.className = "fp-it"; t.textContent = it.title;
    var meta = document.createElement("div");
    meta.className = "fp-ib";
    var m = document.createElement("span");
    m.textContent = "#" + it.rank + " · prominencia " + Math.round(it.prominence * 100) + "/100 · " + Math.round(it.font_px) + " px" + (it.above_fold ? " · sobre el pliegue" : "");
    meta.appendChild(m);
    var href = safeHref(it.url);
    if (href) {
      var a = document.createElement("a");
      a.href = href; a.target = "_blank"; a.rel = "noopener noreferrer"; a.textContent = "Abrir noticia";
      meta.appendChild(a);
    }
    if (it.canonical_url) {
      var h = document.createElement("a");
      h.href = "/portadas?item=" + encodeURIComponent(it.canonical_url) + "#evolucion"; h.textContent = "Ver evolución";
      meta.appendChild(h);
    }
    info.appendChild(x); info.appendChild(t); info.appendChild(meta);
    stage.appendChild(info);
  }

  function setup(v) {
    viewer = v;
    stage = v.querySelector(".vstage");
    img = stage && stage.querySelector("img");
    bar = v.querySelector(".vbar");
    if (!stage || !img || !bar) return;
    new MutationObserver(refresh).observe(img, { attributes: true, attributeFilter: ["src"] });
    new MutationObserver(syncLayer).observe(img, { attributes: true, attributeFilter: ["style"] });
    new MutationObserver(function () { if (!viewer.classList.contains("open")) { key = ""; clearLayer(); } else refresh(); }).observe(v, { attributes: true, attributeFilter: ["class"] });
    img.addEventListener("load", syncLayer);
    window.addEventListener("resize", syncLayer);
    stage.addEventListener("pointerdown", function (e) { down = { x: e.clientX, y: e.clientY }; }, true);
    // The stage captures the pointer (pan/zoom), so clicks never reach the boxes: hit-test by hand.
    stage.addEventListener("click", function (e) {
      if (!layer || (down && Math.abs(e.clientX - down.x) + Math.abs(e.clientY - down.y) > 8)) return;
      if (e.target.closest && e.target.closest(".fp-info")) return;
      var els = document.elementsFromPoint(e.clientX, e.clientY), best = null;
      for (var j = 0; j < els.length; j++) {
        if (els[j].classList && els[j].classList.contains("fp-box")) { best = els[j]; break; } // topmost first
      }
      if (best) showInfo(parseInt(best.dataset.i, 10));
    });
    refresh();
  }

  function init() {
    var v = document.querySelector(".viewer");
    if (v) { setup(v); return; }
    var mo = new MutationObserver(function () {
      var w = document.querySelector(".viewer");
      if (w) { mo.disconnect(); setup(w); }
    });
    mo.observe(document.body, { childList: true });
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init); else init();
})();
