// @vitest-environment jsdom
//
// v0.10.2388 — Problem detayı "Hata nerede" bölümü. Sözleşme:
//   - dört blok yalnız verisi varsa çizilir; verisi olmayan blok YOKTUR
//     (sıfırlarla dolu tablo çizilmez), okunamayan blok başlığıyla "okunamadı"
//     yazar ve ötekiler çizilir;
//   - renk yalnız sapan değerde; tabanı olmayan rotada "normal bilinmiyor";
//   - sürüm etiketli seri yoksa sürüm ORANI yazılmaz, yalnız deploy gerçeği;
//   - bağlantılar mevcut href yardımcılarından (exception grubu, ML olayı);
//   - istek düşerse bölüm tek satır "okunamadı", konu dışı yanıtta hiçbir şey.
// Saat ve saat dilimi bağımsız: saat biçimi bileşene dışarıdan verilir, bölüm
// testinde pencere uzunluğu (dakika) okunur.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Problem, ProblemBreakdownResponse } from '@/lib/types';
import type { Lang } from '@/lib/i18n';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr', res: null as unknown, fail: false, calls: 0 }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, {
    get: (_t, k: string) => (k === 'problemBreakdown'
      ? async () => { m.calls++; if (m.fail) throw new Error('boom'); return m.res; }
      : () => Promise.resolve(null)),
  }),
  isCanceled: () => false,
}));

import { ProblemBreakdownBlocks } from '@/components/coremetry/problems/ProblemBreakdownBlocks';
import { loadEnCatalog, tf } from '@/lib/i18n';
import ProblemBreakdownSection from './ProblemBreakdownSection';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = 1_795_000_000e9; // sabit an (unix ns)
const FULL: ProblemBreakdownResponse = {
  problemId: 'p1', applicable: true, service: 'orders', open: true, windowFrom: T0, windowTo: T0 + 12 * 60e9,
  endpoints: {
    rows: [
      { route: 'POST /orders/assessment', normal: 0.003, current: 0.182, failedPerMin: 36.4, requestsPerMin: 200, deviates: true },
      { route: 'POST /orders', normal: 0.03, current: 0.031, failedPerMin: 9.3, requestsPerMin: 300 },
      { route: 'GET /orders/new', current: 0.5, failedPerMin: 2, requestsPerMin: 4 },
      { route: '', normal: 0, current: 0.02, failedPerMin: 0.02, requestsPerMin: 1 },
    ],
    other: { route: '', normal: 0.004, current: 0.004, failedPerMin: 2, requestsPerMin: 560 }, otherCount: 11,
    failedPerMin: 49.72, requestsPerMin: 1065,
  },
  versions: {
    label: 'service_version',
    rows: [
      { version: 'v2.14.1', current: 0.071, failedPerMin: 42.6, requestsPerMin: 600, deviates: true },
      { version: 'v2.14.0', current: 0.003, failedPerMin: 0.6, requestsPerMin: 200 },
    ],
  },
  exceptions: {
    total: 1000, otherTypes: 2, otherCount: 10, otherShare: 0.01,
    rows: [
      { type: 'AppException', count: 940, share: 0.94, message: 'Kayıt bulunamadı', fingerprint: 'fp-app', groups: 2 },
      { type: 'TimeoutException', count: 50, share: 0.05 },
    ],
  },
  dependencies: {
    rows: [
      { eventId: 'ev-1', family: 'db', target: 'customer-rel', measure: 'error_rate', baseline: 0.002, observed: 0.31, startedAt: T0, lastSeen: T0 + 600e9, live: true },
      { eventId: 'ev-2', family: 'http', target: 'checkout:8443', measure: 'latency', baseline: 40, observed: 1250, startedAt: T0 + 60e9, lastSeen: T0 + 300e9, live: false },
    ],
  },
};
const PROBLEM = { id: 'p1', service: 'orders', metric: 'error_rate', status: 'open', startedAt: T0,
  recentDeploy: { version: 'v2.14.1', timeUnixNs: T0 - 372e9, ageSeconds: 372 } } as unknown as Problem;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
  await settle();
}
const q = (sel: string) => host!.querySelector(sel);
const qa = (sel: string) => Array.from(host!.querySelectorAll(sel));
const text = () => host!.textContent ?? '';
const blocks = (data: ProblemBreakdownResponse, lang: Lang = 'tr', deploy = { version: 'v2.14.1', minutes: 6 } as { version: string; minutes: number } | null) =>
  mount(<ProblemBreakdownBlocks data={data} deploy={deploy} lang={lang} t={(k, v) => tf(k, lang, v)} clock={() => '21:03'} />);

beforeEach(() => { m.lang = 'tr'; m.res = FULL; m.fail = false; m.calls = 0; });
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

describe('ProblemBreakdownBlocks', () => {
  it('dört blok da verisiyle çizilir', async () => {
    await blocks(FULL);
    for (const id of ['endpoints', 'versions', 'exceptions', 'dependencies']) expect(q(`[data-testid="pbd-${id}"]`)).not.toBeNull();
    expect(q('[data-testid="pbd-failed"]')).toBeNull();
    // Endpoint: sunucunun sırası, 4 satır + "Diğer N".
    const rows = qa('[data-testid="pbd-route-row"]');
    expect(rows.map(r => r.querySelector('td')!.textContent)).toEqual(['POST /orders/assessment', 'POST /orders', 'GET /orders/new', '(rota etiketi yok)']);
    expect(rows[0].textContent).toContain('%0,3');
    expect(rows[0].textContent).toContain('%18');
    expect(rows[0].textContent).toContain('36');
    expect(rows[2].textContent).toContain('normal bilinmiyor');
    expect(rows[3].textContent).toContain('<0,1'); // küçük ama sıfır değil
    expect(q('[data-testid="pbd-route-other"]')!.textContent).toContain('Diğer 11 endpoint');
    expect(q('[data-testid="pbd-endpoints"]')!.textContent).toContain('örneklenmemiş sunucu metriği');
    expect(q('[data-testid="pbd-normal-failed"]')).toBeNull();
  });

  it('renk yalnız sapan değerde', async () => {
    await blocks(FULL);
    const cur = qa('[data-testid="pbd-route-current"]');
    // 4 rota + "Diğer" satırı: yalnız eşiği aşan ilk rota renkli.
    expect(cur.map(c => c.classList.contains('cell-err'))).toEqual([true, false, false, false, false]);
    expect(q('[data-testid="pbd-route-other"] [data-testid="pbd-route-current"]')!.classList.contains('cell-err')).toBe(false);
    // Başka hiçbir endpoint hücresi renkli değil.
    expect(qa('[data-testid="pbd-endpoints"] .cell-err')).toHaveLength(1);
    expect(qa('[data-testid="pbd-version"] .cell-err')).toHaveLength(1);
  });

  it('sürüm kartları oranı ve trafiği yazar; deploy gerçeği altında', async () => {
    await blocks(FULL);
    const cards = qa('[data-testid="pbd-version"]').map(c => c.textContent);
    expect(cards[0]).toContain('v2.14.1');
    expect(cards[0]).toContain('%7,1');
    expect(cards[0]).toContain('600 istek / dk');
    expect(cards[1]).toContain('%0,3');
    expect(q('[data-testid="pbd-versions"]')!.textContent).toContain('etiket service_version');
    expect(q('[data-testid="pbd-deploy"]')!.textContent).toBe('v2.14.1 sürümünden 6 dk sonra başladı.');
  });

  it('sürüm etiketli seri yoksa oran yazılmaz: yalnız deploy gerçeği; o da yoksa blok yok', async () => {
    await blocks({ ...FULL, versions: undefined });
    expect(qa('[data-testid="pbd-version"]')).toHaveLength(0);
    expect(q('[data-testid="pbd-versions"]')!.textContent).toBe('Sürümv2.14.1 sürümünden 6 dk sonra başladı.');
    act(() => { root?.unmount(); }); host?.remove();
    await blocks({ ...FULL, versions: undefined }, 'tr', null);
    expect(q('[data-testid="pbd-versions"]')).toBeNull();
  });

  it('exception: pay ve sayı; grup varsa grup sayfasına bağlantı, yoksa düz metin', async () => {
    await blocks(FULL);
    const rows = qa('[data-testid="pbd-ex-row"]');
    expect(rows).toHaveLength(2);
    expect(rows[0].querySelector('a')!.getAttribute('href')).toBe('/problems?exc=fp-app');
    expect(rows[0].textContent).toContain('%94');
    expect(rows[0].textContent).toContain('940');
    expect(rows[0].textContent).toContain('Kayıt bulunamadı');
    expect(rows[0].textContent).toContain('2 grup');
    expect(rows[1].querySelector('a')).toBeNull();
    expect(q('[data-testid="pbd-ex-other"]')!.textContent).toContain('Diğer 2 tür');
    expect(q('[data-testid="pbd-exceptions"]')!.textContent).toContain('hata durumlu span kayıtları (örneklenmez) · 1.000 kayıt');
  });

  it('exception: tür tavanı dolduysa pay yazılmaz ve bunu söyler', async () => {
    await blocks({ ...FULL, exceptions: { capped: true, rows: [{ type: 'AppException', count: 940 }] } });
    const row = q('[data-testid="pbd-ex-row"]')!;
    expect(row.textContent).toContain('940');
    expect(row.textContent).not.toContain('%');
    expect(q('[data-testid="pbd-exceptions"]')!.textContent).toContain('pay yazılmadı');
  });

  it('bağımlılık: ML olayına bağlantı, ölçünün biriminde değer, neden iddiası yok', async () => {
    await blocks(FULL);
    const rows = qa('[data-testid="pbd-dep-row"]');
    expect(rows[0].querySelector('a')!.getAttribute('href')).toBe('/anomalies/ml/ev-1');
    expect(rows[0].textContent).toContain('customer-rel');
    expect(rows[0].textContent).toContain('veritabanı');
    expect(rows[0].textContent).toContain('Hata oranı');
    expect(rows[0].textContent).toContain('%0,2');
    expect(rows[0].textContent).toContain('%31');
    expect(rows[0].textContent).toContain('21:03');
    expect(rows[0].textContent).toContain('sürüyor');
    expect(rows[1].textContent).toContain('40 ms');
    expect(rows[1].textContent).toContain('1,3 sn');
    expect(rows[1].textContent).not.toContain('sürüyor');
    expect(q('[data-testid="pbd-dep-note"]')!.getAttribute('aria-label')).toContain('kanıtlanmış neden değil');
  });

  it('bir blok eksik: o blok çizilmez, ötekiler yerinde', async () => {
    await blocks({ ...FULL, exceptions: undefined }, 'tr', null);
    expect(q('[data-testid="pbd-exceptions"]')).toBeNull();
    for (const id of ['endpoints', 'versions', 'dependencies']) expect(q(`[data-testid="pbd-${id}"]`)).not.toBeNull();
    expect(text()).not.toContain("Exception'a göre");
  });

  it('bir blok okunamadı: başlığıyla "okunamadı", ötekiler çizilir', async () => {
    await blocks({ ...FULL, endpoints: { failed: true, rows: [], failedPerMin: 0, requestsPerMin: 0 }, dependencies: { failed: true, rows: [] } });
    const ep = q('[data-testid="pbd-endpoints"]')!;
    expect(ep.textContent).toBe("Endpoint'e göreokunamadı");
    expect(ep.querySelector('table')).toBeNull();
    expect(q('[data-testid="pbd-dependencies"]')!.textContent).toBe('Bağımlılığa göreokunamadı');
    expect(qa('[data-testid="pbd-ex-row"]')).toHaveLength(2);
  });

  it('rota tabanı okunamadıysa tablo çizilir ve ayrıca söylenir', async () => {
    await blocks({ ...FULL, endpoints: { ...FULL.endpoints!, normalFailed: true } });
    expect(q('[data-testid="pbd-normal-failed"]')!.textContent).toBe('Normal değerler okunamadı.');
    expect(qa('[data-testid="pbd-route-row"]')).toHaveLength(4);
  });

  it('İngilizce arayüz: Türkçe etiket yok, sayı biçimi İngilizce', async () => {
    await loadEnCatalog();
    await blocks(FULL, 'en');
    const s = text();
    for (const trLabel of ['göre', 'Diğer', 'bilinmiyor', 'okunamadı', 'kayıt', 'sürüyor', 'istek', 'örneklenmemiş', 'sürümünden', 'veritabanı', 'Olayda', 'Başlangıç', 'Pay']) {
      expect(s).not.toContain(trLabel);
    }
    expect(s).toContain('By endpoint');
    expect(s).toContain('Other 11 endpoints');
    expect(s).toContain('18%');
    expect(s).toContain('normal unknown');
    expect(s).toContain('Started 6 min after version v2.14.1.');
    expect(s).toContain('1.3 s');
    // v0.10.2487 (dilim 3) — kaynak açıklaması (?) balonunun erişilebilir adında.
    expect(q('[data-testid="pbd-dep-note"]')!.getAttribute('aria-label')).toContain('not a proven cause');
  });
});

describe('ProblemBreakdownSection', () => {
  it('yanıt gelince bölüm başlığı + pencere + bloklar', async () => {
    await mount(<ProblemBreakdownSection problem={PROBLEM} />);
    expect(m.calls).toBe(1);
    expect(q('.pb-sect > .h')!.textContent).toBe('Hata neredeşimdi · son 12 dk');
    expect(q('[data-testid="pbd-blocks"]')).not.toBeNull();
    expect(q('[data-testid="pbd-deploy"]')!.textContent).toContain('6 dk sonra');
  });

  it('kapanmış Problem: başlık "Problem sırasında", sütun "Problemde"', async () => {
    m.res = { ...FULL, open: false, windowTo: T0 + 40 * 60e9 };
    await mount(<ProblemBreakdownSection problem={PROBLEM} />);
    expect(q('.pb-sect > .h')!.textContent).toBe('Hata neredeProblem sırasında · 40 dk');
    expect(q('[data-testid="pbd-endpoints"] thead')!.textContent).toContain('Problemde');
    expect(q('[data-testid="pbd-endpoints"] thead')!.textContent).not.toContain('Şimdi');
  });

  it('istek düşer: tek satır "okunamadı", blok yok', async () => {
    m.fail = true;
    await mount(<ProblemBreakdownSection problem={PROBLEM} />);
    expect(q('[data-testid="pbd-load-error"]')!.textContent).toBe('Kırılımlar okunamadı.');
    expect(q('[data-testid="pbd-blocks"]')).toBeNull();
  });

  it('konu dışı ya da bloksuz yanıt: hiçbir şey çizilmez', async () => {
    m.res = { problemId: 'p1', applicable: false };
    await mount(<ProblemBreakdownSection problem={PROBLEM} />);
    expect(host!.innerHTML).toBe('');
    act(() => { root?.unmount(); }); host?.remove();
    m.res = { problemId: 'p1', applicable: true, windowFrom: T0, windowTo: T0 + 300e9 };
    await mount(<ProblemBreakdownSection problem={PROBLEM} />);
    expect(host!.innerHTML).toBe(''); // deploy gerçeği tek başına bölüm açmaz
  });
});
