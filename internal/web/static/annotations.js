(function () {
  "use strict";
  var r = document.getElementById("conf");
  var hid = document.getElementById("confval");
  var out = document.getElementById("confout");
  if (!r || !hid) return;
  function sync() {
    hid.value = String(Number(r.value) / 100);
    if (out) out.textContent = r.value + " %";
  }
  r.addEventListener("input", sync);
  r.addEventListener("change", function () { sync(); r.form.submit(); });
})();
