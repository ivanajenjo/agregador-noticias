/* Agregador UI: carousel chips, RAG streaming, shots viewer, service worker. No dependencies. */
(function () {
  "use strict";
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };

  if ("serviceWorker" in navigator && window.isSecureContext) {
    window.addEventListener("load", function () { navigator.serviceWorker.register("/sw.js").catch(function () {}); });
  }

  /* ---- home chips: scroll the carousel to a column ---- */
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("[data-scroll-to]");
    if (!a) return;
    var col = document.getElementById(a.getAttribute("data-scroll-to"));
    var wrap = col && col.parentNode;
    if (!col) return;
    e.preventDefault();
    if (wrap && wrap.scrollWidth > wrap.clientWidth) {
      wrap.scrollTo({ left: col.offsetLeft - wrap.offsetLeft - 12, behavior: "smooth" });
    } else {
      col.scrollIntoView({ behavior: "smooth", block: "start" });
    }
    $$(".chip.on").forEach(function (c) { c.classList.remove("on"); });
    a.classList.add("on");
  });

  /* ---- shots viewer (pinch-zoom, pan, swipe between items) ---- */
  var viewer, vimg, vtitle, items = [], idx = 0, st = { s: 1, x: 0, y: 0 };
  function buildViewer() {
    viewer = document.createElement("div");
    viewer.className = "viewer";
    viewer.setAttribute("role", "dialog");
    viewer.setAttribute("aria-modal", "true");
    viewer.setAttribute("aria-label", "Visor de capturas");
    viewer.innerHTML = '<div class="vbar"><button type="button" data-v="prev" aria-label="Anterior">‹</button>' +
      '<span class="vt"></span><button type="button" data-v="next" aria-label="Siguiente">›</button>' +
      '<button type="button" data-v="close" aria-label="Cerrar">✕</button></div>' +
      '<div class="vstage"><img alt=""><div class="vhint">Pellizca para ampliar · desliza para cambiar de medio</div></div>';
    document.body.appendChild(viewer);
    vimg = $("img", viewer); vtitle = $(".vt", viewer);
    viewer.addEventListener("click", function (e) {
      var b = e.target.closest("[data-v]");
      if (!b) return;
      var v = b.getAttribute("data-v");
      if (v === "close") closeViewer(); else go(v === "next" ? 1 : -1);
    });
    bindGestures($(".vstage", viewer));
  }
  function apply() {
    vimg.style.transform = "translate(" + st.x + "px," + st.y + "px) scale(" + st.s + ")";
  }
  function clamp() {
    var stage = vimg.parentNode, w = stage.clientWidth, h = vimg.clientHeight * st.s, sw = w * st.s;
    var minX = Math.min(0, w - sw), minY = Math.min(0, stage.clientHeight - h);
    st.x = Math.max(minX, Math.min(0, st.x));
    st.y = Math.max(minY, Math.min(0, st.y));
  }
  function show(i) {
    idx = (i + items.length) % items.length;
    var a = items[idx];
    vimg.src = a.getAttribute("href");
    vimg.alt = a.getAttribute("data-title") || "";
    vtitle.textContent = (a.getAttribute("data-title") || "") + (items.length > 1 ? "  (" + (idx + 1) + "/" + items.length + ")" : "");
    st = { s: 1, x: 0, y: 0 }; apply();
  }
  function go(d) { if (items.length > 1) show(idx + d); }
  function openViewer(a) {
    if (!viewer) buildViewer();
    var g = a.getAttribute("data-viewer");
    items = $$('a[data-viewer="' + g + '"]');
    viewer.classList.add("open");
    document.documentElement.style.overflow = "hidden";
    viewer._opener = a;
    show(items.indexOf(a));
    $('[data-v="close"]', viewer).focus();
  }
  function closeViewer() {
    viewer.classList.remove("open");
    document.documentElement.style.overflow = "";
    if (viewer._opener) viewer._opener.focus();
  }
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("a[data-viewer]");
    if (!a || e.metaKey || e.ctrlKey || e.shiftKey) return;
    e.preventDefault();
    openViewer(a);
  });
  document.addEventListener("keydown", function (e) {
    if (!viewer || !viewer.classList.contains("open")) return;
    if (e.key === "Escape") closeViewer();
    else if (e.key === "ArrowRight") go(1);
    else if (e.key === "ArrowLeft") go(-1);
  });
  function bindGestures(stage) {
    var ptrs = {}, start = null, lastTap = 0;
    function pts() { return Object.keys(ptrs).map(function (k) { return ptrs[k]; }); }
    function dist(a, b) { return Math.hypot(a.x - b.x, a.y - b.y); }
    stage.addEventListener("pointerdown", function (e) {
      stage.setPointerCapture(e.pointerId);
      ptrs[e.pointerId] = { x: e.clientX, y: e.clientY };
      var p = pts();
      start = { n: p.length, st: { s: st.s, x: st.x, y: st.y }, p: p.map(function (q) { return { x: q.x, y: q.y }; }), t: Date.now(), moved: false };
    });
    stage.addEventListener("pointermove", function (e) {
      if (!ptrs[e.pointerId] || !start) return;
      ptrs[e.pointerId] = { x: e.clientX, y: e.clientY };
      var p = pts();
      if (p.length >= 2 && start.p.length >= 2) {
        var ns = Math.max(1, Math.min(6, start.st.s * dist(p[0], p[1]) / dist(start.p[0], start.p[1])));
        var r = stage.getBoundingClientRect();
        var cx = (p[0].x + p[1].x) / 2 - r.left, cy = (p[0].y + p[1].y) / 2 - r.top;
        var ocx = (start.p[0].x + start.p[1].x) / 2 - r.left, ocy = (start.p[0].y + start.p[1].y) / 2 - r.top;
        var k = ns / start.st.s;
        st.s = ns; st.x = cx - (ocx - start.st.x) * k; st.y = cy - (ocy - start.st.y) * k;
        start.moved = true; clamp(); apply();
      } else if (p.length === 1 && start.p.length === 1) {
        var dx = p[0].x - start.p[0].x, dy = p[0].y - start.p[0].y;
        if (Math.abs(dx) + Math.abs(dy) > 6) start.moved = true;
        if (st.s > 1 || Math.abs(dy) >= Math.abs(dx)) { st.x = start.st.x + dx; st.y = start.st.y + dy; clamp(); apply(); }
      }
    });
    function up(e) {
      var had = ptrs[e.pointerId];
      delete ptrs[e.pointerId];
      if (!had || !start) return;
      if (start.n === 1 && Object.keys(ptrs).length === 0) {
        var dx = e.clientX - start.p[0].x, dy = e.clientY - start.p[0].y;
        if (st.s === 1 && Math.abs(dx) > 60 && Math.abs(dx) > Math.abs(dy) * 1.5) { go(dx < 0 ? 1 : -1); }
        else if (!start.moved && Date.now() - start.t < 300) {
          if (Date.now() - lastTap < 350) { // double tap
            var r = stage.getBoundingClientRect(), ns = st.s > 1 ? 1 : 2.5;
            var px = e.clientX - r.left, py = e.clientY - r.top, k = ns / st.s;
            st.x = px - (px - st.x) * k; st.y = py - (py - st.y) * k; st.s = ns; clamp(); apply(); lastTap = 0;
          } else lastTap = Date.now();
        }
      }
      start = Object.keys(ptrs).length ? { n: Object.keys(ptrs).length, st: { s: st.s, x: st.x, y: st.y }, p: pts().map(function (q) { return { x: q.x, y: q.y }; }), t: Date.now(), moved: true } : null;
    }
    stage.addEventListener("pointerup", up);
    stage.addEventListener("pointercancel", up);
    stage.addEventListener("wheel", function (e) {
      e.preventDefault();
      if (e.ctrlKey) {
        var r = stage.getBoundingClientRect(), ns = Math.max(1, Math.min(6, st.s * (e.deltaY < 0 ? 1.15 : 0.87)));
        var px = e.clientX - r.left, py = e.clientY - r.top, k = ns / st.s;
        st.x = px - (px - st.x) * k; st.y = py - (py - st.y) * k; st.s = ns;
      } else { st.y -= e.deltaY; st.x -= e.deltaX; }
      clamp(); apply();
    }, { passive: false });
  }

  /* ---- RAG chat ---- */
  var form = $("#ask-form");
  if (form) initAsk(form);

  function initAsk(form) {
    var chat = $("#chat"), ta = $("#question"), btn = $("#ask-btn"), busy = false;
    function autosize() { ta.style.height = "auto"; ta.style.height = Math.min(ta.scrollHeight, window.innerHeight * 0.4) + "px"; }
    ta.addEventListener("input", autosize);
    if (ta.value) autosize();
    ta.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && (e.ctrlKey || e.metaKey || (!e.shiftKey && window.matchMedia("(min-width:700px)").matches))) {
        e.preventDefault(); form.requestSubmit();
      }
    });
    form.addEventListener("submit", function (e) {
      e.preventDefault();
      var q = ta.value.trim();
      if (!q || busy) return;
      ask(q);
    });

    function filters() {
      var o = { sources: [], leanings: [], from: "", to: "" };
      $$("#ask-filters input").forEach(function (i) {
        if (i.name === "source" && i.checked) o.sources.push(i.value);
        else if (i.name === "leaning" && i.checked) o.leanings.push(i.value);
        else if (i.name === "from") o.from = i.value;
        else if (i.name === "to") o.to = i.value;
      });
      return o;
    }
    function el(tag, cls, text) { var n = document.createElement(tag); if (cls) n.className = cls; if (text != null) n.textContent = text; return n; }

    // Renders text (never HTML) with **bold**, "- " lists, paragraphs and [n] citation links.
    function inline(parent, text) {
      text.split(/(\*\*[^*]+\*\*)/).forEach(function (part) {
        var holder = parent;
        if (/^\*\*[^*]+\*\*$/.test(part)) { holder = el("strong"); parent.appendChild(holder); part = part.slice(2, -2); }
        part.split(/(\[\d+(?:\s*,\s*\d+)*\])/).forEach(function (seg) {
          var m = /^\[(\d+(?:\s*,\s*\d+)*)\]$/.exec(seg);
          if (!m) { if (seg) holder.appendChild(document.createTextNode(seg)); return; }
          m[1].split(/\s*,\s*/).forEach(function (n) {
            var a = el("a", "cite", "[" + n + "]"); a.href = "#" + holder.closest(".turn").id + "-s" + n;
            holder.appendChild(a);
          });
        });
      });
    }
    function renderAnswer(box, text, typing) {
      box.textContent = "";
      var list = null;
      text.split("\n").forEach(function (line) {
        var li = /^\s*(?:[-*•]|\d+[.)])\s+(.*)$/.exec(line);
        if (li) { if (!list) { list = el("ul"); box.appendChild(list); } var it = el("li"); list.appendChild(it); inline(it, li[1]); return; }
        list = null;
        if (!line.trim()) return;
        var p = el("p"); box.appendChild(p); inline(p, line.replace(/^#+\s*/, ""));
      });
      if (typing && box.lastChild) box.lastChild.classList.add("typing");
    }
    function renderSources(ol, srcs) {
      srcs.forEach(function (s) {
        var li = el("li"); li.id = ol.getAttribute("data-turn") + "-s" + s.n;
        li.appendChild(el("span", "n", "[" + s.n + "]"));
        var d = el("div");
        var t = el("a", "t", s.title || s.url); t.href = "/articulo/" + encodeURIComponent(s.article_id); d.appendChild(t);
        var m = el("span", "meta", s.source + (s.section ? " · " + s.section : "") + (s.published ? " · " + s.published.slice(0, 10) : "") + " ");
        var o = el("a", "ext", "Original ↗"); o.href = s.url; o.target = "_blank"; o.rel = "noopener noreferrer";
        if (/^https?:\/\//i.test(s.url)) m.appendChild(o);
        d.appendChild(m);
        if (s.snippet) d.appendChild(el("div", "meta", s.snippet));
        li.appendChild(d); ol.appendChild(li);
      });
    }

    function ask(q) {
      busy = true; btn.disabled = true; chat.setAttribute("aria-busy", "true");
      var empty = $("#chat-empty"); if (empty) empty.remove();
      var id = "t" + Date.now();
      var turn = el("div", "turn"); turn.id = id;
      turn.appendChild(el("div", "q", q));
      var ans = el("div", "a"); ans.appendChild(el("p", "typing", "Buscando fuentes…"));
      turn.appendChild(ans);
      var h = el("h2", null, "Fuentes"); h.hidden = true; turn.appendChild(h);
      var ol = el("ol", "srcs"); ol.setAttribute("data-turn", id); turn.appendChild(ol);
      chat.appendChild(turn);
      turn.scrollIntoView({ behavior: "smooth", block: "start" });
      ta.value = ""; autosize();

      var f = filters(), text = "", raf = 0, errMsg = "";
      function paint() { raf = 0; renderAnswer(ans, text, true); }
      function finish() {
        if (raf) cancelAnimationFrame(raf);
        if (text) renderAnswer(ans, text, false);
        else if (!errMsg) { ans.textContent = ""; ans.appendChild(el("p", "muted", "Sin respuesta.")); }
        else ans.textContent = "";
        if (errMsg) { var p = el("p", "err", errMsg); p.setAttribute("role", "alert"); ans.appendChild(p); }
        busy = false; btn.disabled = false; chat.setAttribute("aria-busy", "false");
      }
      function onEvent(name, data) {
        if (name === "sources") {
          var srcs = []; try { srcs = JSON.parse(data); } catch (e) {}
          h.hidden = !srcs.length; renderSources(ol, srcs);
          if (!text) { ans.textContent = ""; ans.appendChild(el("p", "typing", srcs.length ? "Redactando respuesta…" : "Sin fuentes relevantes. Redactando…")); }
        } else if (name === "delta") {
          text += data; if (!raf) raf = requestAnimationFrame(paint);
        } else if (name === "error") { errMsg = data; }
      }
      fetch("/api/ask", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Requested-With": "fetch" },
        body: JSON.stringify({ question: q, compare: $("#compare").checked, sources: f.sources, leanings: f.leanings, from: f.from, to: f.to })
      }).then(function (res) {
        if (!res.ok) return res.json().catch(function () { return {}; }).then(function (j) { throw new Error(j.error || ("Error " + res.status)); });
        var reader = res.body.getReader(), dec = new TextDecoder(), buf = "";
        function parse(block) {
          var name = "message", data = [];
          block.split("\n").forEach(function (l) {
            if (l.indexOf("event:") === 0) name = l.slice(6).trim();
            else if (l.indexOf("data:") === 0) data.push(l.slice(5).replace(/^ /, ""));
          });
          if (data.length || name !== "message") onEvent(name, data.join("\n"));
        }
        function pump() {
          return reader.read().then(function (r) {
            if (r.done) { if (buf.trim()) parse(buf); return; }
            buf += dec.decode(r.value, { stream: true }).replace(/\r\n/g, "\n");
            var i;
            while ((i = buf.indexOf("\n\n")) >= 0) { parse(buf.slice(0, i)); buf = buf.slice(i + 2); }
            return pump();
          });
        }
        return pump();
      }).catch(function (err) { errMsg = err.message || "Error de red"; }).then(finish);
    }
  }
})();
