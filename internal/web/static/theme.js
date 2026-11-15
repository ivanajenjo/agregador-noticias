/* Light/dark toggle. Loaded blocking in <head> so a forced theme is applied before first paint.
   No stored choice => the page follows the system (prefers-color-scheme). */
(function () {
  "use strict";
  var KEY = "theme", root = document.documentElement;
  var mq = window.matchMedia && matchMedia("(prefers-color-scheme: dark)");
  function stored() { try { return localStorage.getItem(KEY); } catch (e) { return null; } }
  function current() { return root.getAttribute("data-theme") || (mq && mq.matches ? "dark" : "light"); }
  function label() {
    var next = current() === "dark" ? "Cambiar a modo claro" : "Cambiar a modo oscuro";
    var b = document.querySelector("[data-theme-toggle]");
    if (b) { b.setAttribute("aria-label", next); b.title = next; }
  }
  var t = stored();
  if (t === "light" || t === "dark") root.setAttribute("data-theme", t);

  document.addEventListener("DOMContentLoaded", label);
  if (mq && mq.addEventListener) mq.addEventListener("change", label);
  document.addEventListener("click", function (e) {
    if (!(e.target.closest && e.target.closest("[data-theme-toggle]"))) return;
    var next = current() === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", next);
    try { localStorage.setItem(KEY, next); } catch (err) {}
    label();
  });
})();
