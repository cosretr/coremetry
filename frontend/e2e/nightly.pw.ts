// UI nightly — 2026-11-21. Her tema × ~10 üst rota: Web Vitals (LCP, INP,
// CLS) + TTFI (1.5 s bütçesine karşı) + sayfa düzeyinde axe (color-contrast
// AÇIK). RAPOR-ÖNCE: bütçe aşımı ve axe ihlali yalnız raporlanır
// (out/summary.md, out/vitals.json); test YALNIZ çökmede kırmızı:
// gezinme hatası, belge 5xx, `crash` olayı ya da #main'in hiç dolmaması.
//
// Ölçüm tanımları (Chromium, tek koşu — eğilim sinyali, mutlak değil):
//   - TTFI: gezinme başlangıcından #main'in ilk kez bir eleman çocuğu
//     taşıdığı kareye (uygulama kabuğu + sayfa çizildi, etkileşime açık).
//   - LCP: son `largest-contentful-paint` girdisi.
//   - CLS: girdisiz `layout-shift` toplamı (oturum penceresiz, üst sınır).
//   - INP: yükleme sonrası sentetik etkileşimler (Tab + #main tıklaması);
//     `event` girdilerinin interactionId'li en uzunu. Gerçek kullanıcı
//     INP'si değil — "ilk etkileşim ana iş parçacığında ne kadar bekler".
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const OUT = join(dirname(fileURLToPath(import.meta.url)), 'out');
const TTFI_BUDGET_MS = 1500; // CLAUDE.md "TTFI < 1.5s fresh tab"
const LCP_GOOD_MS = 2500;
const CLS_GOOD = 0.1;
const INP_GOOD_MS = 200;

// Kayıtta ciCell:true temalar (src/lib/themeRegistry.ts) — design-axe CI
// hücreleriyle aynı küme. UI_NIGHTLY_THEMES ile daraltılabilir.
const THEMES = (process.env.UI_NIGHTLY_THEMES || 'dark,light,redhat,contrast').split(',').map(s => s.trim()).filter(Boolean);
const ROUTES = [
  '/', '/services', '/traces', '/logs', '/problems',
  '/topology', '/metrics', '/endpoints', '/databases', '/dashboards',
];
const EMAIL = process.env.COREMETRY_E2E_EMAIL || 'admin@coremetry.local';
const PASSWORD = process.env.COREMETRY_E2E_PASSWORD || 'admin';

type Row = {
  theme: string; route: string; ttfiMs: number | null; lcpMs: number | null; cls: number | null; inpMs: number | null;
  axe: { violations: number; contrast: number; ids: string[] } | null; pageErrors: string[];
};
const rows: Row[] = [];

// Yükleme ÖNCESİ kurulur: tampon gözlemciler + #main doluluk saati.
const VITALS_INIT = () => {
  const w = window as unknown as { __cm: { lcp: number | null; cls: number; inp: number; ttfi: number | null } };
  w.__cm = { lcp: null, cls: 0, inp: 0, ttfi: null };
  try {
    new PerformanceObserver(l => { for (const e of l.getEntries()) w.__cm.lcp = e.startTime; })
      .observe({ type: 'largest-contentful-paint', buffered: true });
    new PerformanceObserver(l => {
      for (const e of l.getEntries() as Array<PerformanceEntry & { value: number; hadRecentInput: boolean }>) {
        if (!e.hadRecentInput) w.__cm.cls += e.value;
      }
    }).observe({ type: 'layout-shift', buffered: true });
    new PerformanceObserver(l => {
      for (const e of l.getEntries() as Array<PerformanceEntry & { interactionId?: number }>) {
        if (e.interactionId) w.__cm.inp = Math.max(w.__cm.inp, e.duration);
      }
    }).observe({ type: 'event', buffered: true, durationThreshold: 16 } as PerformanceObserverInit);
  } catch { /* gözlemci desteklenmiyorsa alan null kalır */ }
  const tick = () => {
    const m = document.getElementById('main');
    if (m && m.firstElementChild) { w.__cm.ttfi = performance.now(); return; }
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
};

async function login(page: Page) {
  const r = await page.request.post('/api/auth/login', { data: { email: EMAIL, password: PASSWORD } });
  expect(r.ok(), `login ${r.status()}`).toBe(true);
}

for (const theme of THEMES) {
  test.describe(`theme=${theme}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript(t => { try { localStorage.setItem('coremetry-theme', t); } catch { /* yok */ } }, theme);
      await page.addInitScript(VITALS_INIT);
      await login(page);
    });

    for (const route of ROUTES) {
      test(`${route}`, async ({ page }) => {
        const pageErrors: string[] = [];
        let crashed = false;
        page.on('pageerror', e => pageErrors.push(String(e.message).slice(0, 200)));
        page.on('crash', () => { crashed = true; });

        const resp = await page.goto(route, { waitUntil: 'domcontentloaded' });
        // ÇÖKME kapıları — bunlar geceyi kırar.
        expect(resp, 'gezinme cevabı yok').not.toBeNull();
        expect(resp!.status(), 'belge 5xx').toBeLessThan(500);
        await page.waitForFunction(() => (window as unknown as { __cm: { ttfi: number | null } }).__cm?.ttfi != null, null, { timeout: 30_000 });
        expect(crashed, 'sayfa çöktü').toBe(false);

        // Sentetik etkileşim (INP) + LCP'nin oturması için kısa bekleme.
        await page.keyboard.press('Tab');
        await page.locator('#main').click({ position: { x: 5, y: 5 }, force: true }).catch(() => {});
        await page.waitForTimeout(1500);
        const v = await page.evaluate(() => (window as unknown as { __cm: { lcp: number | null; cls: number; inp: number; ttfi: number | null } }).__cm);

        let axe: Row['axe'] = null;
        try {
          const res = await new AxeBuilder({ page })
            .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
            .options({ rules: { 'color-contrast': { enabled: true } } })
            .analyze();
          axe = {
            violations: res.violations.reduce((n, x) => n + x.nodes.length, 0),
            contrast: res.violations.find(x => x.id === 'color-contrast')?.nodes.length ?? 0,
            ids: res.violations.map(x => `${x.id}×${x.nodes.length}`),
          };
        } catch { axe = null; } // axe hatası rapor edilir, gece kırılmaz

        rows.push({
          theme, route,
          ttfiMs: v.ttfi != null ? Math.round(v.ttfi) : null,
          lcpMs: v.lcp != null ? Math.round(v.lcp) : null,
          cls: Math.round(v.cls * 1000) / 1000,
          inpMs: v.inp ? Math.round(v.inp) : null,
          axe, pageErrors,
        });
      });
    }
  });
}

const flag = (bad: boolean) => (bad ? ' ⚠' : '');

test.afterAll(() => {
  mkdirSync(OUT, { recursive: true });
  writeFileSync(join(OUT, 'vitals.json'), JSON.stringify({ budgets: { TTFI_BUDGET_MS, LCP_GOOD_MS, CLS_GOOD, INP_GOOD_MS }, rows }, null, 2));
  const over = rows.filter(r => r.ttfiMs != null && r.ttfiMs > TTFI_BUDGET_MS).length;
  const lines = [
    '## UI nightly (report-only)', '',
    `Rows: ${rows.length} · TTFI > ${TTFI_BUDGET_MS} ms: ${over} · axe violations (nodes): ${rows.reduce((n, r) => n + (r.axe?.violations ?? 0), 0)}`, '',
    '| theme | route | TTFI ms | LCP ms | CLS | INP ms | axe (contrast) | page errors |',
    '|---|---|---:|---:|---:|---:|---:|---:|',
    ...rows.map(r => `| ${r.theme} | \`${r.route}\` | ${r.ttfiMs ?? '–'}${flag((r.ttfiMs ?? 0) > TTFI_BUDGET_MS)} | ${r.lcpMs ?? '–'}${flag((r.lcpMs ?? 0) > LCP_GOOD_MS)} | ${r.cls ?? '–'}${flag((r.cls ?? 0) > CLS_GOOD)} | ${r.inpMs ?? '–'}${flag((r.inpMs ?? 0) > INP_GOOD_MS)} | ${r.axe ? `${r.axe.violations} (${r.axe.contrast})` : 'axe failed'} | ${r.pageErrors.length} |`),
    '', 'Axe rule ids per row are in `vitals.json`. ⚠ = over budget / "good" threshold; nothing here fails the run.',
  ];
  writeFileSync(join(OUT, 'summary.md'), lines.join('\n') + '\n');
});
