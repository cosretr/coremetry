// e2e/shot.mjs — yerel Coremetry sayfasının iki temada tam sayfa görüntüsü. YALNIZ YEREL.
// Doğrudan değil scripts/dev-shot.sh ile çağrılır (oturum + playwright-core çözümlemesi orada).
//
// Ortam: SHOT_PW_CORE (playwright-core dizini), SHOT_BASE (http://localhost:8088),
// SHOT_PATH (/services?range=1h), SHOT_NAME, SHOT_JAR (curl çerez kavanozu),
// SHOT_OUT (e2e/shots), SHOT_THEMES ("redhat dark"), SHOT_ALLOW_CONSOLE ("1" → gevşek).
// Tema: frontend/index.html açılış betiği localStorage 'coremetry-theme' okuyup
// <html data-theme> yazar; burada sayfa yüklenmeden önce aynı anahtar yazılır.
// Fark: önceki PNG ile tarayıcı canvas'ında piksel karşılaştırması (ek bağımlılık yok).
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

const env = process.env;
const require = createRequire(import.meta.url);
const { chromium } = require(env.SHOT_PW_CORE);
const base = env.SHOT_BASE || 'http://localhost:8088';
if (!/^https?:\/\/(localhost|127\.0\.0\.1)(:\d+)?$/.test(base)) {
  console.error(`yalnız localhost: ${base}`);
  process.exit(2);
}
const target = base + env.SHOT_PATH;
const out = env.SHOT_OUT;
const name = env.SHOT_NAME;
const themes = (env.SHOT_THEMES || 'redhat dark').split(/\s+/).filter(Boolean);
fs.mkdirSync(out, { recursive: true });

function readJar(file) {
  const cookies = [];
  for (let line of fs.readFileSync(file, 'utf8').split('\n')) {
    let httpOnly = false;
    if (line.startsWith('#HttpOnly_')) { httpOnly = true; line = line.slice('#HttpOnly_'.length); }
    if (!line || line.startsWith('#')) continue;
    const f = line.split('\t');
    if (f.length < 7) continue;
    const c = { name: f[5], value: f[6], domain: f[0].replace(/^\./, ''), path: f[2], httpOnly, secure: f[3] === 'TRUE', sameSite: 'Lax' };
    const exp = Number(f[4]);
    if (exp > 0) c.expires = exp;
    cookies.push(c);
  }
  return cookies;
}

async function diffRatio(browser, prevFile, curFile) {
  const page = await browser.newPage();
  try {
    const a = 'data:image/png;base64,' + fs.readFileSync(prevFile).toString('base64');
    const b = 'data:image/png;base64,' + fs.readFileSync(curFile).toString('base64');
    return await page.evaluate(async ([a, b]) => {
      const load = (src) => new Promise((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = bad; i.src = src; });
      const [ia, ib] = await Promise.all([load(a), load(b)]);
      const w = Math.max(ia.width, ib.width), h = Math.max(ia.height, ib.height);
      const px = (img) => {
        const c = document.createElement('canvas'); c.width = w; c.height = h;
        const x = c.getContext('2d'); x.drawImage(img, 0, 0);
        return { d: x.getImageData(0, 0, w, h).data, iw: img.width, ih: img.height };
      };
      const A = px(ia), B = px(ib);
      let changed = 0;
      for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) {
        const inA = x < A.iw && y < A.ih, inB = x < B.iw && y < B.ih;
        if (inA !== inB) { changed++; continue; }
        const i = (y * w + x) * 4;
        if (Math.abs(A.d[i] - B.d[i]) > 8 || Math.abs(A.d[i + 1] - B.d[i + 1]) > 8 || Math.abs(A.d[i + 2] - B.d[i + 2]) > 8) changed++;
      }
      return changed / (w * h);
    }, [a, b]);
  } finally {
    await page.close();
  }
}

const browser = await chromium.launch({ headless: true });
let exit = 0;
try {
  for (const theme of themes) {
    const t0 = Date.now();
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
    await ctx.addCookies(readJar(env.SHOT_JAR));
    await ctx.addInitScript((t) => { try { localStorage.setItem('coremetry-theme', t); } catch { /* yok */ } }, theme);
    const page = await ctx.newPage();
    const consoleErrors = [], failed = [];
    page.on('console', (m) => { if (m.type() === 'error') consoleErrors.push({ text: m.text().slice(0, 500), at: m.location()?.url || '' }); });
    page.on('pageerror', (e) => consoleErrors.push({ text: String(e).slice(0, 500), at: 'pageerror' }));
    page.on('requestfailed', (r) => failed.push({ url: r.url(), method: r.method(), status: 0, error: r.failure()?.errorText || 'failed' }));
    page.on('response', (r) => { if (r.status() >= 400) failed.push({ url: r.url(), method: r.request().method(), status: r.status() }); });

    let idle = 'networkidle';
    try {
      await page.goto(target, { waitUntil: 'networkidle', timeout: 30000 });
    } catch {
      idle = 'networkidle-timeout(30s)';
      await page.waitForLoadState('load').catch(() => {});
    }
    await page.waitForTimeout(800); // grafik çizimi / geçişler
    const finalUrl = page.url();
    const themeAttr = await page.evaluate(() => document.documentElement.getAttribute('data-theme'));
    const loginPage = /\/login(\b|$|\?)/.test(new URL(finalUrl).pathname);

    // Uygulama kabuğu içeriği iç kaydırıcıda tutar (belge 900px kalır); "tam sayfa"
    // için en uzun iç kaydırıcının taşmasını görüntü yüksekliğine ekle (üst sınır 8000px).
    const extra = await page.evaluate(() => {
      let m = 0;
      for (const el of document.querySelectorAll('*')) {
        const oy = getComputedStyle(el).overflowY;
        if ((oy === 'auto' || oy === 'scroll') && el.clientHeight > 200) m = Math.max(m, el.scrollHeight - el.clientHeight);
      }
      return m;
    });
    if (extra > 0) {
      await page.setViewportSize({ width: 1440, height: Math.min(900 + extra, 8000) });
      await page.waitForTimeout(600);
    }
    const png = path.join(out, `${name}-${theme}.png`);
    const prev = path.join(out, `${name}-${theme}.prev.png`);
    const hadPrev = fs.existsSync(png);
    if (hadPrev) fs.renameSync(png, prev);
    await page.screenshot({ path: png, fullPage: true });
    const dims = await page.evaluate(() => [document.documentElement.scrollWidth, document.documentElement.scrollHeight]);
    await ctx.close();
    const ratio = hadPrev ? await diffRatio(browser, prev, png) : null;
    const ms = Date.now() - t0;

    const report = { url: target, finalUrl, theme, themeAttr, loginPage, idle, consoleErrors, failedRequests: failed, diffRatio: ratio, ms, takenAt: new Date().toISOString() };
    fs.writeFileSync(path.join(out, `${name}-${theme}.json`), JSON.stringify(report, null, 2));
    const bytes = fs.statSync(png).size;
    console.log(`${theme.padEnd(7)} ${png}  ${dims[0]}x${dims[1]} ${(bytes / 1024).toFixed(0)}KB  console_errors=${consoleErrors.length}  failed_requests=${failed.length}  diff=${ratio === null ? 'n/a(no prev)' : (ratio * 100).toFixed(2) + '%'}  ${ms}ms  ${idle}${themeAttr !== theme ? `  THEME-MISMATCH(data-theme=${themeAttr})` : ''}${loginPage ? '  LOGIN-PAGE' : ''}`);
    if (loginPage || themeAttr !== theme) exit = 1;
    if (consoleErrors.length > 0 && env.SHOT_ALLOW_CONSOLE !== '1') exit = 1;
  }
} finally {
  await browser.close();
}
process.exit(exit);
