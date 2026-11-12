/* Pure helpers for the capture comparator (no DOM). Loaded as a classic script
   (window.CmpLogic) and as a CommonJS module for `node --test`. */
(function (root) {
  "use strict";

  /* Index of the frame closest to unix time t (frames sorted by .t). -1 if empty. */
  function nearestFrame(frames, t) {
    var n = frames.length;
    if (!n) return -1;
    var lo = 0, hi = n;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      if (frames[mid].t >= t) hi = mid; else lo = mid + 1;
    }
    if (lo === 0) return 0;
    if (lo === n) return n - 1;
    return (t - frames[lo - 1].t <= frames[lo].t - t) ? lo - 1 : lo;
  }

  /* Indexes of frames whose day key equals day ("" = all). */
  function framesOfDay(frames, day) {
    var out = [];
    for (var i = 0; i < frames.length; i++) if (!day || frames[i].d === day) out.push(i);
    return out;
  }

  function clamp(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }

  /* Perceptual-ish luminance of an RGBA pixel (0..255). */
  function luma(d, o) { return 0.299 * d[o] + 0.587 * d[o + 1] + 0.114 * d[o + 2]; }

  /* Mean absolute luminance difference per block between two RGBA buffers.
     a and b are {data,width,height}; only the overlapping top-left area is
     compared (images are aligned on width upstream). Returns
     {cols, rows, bs, diffs: Float32Array(cols*rows)} with values 0..255. */
  function blockDiffs(a, b, bs) {
    var w = Math.min(a.width, b.width), h = Math.min(a.height, b.height);
    bs = Math.max(1, bs | 0);
    var cols = Math.floor(w / bs), rows = Math.floor(h / bs);
    var diffs = new Float32Array(cols * rows);
    for (var by = 0; by < rows; by++) {
      for (var bx = 0; bx < cols; bx++) {
        var sum = 0;
        for (var y = by * bs; y < (by + 1) * bs; y++) {
          var ra = y * a.width * 4, rb = y * b.width * 4;
          for (var x = bx * bs; x < (bx + 1) * bs; x++) {
            sum += Math.abs(luma(a.data, ra + x * 4) - luma(b.data, rb + x * 4));
          }
        }
        diffs[by * cols + bx] = sum / (bs * bs);
      }
    }
    return { cols: cols, rows: rows, bs: bs, diffs: diffs };
  }

  /* Percentage (0..100) of blocks whose diff exceeds thr. */
  function changedPercent(res, thr) {
    var n = res.diffs.length;
    if (!n) return 0;
    var c = 0;
    for (var i = 0; i < n; i++) if (res.diffs[i] > thr) c++;
    return c * 100 / n;
  }

  /* Heatmap pixels (RGBA Uint8ClampedArray cols*rows) for blocks above thr;
     intensity grows with the difference. */
  function heatPixels(res, thr) {
    var out = new Uint8ClampedArray(res.diffs.length * 4);
    for (var i = 0; i < res.diffs.length; i++) {
      var d = res.diffs[i];
      if (d <= thr) continue;
      var k = clamp((d - thr) / Math.max(1, 120 - thr), 0, 1);
      out[i * 4] = 255; out[i * 4 + 1] = 90 - 60 * k; out[i * 4 + 2] = 0;
      out[i * 4 + 3] = 110 + 120 * k;
    }
    return out;
  }

  function fmtPct(p) {
    if (p === 0) return "0 %";
    if (p < 0.1) return "<0,1 %";
    return (p < 10 ? p.toFixed(1) : Math.round(p).toString()).replace(".", ",") + " %";
  }

  /* Interval (ms) between timelapse frames for a speed multiplier. */
  function frameInterval(speed) { return Math.round(1100 / clamp(speed || 1, 0.25, 8)); }

  var api = { nearestFrame: nearestFrame, framesOfDay: framesOfDay, blockDiffs: blockDiffs,
    changedPercent: changedPercent, heatPixels: heatPixels, fmtPct: fmtPct, frameInterval: frameInterval, clamp: clamp };
  if (typeof module !== "undefined" && module.exports) module.exports = api; else root.CmpLogic = api;
})(typeof window !== "undefined" ? window : this);
