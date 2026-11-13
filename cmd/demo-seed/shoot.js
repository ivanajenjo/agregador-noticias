// Usage: node shoot-demo.js <outdir> <base-url> <fake-llm-port> [only-substring]
const { chromium } = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const http = require('http');
const fs = require('fs');
const [OUT, BASE, FAKE, ONLY] = [process.argv[2], process.argv[3], +process.argv[4], process.argv[5]];
fs.mkdirSync(OUT, { recursive: true });

// ---- fake OpenAI-compatible server (chat + embeddings, same hashing as internal/demo/embed.go)
const fold = s => s.replace(/á/g, 'a').replace(/é/g, 'e').replace(/í/g, 'i').replace(/ó/g, 'o').replace(/ú/g, 'u').replace(/ü/g, 'u').replace(/ñ/g, 'n');
function fnv(s) { let h = 0x811c9dc5; const b = Buffer.from(s); for (const c of b) { h ^= c; h = Math.imul(h, 0x01000193) >>> 0; } return h >>> 0; }
function embed(t) {
  const v = new Array(256).fill(0);
  for (const tok of fold(t.toLowerCase()).split(/[^\p{L}\p{N}]+/u)) { if ([...tok].length < 3) continue; v[fnv(tok) % 256]++; }
  const n = Math.sqrt(v.reduce((a, x) => a + x * x, 0)) || 1; if (n === 1 && v.every(x => x === 0)) v[0] = 1;
  return v.map(x => x / n);
}
const ANSWER = [
  'La borrasca Ciro ha sido la noticia dominante de las últimas horas, pero cada medio la cuenta con un énfasis distinto. ',
  'Las agencias y los medios de centro se centran en los datos: alerta roja en la costa gallega, rachas de hasta 130 km/h y vuelos cancelados [1][2]. ',
  'Los medios de izquierda y centro-izquierda ponen el foco en la prevención y en los recursos de los servicios de emergencias, y vinculan el temporal al debate sobre los recortes [3][4]. ',
  'En los medios de centro-derecha y derecha predomina la crítica a la coordinación de las administraciones y a la tardanza de los avisos a los vecinos [5][6]. ',
  'Un medio de verificación, además, desmiente un bulo que circulaba por WhatsApp sobre vientos de 200 km/h en Madrid [7]. ',
  'En conjunto, hay acuerdo en los hechos (alerta roja, cortes de luz) y diferencias en quién se responsabiliza de la respuesta.',
];
const fake = http.createServer((req, res) => {
  let body = '';
  req.on('data', c => body += c);
  req.on('end', () => {
    const j = (o, c = 200) => { res.writeHead(c, { 'content-type': 'application/json' }); res.end(JSON.stringify(o)); };
    if (req.url.startsWith('/v1/models')) return j({ object: 'list', data: [{ id: 'qwen2.5:7b', object: 'model' }, { id: 'nomic-embed-text', object: 'model' }] });
    if (req.url.startsWith('/v1/embeddings')) {
      let q = {}; try { q = JSON.parse(body); } catch (e) {}
      const inp = Array.isArray(q.input) ? q.input : [q.input || ''];
      return j({ object: 'list', model: q.model, data: inp.map((t, i) => ({ object: 'embedding', index: i, embedding: embed(String(t)) })) });
    }
    if (req.url.startsWith('/v1/chat/completions')) {
      let q = {}; try { q = JSON.parse(body); } catch (e) {}
      if (!q.stream) return j({ choices: [{ message: { role: 'assistant', content: ANSWER.join('') } }] });
      res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
      let i = 0;
      const tick = () => {
        if (i >= ANSWER.length) { res.write('data: [DONE]\n\n'); return res.end(); }
        const words = ANSWER[i++].split(/(?<= )/);
        let k = 0;
        const w = () => { if (k >= words.length) return setTimeout(tick, 20); res.write('data: ' + JSON.stringify({ choices: [{ delta: { content: words[k++] } }] }) + '\n\n'); setTimeout(w, 12); };
        w();
      };
      return tick();
    }
    j({ error: 'not found' }, 404);
  });
});

const sleep = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  await new Promise(r => fake.listen(FAKE, '127.0.0.1', r));
  const b = await chromium.launch({ executablePath: process.env.CHROME || undefined });
  const problems = [];
  const written = [];
  // ---- discovery
  const dctx = await b.newContext({ viewport: { width: 1440, height: 900 } });
  const dp = await dctx.newPage();
  const links = async (path, sel) => { await dp.goto(BASE + path); return dp.$$eval(sel, a => a.map(x => ({ h: x.getAttribute('href'), t: x.textContent.trim().replace(/\s+/g, ' ') }))); };
  const storyLinks = await links('/encuadre?vista=comparadas', 'a[href^="/encuadre/"]');
  const story = (storyLinks.find(l => /Ciro|borrasca/i.test(l.t)) || storyLinks[0] || {}).h;
  const termLinks = await links('/tendencias', 'a[href^="/tendencias/termino/"]');
  const term = (termLinks.find(l => /Ciro/i.test(l.t)) || termLinks[0] || {}).h;
  const revLinks = await links('/cambios', 'a[href^="/articulo/"]');
  const article = (revLinks.find(l => /Ciro/i.test(l.t)) || revLinks[0] || {}).h;
  const ys = new Date(Date.now() - 86400000).toISOString().slice(0, 10);
  await dp.goto(BASE + '/capturas');
  const shotDate = (await dp.$('.shotcard')) ? '' : ys;
  const D = shotDate ? 'date=' + shotDate + '&' : '';
  console.log('discovered', { story, term, article });
  await dctx.close();

  const pages = [];
  const P = (name, fn, o = {}) => pages.push({ name, fn, ...o });
  const go = path => async p => { await p.goto(BASE + path, { waitUntil: 'load' }); };
  P('portada', go('/'), { full: true });
  P('buscar', go('/buscar?q=borrasca'), { full: true });
  P('preguntar', async p => {
    await p.goto(BASE + '/preguntar', { waitUntil: 'load' });
    await p.fill('#question', '¿Qué dicen los medios sobre la borrasca Ciro en Galicia?');
    await p.check('#compare');
    await p.click('#ask-btn');
    await p.waitForFunction(() => document.querySelector('#chat').getAttribute('aria-busy') === 'false', null, { timeout: 20000 });
    await sleep(300);
    await p.evaluate(() => { const s = document.querySelector('.turn'); if (s) s.scrollIntoView({ block: 'start' }); });
  });
  P('tendencias', go('/tendencias'), { full: true });
  if (term) P('tendencias-termino', go(term), { full: true });
  P('puntos-ciegos', go('/encuadre'), { full: true });
  P('encuadre', go('/encuadre?vista=comparadas'), { full: true });
  if (story) P('encuadre-historia', go(story), { full: true });
  P('cambios', go('/cambios'), { full: true });
  P('resumen', go('/resumen'), { full: true });
  P('seguimientos', go('/seguimientos'), { full: true });
  P('tono', go('/anotaciones'), { full: true });
  P('medios', go('/medios'), { full: true });
  P('medio-ficha', go('/medios/elpais'), { full: true });
  P('medios-comparar', go('/medios/comparar?a=eldiario&b=elpais&c=elmundo&d=abc'), { full: true });
  P('capturas', go('/capturas' + (shotDate ? '?date=' + shotDate : '')), { full: true });
  P('capturas-comparar', go('/capturas?' + D + 'mode=compare&hour=09'), { full: true });
  P('capturas-visor', async p => { await p.goto(BASE + '/capturas' + (shotDate ? '?date=' + shotDate : ''), { waitUntil: 'load' }); await p.click('a[data-viewer=grid]'); await sleep(600); }, { schemes: ['light'] });
  const cmpOpen = async p => { await p.goto(BASE + '/capturas/elpais', { waitUntil: 'load' }); await p.waitForSelector('#cmp'); await sleep(600); };
  P('capturas-slider', async p => {
    await cmpOpen(p);
    await p.evaluate(() => { const r = document.getElementById('cmp-pos'); r.value = 50; r.dispatchEvent(new Event('input', { bubbles: true })); });
    await p.evaluate(() => document.getElementById('stage-slider').scrollIntoView({ block: 'start' }));
    await sleep(500);
  });
  P('capturas-diferencias', async p => {
    await cmpOpen(p);
    await p.click('.cmp-tabs [data-modo=diff]');
    await p.waitForFunction(() => /cambiado/.test(document.getElementById('cmp-pct').textContent), null, { timeout: 20000 });
    await sleep(500);
  }, { full: true });
  P('capturas-timelapse', async p => { await cmpOpen(p); await p.click('.cmp-tabs [data-modo=timelapse]'); await sleep(700); });
  P('portadas', go('/portadas'), { full: true });
  P('estado', go('/estado'), { full: true });
  P('mas', go('/mas'));
  P('api-docs', go('/api/docs'), { full: true });
  if (article) P('articulo', async p => { await p.goto(BASE + article, { waitUntil: 'load' }); await sleep(600); }, { full: true });

  for (const scheme of ['light', 'dark']) {
    for (const [dev, vp, mobile] of [['desktop', { width: 1440, height: 900 }, false], ['mobile', { width: 390, height: 844 }, true]]) {
      const ctx = await b.newContext({ viewport: vp, colorScheme: scheme, deviceScaleFactor: mobile ? 2 : 1, isMobile: mobile, hasTouch: mobile, locale: 'es-ES', timezoneId: 'Europe/Madrid' });
      for (const pg of pages) {
        if (ONLY && !pg.name.includes(ONLY)) continue;
        if (pg.schemes && !pg.schemes.includes(scheme)) continue;
        const p = await ctx.newPage();
        const errs = [];
        p.on('console', m => { if (m.type() === 'error') errs.push('console: ' + m.text()); });
        p.on('pageerror', e => errs.push('pageerror: ' + e));
        p.on('requestfailed', r => errs.push('requestfailed: ' + r.url() + ' ' + (r.failure() || {}).errorText));
        p.on('response', r => { if (r.status() >= 400) errs.push('http ' + r.status() + ': ' + r.url()); });
        try {
          await pg.fn(p);
          await p.evaluate(async () => { for (const i of document.images) i.loading = 'eager'; await Promise.race([Promise.all([...document.images].map(i => i.complete ? 1 : new Promise(r => { i.onload = i.onerror = r; }))), new Promise(r => setTimeout(r, 8000))]); });
          await sleep(350);
          const file = `${OUT}/${pg.name}-${dev}-${scheme}.png`;
          if (pg.full && !mobile) {
            const h = await p.evaluate(() => Math.max(document.documentElement.scrollHeight, document.body.scrollHeight));
            await p.screenshot({ path: file, fullPage: true, clip: { x: 0, y: 0, width: 1440, height: Math.min(h, 2400) } });
          } else {
            await p.screenshot({ path: file });
          }
          written.push(file);
        } catch (e) { errs.push('FAILED: ' + String(e).split('\n')[0]); }
        if (errs.length) problems.push(`${pg.name} ${dev} ${scheme}: ${[...new Set(errs)].join(' | ')}`);
        await p.close();
      }
      await ctx.close();
    }
  }
  await b.close();
  fake.close();
  console.log('written', written.length);
  console.log(problems.length ? 'PROBLEMS:\n' + problems.join('\n') : 'no console/network problems');
})().catch(e => { console.error(e); process.exit(1); });
