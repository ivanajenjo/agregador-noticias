/* Capture comparator + timelapse (page /capturas/{medio}). Depends on shotscmp-logic.js. No inline code (CSP). */
(function () {
  "use strict";
  var L = window.CmpLogic, root = document.getElementById("cmp");
  if (!root || !L) return;
  var frames;
  try { frames = JSON.parse(root.getAttribute("data-frames") || "[]"); } catch (e) { frames = []; }
  if (frames.length < 2) return;

  function $(s, c) { return (c || root).querySelector(s); }
  function $$(s, c) { return Array.prototype.slice.call((c || root).querySelectorAll(s)); }
  var reduced = window.matchMedia && matchMedia("(prefers-reduced-motion: reduce)").matches;
  var byId = {};
  frames.forEach(function (f, i) { byId[f.id] = i; });

  var modo = root.getAttribute("data-modo") || "slider";
  var ai = byId[root.getAttribute("data-a")], bi = byId[root.getAttribute("data-b")];
  if (ai === undefined) ai = frames.length - 2;
  if (bi === undefined) bi = frames.length - 1;
  var armed = "a", full = false, fs = false;

  var selA = $("#cmp-a"), selB = $("#cmp-b");
  var panels = {};
  $$(".cmp-panel").forEach(function (p) { panels[p.getAttribute("data-panel")] = p; });

  /* ---------- enhance: reveal JS-only controls ---------- */
  $$(".cmp-step").forEach(function (b) { b.hidden = false; });
  var go = $(".cmp-go"); if (go) go.hidden = true;

  /* ---------- state -> DOM ---------- */
  function qs(extra) {
    var p = new URLSearchParams(location.search);
    p.set("modo", modo); p.set("a", frames[ai].id); p.set("b", frames[bi].id);
    for (var k in extra) p.set(k, extra[k]);
    return location.pathname + "?" + p.toString();
  }
  function syncUrl() {
    try { history.replaceState(null, "", qs({})); } catch (e) { /* ignore */ }
    $$(".cmp-tabs [role=tab]").forEach(function (t) { t.setAttribute("href", qs({ modo: t.getAttribute("data-modo") })); });
    var sw = $(".cmp-swap");
    if (sw) {
      var p = new URLSearchParams(location.search); p.set("a", frames[bi].id); p.set("b", frames[ai].id);
      sw.setAttribute("href", location.pathname + "?" + p.toString());
    }
    $$(".cmp-th").forEach(function (t) {
      var p = new URLSearchParams(location.search); p.set("b", frames[+t.getAttribute("data-i")].id);
      t.setAttribute("href", location.pathname + "?" + p.toString());
    });
  }
  function setImg(img, f, slot) {
    if (img.getAttribute("src") !== f.u) img.setAttribute("src", f.u);
    var base = img.getAttribute("alt") || "";
    img.setAttribute("alt", base.replace(/, [^,]*\((antes|después)\)/, ", " + f.l + " ($1)").replace(/, [^,]*\(después\), con/, ", " + f.l + " (después), con"));
  }
  function render() {
    selA.value = String(frames[ai].id); selB.value = String(frames[bi].id);
    $$("[data-img=a]").forEach(function (im) { setImg(im, frames[ai]); });
    $$("[data-img=b]").forEach(function (im) { setImg(im, frames[bi]); });
    $$("[data-lbl=a]").forEach(function (e) { e.textContent = frames[ai].l; });
    $$("[data-lbl=b]").forEach(function (e) { e.textContent = frames[bi].l; });
    $$(".cmp-th").forEach(function (t) {
      var i = +t.getAttribute("data-i");
      t.classList.toggle("selA", i === ai); t.classList.toggle("selB", i === bi);
    });
    var same = $(".notice.warn"); if (same) same.hidden = ai !== bi;
    syncUrl();
    if (modo === "diff") scheduleDiff();
    updateHeights();
  }
  function setSel(slot, i) {
    i = L.clamp(i, 0, frames.length - 1);
    if (slot === "a") ai = i; else bi = i;
    resetZoom(); render();
  }

  /* ---------- mode switching ---------- */
  function setModo(m) {
    if (!panels[m]) return;
    if (modo === "timelapse" && m !== "timelapse") tlPause();
    modo = m;
    Object.keys(panels).forEach(function (k) { panels[k].hidden = k !== m; });
    $$(".cmp-tabs [role=tab]").forEach(function (t) { t.setAttribute("aria-selected", String(t.getAttribute("data-modo") === m)); });
    $$("[data-for=compare]").forEach(function (e) { e.hidden = m === "timelapse"; });
    $("#cmp-fs").hidden = !(m === "slider" || m === "diff");
    if (fs && !(m === "slider" || m === "diff")) toggleFs(false);
    if (m === "lado") $$("img", panels.lado).forEach(function (im) { im.loading = "eager"; });
    var hid = $("input[name=modo]"); $$("input[name=modo]").forEach(function (h) { h.value = m; });
    resetZoom();
    if (m === "diff") scheduleDiff();
    if (m === "timelapse") tlEnter();
    syncUrl(); updateHeights();
  }
  $(".cmp-tabs").addEventListener("click", function (e) {
    var t = e.target.closest("[role=tab]");
    if (!t || e.metaKey || e.ctrlKey || e.shiftKey) return;
    e.preventDefault(); setModo(t.getAttribute("data-modo"));
  });

  /* ---------- pickers ---------- */
  selA.addEventListener("change", function () { setSel("a", byId[selA.value]); });
  selB.addEventListener("change", function () { setSel("b", byId[selB.value]); });
  root.addEventListener("click", function (e) {
    var st = e.target.closest(".cmp-step");
    if (st) {
      var s = st.getAttribute("data-step"); setSel(s.charAt(0), (s.charAt(0) === "a" ? ai : bi) + (s.slice(1) === "-1" ? -1 : 1));
      return;
    }
    var sw = e.target.closest(".cmp-swap");
    if (sw && !e.metaKey && !e.ctrlKey) { e.preventDefault(); var t = ai; ai = bi; bi = t; resetZoom(); render(); return; }
    var pr = e.target.closest(".cmp-presets a");
    if (pr && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      var a = byId[pr.getAttribute("data-a")], b = byId[pr.getAttribute("data-b")];
      if (a !== undefined && b !== undefined) { ai = a; bi = b; resetZoom(); render(); }
      return;
    }
    var arm = e.target.closest("[data-arm]");
    if (arm) { setArmed(arm.getAttribute("data-arm")); return; }
    var th = e.target.closest(".cmp-th");
    if (th && !e.metaKey && !e.ctrlKey && !e.shiftKey) {
      e.preventDefault();
      var i = +th.getAttribute("data-i");
      if (modo === "timelapse") { tlGoto(i); return; }
      setSel(armed, i); setArmed(armed === "a" ? "b" : "a");
    }
  });
  function setArmed(s) {
    armed = s;
    $$("[data-arm]").forEach(function (b) { var on = b.getAttribute("data-arm") === s; b.classList.toggle("on", on); b.setAttribute("aria-pressed", String(on)); });
  }

  /* ---------- full page / fullscreen ---------- */
  var fullBtn = $("#cmp-full"), fsBtn = $("#cmp-fs");
  fullBtn.addEventListener("click", function () {
    full = !full; root.classList.toggle("full", full);
    fullBtn.setAttribute("aria-pressed", String(full));
    fullBtn.textContent = full ? "Ver solo la parte superior" : "Ver página completa";
    if (modo === "diff") scheduleDiff();
  });
  var fsOpener = null;
  function toggleFs(on) {
    if (on === fs) return;
    fs = on; root.classList.toggle("fs", on);
    document.documentElement.style.overflow = on ? "hidden" : "";
    fsBtn.textContent = on ? "Salir de pantalla completa" : "Pantalla completa";
    fsBtn.setAttribute("aria-pressed", String(on));
    resetZoom();
    if (on) { fsOpener = document.activeElement; fsBtn.focus(); } else if (fsOpener && fsOpener.focus) fsOpener.focus();
  }
  fsBtn.addEventListener("click", function () { toggleFs(!fs); });
  fsBtn.hidden = !(modo === "slider" || modo === "diff");

  /* ---------- slider ---------- */
  var pos = $("#cmp-pos"), sliderStage = $("#stage-slider"), sliderZoom = $(".cmp-zoom", sliderStage);
  function setPos(v) {
    v = L.clamp(v, 0, 100);
    pos.value = String(Math.round(v));
    sliderZoom.style.setProperty("--x", v + "%");
  }
  pos.addEventListener("input", function () { setPos(+pos.value); });
  setPos(50);

  /* Pointer handling shared by slider and diff stages: slider drag; in fullscreen, pan + pinch zoom. */
  var zooms = [];
  function bindStage(stage, zoom, withSlider) {
    var z = { s: 1, x: 0, y: 0 }; zooms.push({ z: z, el: zoom });
    var ptrs = {}, mode = null, start = null, lastTap = 0;
    function apply() { zoom.style.transform = z.s === 1 && !z.x && !z.y ? "" : "translate(" + z.x + "px," + z.y + "px) scale(" + z.s + ")"; }
    function clampPan() {
      var w = stage.clientWidth, h = stage.clientHeight;
      z.x = L.clamp(z.x, Math.min(0, w - w * z.s), 0);
      z.y = L.clamp(z.y, Math.min(0, h - zoom.offsetHeight * z.s), 0);
    }
    function posFromX(cx) {
      var r = zoom.getBoundingClientRect();
      if (r.width) setPos((cx - r.left) / r.width * 100);
    }
    function pts() { return Object.keys(ptrs).map(function (k) { return ptrs[k]; }); }
    function zoomAt(ns, mx, my) {
      ns = L.clamp(ns, 1, 6);
      var cx = (mx - z.x) / z.s, cy = (my - z.y) / z.s;
      z.s = ns; z.x = mx - cx * ns; z.y = my - cy * ns; clampPan(); apply();
    }
    function local(e) { var r = stage.getBoundingClientRect(); return { x: e.clientX - r.left, y: e.clientY - r.top }; }
    z.reset = function () { z.s = 1; z.x = 0; z.y = 0; apply(); };
    stage.addEventListener("pointerdown", function (e) {
      if (withSlider && (e.target.closest(".cmp-handle") || (!fs && e.pointerType === "mouse"))) {
        mode = "slider"; stage.setPointerCapture(e.pointerId); posFromX(e.clientX); e.preventDefault(); return;
      }
      if (!fs) return;
      stage.setPointerCapture(e.pointerId);
      ptrs[e.pointerId] = local(e);
      var p = pts();
      if (p.length === 1) {
        mode = "pan"; start = { x: p[0].x, y: p[0].y, zx: z.x, zy: z.y, moved: false };
      } else if (p.length === 2) {
        mode = "pinch";
        start = { d: Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y), s: z.s, mx: (p[0].x + p[1].x) / 2, my: (p[0].y + p[1].y) / 2, zx: z.x, zy: z.y };
      }
    });
    stage.addEventListener("pointermove", function (e) {
      if (mode === "slider") { posFromX(e.clientX); return; }
      if (!ptrs[e.pointerId]) return;
      ptrs[e.pointerId] = local(e);
      var p = pts();
      if (mode === "pan" && p.length === 1) {
        var dx = p[0].x - start.x, dy = p[0].y - start.y;
        if (Math.abs(dx) + Math.abs(dy) > 4) start.moved = true;
        z.x = start.zx + dx; z.y = start.zy + dy; clampPan(); apply();
      } else if (mode === "pinch" && p.length === 2) {
        var d = Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y), mx = (p[0].x + p[1].x) / 2, my = (p[0].y + p[1].y) / 2;
        var ns = L.clamp(start.s * d / (start.d || 1), 1, 6);
        var cx = (start.mx - start.zx) / start.s, cy = (start.my - start.zy) / start.s;
        z.s = ns; z.x = mx - cx * ns; z.y = my - cy * ns; clampPan(); apply();
      }
    });
    function up(e) {
      if (mode === "slider") { mode = null; return; }
      var wasPan = mode === "pan" && start && !start.moved;
      delete ptrs[e.pointerId];
      if (!Object.keys(ptrs).length) {
        if (wasPan) { /* double tap toggles zoom */
          var now = Date.now();
          if (now - lastTap < 320) { var l = local(e); if (z.s > 1) { z.reset(); } else zoomAt(2.5, l.x, l.y); lastTap = 0; } else lastTap = now;
        }
        mode = null;
      } else if (Object.keys(ptrs).length === 1) {
        var p = pts()[0]; mode = "pan"; start = { x: p.x, y: p.y, zx: z.x, zy: z.y, moved: true };
      }
    }
    stage.addEventListener("pointerup", up); stage.addEventListener("pointercancel", up);
    stage.addEventListener("wheel", function (e) {
      if (!fs) return;
      e.preventDefault();
      var l = local(e);
      if (e.ctrlKey || e.metaKey) zoomAt(z.s * (e.deltaY < 0 ? 1.15 : 1 / 1.15), l.x, l.y);
      else { z.y -= e.deltaY; z.x -= e.deltaX; clampPan(); apply(); }
    }, { passive: false });
  }
  function resetZoom() { zooms.forEach(function (o) { o.z.reset(); }); }
  bindStage(sliderStage, sliderZoom, true);
  var diffStage = $("#stage-diff");
  bindStage(diffStage, $(".cmp-zoom", diffStage), false);

  /* ---------- side by side: synced vertical scroll ---------- */
  (function () {
    var panes = $$(".cmp-pane"), lock = false;
    panes.forEach(function (p, i) {
      p.addEventListener("scroll", function () {
        if (lock) return;
        lock = true;
        var o = panes[1 - i], max = p.scrollHeight - p.clientHeight, omax = o.scrollHeight - o.clientHeight;
        o.scrollTop = max > 0 ? p.scrollTop / max * omax : 0;
        requestAnimationFrame(function () { lock = false; });
      }, { passive: true });
    });
  })();

  /* ---------- heights note ---------- */
  function updateHeights() {
    var out = $("#cmp-heights"), a = $("[data-img=a]", panels.slider), b = $("[data-img=b]", panels.slider);
    if (!a.naturalWidth || !b.naturalWidth) { out.textContent = ""; return; }
    var ha = a.naturalHeight / a.naturalWidth, hb = b.naturalHeight / b.naturalWidth;
    out.textContent = Math.abs(ha - hb) / Math.max(ha, hb) > 0.02
      ? "Las capturas tienen distinta altura (" + Math.round(ha * 1000) + " y " + Math.round(hb * 1000) + " por cada 1000 px de ancho): se alinean por la parte superior." : "";
  }
  $$("[data-img]", panels.slider).forEach(function (im) { im.addEventListener("load", updateHeights); });

  /* ---------- diff (client-side, canvas) ---------- */
  var sens = $("#cmp-sens"), pct = $("#cmp-pct"), heat = $("#cmp-heat"), diffNote = $("#cmp-diffnote");
  var diffTok = 0, diffTimer = 0, lastRes = null, lastCrop = false;
  function loadImg(u) {
    return new Promise(function (ok, bad) {
      var im = new Image(); im.decoding = "async";
      im.onload = function () { ok(im); }; im.onerror = function () { bad(new Error("img")); };
      im.src = u;
    });
  }
  function scheduleDiff() {
    clearTimeout(diffTimer);
    pct.textContent = "Calculando…";
    var tok = ++diffTok;
    diffTimer = setTimeout(function () { runDiff(tok); }, 120);   // throttle quick A/B changes
  }
  function runDiff(tok) {
    var fa = frames[ai], fb = frames[bi];
    Promise.all([loadImg(fa.u), loadImg(fb.u)]).then(function (r) {
      if (tok !== diffTok) return;
      var A = r[0], B = r[1];
      var W = Math.min(window.innerWidth < 700 ? 320 : 480, A.naturalWidth, B.naturalWidth);
      var sa = W / A.naturalWidth, sb = W / B.naturalWidth;
      var ha = Math.round(A.naturalHeight * sa), hb = Math.round(B.naturalHeight * sb);
      var cssW = diffStage.clientWidth || W;
      var cap = full ? 6000 : Math.round(2.5 * window.innerHeight * W / cssW);
      var h = Math.min(ha, hb, cap);
      lastCrop = h < Math.min(ha, hb);
      var bs = W <= 320 ? 12 : 16;
      var cv = document.createElement("canvas"); cv.width = W; cv.height = h;
      var cx = cv.getContext("2d", { willReadFrequently: true });
      function grab(im, s) {
        cx.clearRect(0, 0, W, h);
        cx.drawImage(im, 0, 0, im.naturalWidth, Math.min(im.naturalHeight, h / s), 0, 0, W, h);
        return cx.getImageData(0, 0, W, h);
      }
      var da = grab(A, sa);
      var db = grab(B, sb);
      lastRes = L.blockDiffs(da, db, bs);
      lastRes.hFull = hb; lastRes.W = W;
      drawHeat();
    }).catch(function () { if (tok === diffTok) { pct.textContent = "No se pudieron cargar las capturas"; lastRes = null; } });
  }
  var heatRaf = 0;
  function drawHeat() {
    if (!lastRes) return;
    var res = lastRes, thr = +sens.value;
    heat.width = res.cols; heat.height = res.rows;
    var cx = heat.getContext("2d");
    var img = cx.createImageData(res.cols, res.rows);
    img.data.set(L.heatPixels(res, thr));
    cx.putImageData(img, 0, 0);
    heat.style.width = (res.cols * res.bs / res.W * 100) + "%";
    heat.style.height = (res.rows * res.bs / res.hFull * 100) + "%";
    var p = L.changedPercent(res, thr);
    pct.textContent = L.fmtPct(p) + " cambiado" + (lastCrop ? " (parte superior)" : "");
    diffNote.textContent = "Se compara en tu navegador, alineando por anchura. Las zonas en naranja son las que cambian entre las dos capturas." +
      (lastCrop ? " Solo se analiza la parte superior; pulsa «Ver página completa» para analizarla entera." : "");
  }
  sens.addEventListener("input", function () { cancelAnimationFrame(heatRaf); heatRaf = requestAnimationFrame(drawHeat); });

  /* ---------- timelapse ---------- */
  var tlImg = $("#tl-img"), tlScrub = $("#tl-scrub"), tlTime = $("#tl-time"), tlPlay = $("#tl-play"),
    tlSpeed = $("#tl-speed"), tlDay = $("#tl-day");
  var tl = { list: [], pos: 0, playing: false, tok: 0, timer: 0, inited: false, keep: [] };
  function tlBuild(day, keepIdx) {
    tl.list = L.framesOfDay(frames, day);
    tlScrub.max = String(Math.max(0, tl.list.length - 1));
    var p = keepIdx === undefined ? -1 : tl.list.indexOf(keepIdx);
    tl.pos = p >= 0 ? p : 0;
    tlShow();
  }
  function tlEnter() {
    if (!tl.inited) { tl.inited = true; tlBuild("", bi); }
    else tlShow();
  }
  function tlPreload(p) {
    for (var k = 1; k <= 2; k++) {
      var j = tl.list[p + k]; if (j === undefined) continue;
      var im = new Image(); im.src = frames[j].u; tl.keep.push(im);
      if (tl.keep.length > 6) tl.keep.shift();
    }
  }
  function tlShow() {
    if (!tl.list.length) return;
    tl.pos = L.clamp(tl.pos, 0, tl.list.length - 1);
    var f = frames[tl.list[tl.pos]];
    if (tlImg.getAttribute("src") !== f.u) tlImg.setAttribute("src", f.u);
    tlImg.setAttribute("alt", "Fotograma del timelapse: " + f.l);
    tlScrub.value = String(tl.pos);
    tlTime.textContent = f.l + " · " + (tl.pos + 1) + "/" + tl.list.length;
    $$(".cmp-th").forEach(function (t) {
      var cur = +t.getAttribute("data-i") === tl.list[tl.pos] && modo === "timelapse";
      t.classList.toggle("cur", cur);
      if (cur) {
        var strip = t.closest(".cmp-strip"), tr = t.getBoundingClientRect(), sr = strip.getBoundingClientRect();
        if (tr.left < sr.left || tr.right > sr.right) strip.scrollTo({ left: strip.scrollLeft + tr.left - sr.left - sr.width / 2 + tr.width / 2, behavior: reduced ? "auto" : "smooth" });
      }
    });
    tlPreload(tl.pos);
  }
  function tlGoto(frameIdx) {
    var p = tl.list.indexOf(frameIdx);
    if (p < 0) { tlDay.value = ""; tlBuild("", frameIdx); p = tl.list.indexOf(frameIdx); }
    tl.pos = Math.max(0, p); tlShow();
  }
  function tlStep(d) { tl.pos = L.clamp(tl.pos + d, 0, tl.list.length - 1); tlShow(); }
  function tlPause() {
    tl.playing = false; tl.tok++; clearTimeout(tl.timer);
    tlPlay.textContent = "▶ Reproducir"; tlPlay.setAttribute("aria-label", "Reproducir"); tlPlay.setAttribute("aria-pressed", "false");
  }
  function tlPlayStart() {
    if (tl.pos >= tl.list.length - 1) tl.pos = 0;
    tl.playing = true; var tok = ++tl.tok;
    tlPlay.textContent = "⏸ Pausa"; tlPlay.setAttribute("aria-label", "Pausar"); tlPlay.setAttribute("aria-pressed", "true");
    tlShow();
    (function tick() {
      tl.timer = setTimeout(function () {
        if (tok !== tl.tok) return;
        if (tl.pos >= tl.list.length - 1) { tlPause(); return; }
        var f = frames[tl.list[tl.pos + 1]], im = new Image();
        function next() { if (tok !== tl.tok) return; tl.pos++; tlShow(); tick(); }
        im.onload = im.onerror = next; im.src = f.u;          // wait for the next frame (cached once preloaded)
      }, L.frameInterval(+tlSpeed.value));
    })();
  }
  tlPlay.addEventListener("click", function () { tl.playing ? tlPause() : tlPlayStart(); });
  $("#tl-prev").addEventListener("click", function () { tlPause(); tlStep(-1); });
  $("#tl-next").addEventListener("click", function () { tlPause(); tlStep(1); });
  tlScrub.addEventListener("input", function () { tl.pos = +tlScrub.value; tlShow(); });
  tlDay.addEventListener("change", function () { tlPause(); tlBuild(tlDay.value); });
  tlSpeed.addEventListener("change", function () { if (tl.playing) { clearTimeout(tl.timer); var t = tl.tok; tlPause(); tlPlayStart(); } });
  if (reduced) { var hint = $("#tl-hint"); if (hint) hint.textContent += " Tienes activado «reducir movimiento»: la reproducción nunca empieza sola."; }

  /* ---------- keyboard ---------- */
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && fs) { toggleFs(false); return; }
    if (modo !== "timelapse" || e.altKey || e.ctrlKey || e.metaKey) return;
    var t = e.target;
    if (t && t.closest && t.closest("input,select,textarea")) return;
    var k = e.key, handled = true;
    if (k === " " || k === "k") { if (t && t.closest && t.closest("button,a")) return; tl.playing ? tlPause() : tlPlayStart(); }
    else if (k === "ArrowRight") { tlPause(); tlStep(1); }
    else if (k === "ArrowLeft") { tlPause(); tlStep(-1); }
    else if (k === "Home") { tlPause(); tl.pos = 0; tlShow(); }
    else if (k === "End") { tlPause(); tl.pos = tl.list.length - 1; tlShow(); }
    else if (k === "+" || k === "=" || k === "-") {
      var o = $$("option", tlSpeed), i = tlSpeed.selectedIndex + (k === "-" ? -1 : 1);
      if (i >= 0 && i < o.length) { tlSpeed.selectedIndex = i; tlSpeed.dispatchEvent(new Event("change")); }
    } else handled = false;
    if (handled) e.preventDefault();
  });

  /* ---------- boot ---------- */
  setArmed("a");
  setModo(modo);
  render();
})();
