const test = require("node:test");
const assert = require("node:assert");
const L = require("./static/shotscmp-logic.js");

const frames = [{ t: 100, d: "a" }, { t: 200, d: "a" }, { t: 400, d: "b" }];
test("nearestFrame", () => {
  assert.strictEqual(L.nearestFrame([], 5), -1);
  assert.strictEqual(L.nearestFrame(frames, 0), 0);
  assert.strictEqual(L.nearestFrame(frames, 149), 0);
  assert.strictEqual(L.nearestFrame(frames, 150), 0); // tie -> earlier
  assert.strictEqual(L.nearestFrame(frames, 151), 1);
  assert.strictEqual(L.nearestFrame(frames, 301), 2);
  assert.strictEqual(L.nearestFrame(frames, 9999), 2);
});
test("framesOfDay", () => {
  assert.deepStrictEqual(L.framesOfDay(frames, "a"), [0, 1]);
  assert.deepStrictEqual(L.framesOfDay(frames, ""), [0, 1, 2]);
});
function img(w, h, fn) {
  const data = new Uint8ClampedArray(w * h * 4);
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
    const v = fn(x, y), o = (y * w + x) * 4;
    data[o] = data[o + 1] = data[o + 2] = v; data[o + 3] = 255;
  }
  return { data, width: w, height: h };
}
test("blockDiffs / changedPercent", () => {
  const a = img(32, 32, () => 10);
  const b = img(32, 48, (x, y) => (x >= 16 && y < 16 ? 200 : 10)); // taller; one block-quadrant changed
  const r = L.blockDiffs(a, b, 16);
  assert.strictEqual(r.cols, 2); assert.strictEqual(r.rows, 2);
  assert.ok(r.diffs[1] > 180); assert.strictEqual(r.diffs[0], 0);
  assert.strictEqual(L.changedPercent(r, 20), 25);
  assert.strictEqual(L.changedPercent(r, 250), 0);
  const same = L.blockDiffs(a, a, 8);
  assert.strictEqual(L.changedPercent(same, 1), 0);
  const px = L.heatPixels(r, 20);
  assert.strictEqual(px[3], 0); assert.ok(px[7] > 0);
});
test("fmtPct/frameInterval", () => {
  assert.strictEqual(L.fmtPct(0), "0 %");
  assert.strictEqual(L.fmtPct(0.04), "<0,1 %");
  assert.strictEqual(L.fmtPct(3.26), "3,3 %");
  assert.strictEqual(L.fmtPct(42.4), "42 %");
  assert.ok(L.frameInterval(2) < L.frameInterval(1));
});
