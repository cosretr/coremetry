// @vitest-environment jsdom
//
// MLReplaySheet.layout — v0.10.1845 (öğrenme döngüsü Faz 2, mockup
// anomaly-detail.html görünüm 2–4; audit §17 bölüm sırası).
//
// NE ÇİVİLİYOR:
//   • bölümler mockup sırasında: başlık çipleri → kalın özet → kart →
//     olayın seyri → eylemler → kanıt → bağlam → katlı teknik detay;
//   • kart etiketleri mockup'la birebir, metin SUNUCUDAN (FE cümle kurmaz);
//   • viewer etiketi GÖRÜR, düğmeler devre dışı (salt okunur, boş değil);
//   • kayıt yoksa (snapshot null) kart yine çizilir, grafik yerine not.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ExplainCard, MLReplay, MLSnapshot } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null, addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { role: 'viewer', replay: null as unknown, calls: [] as string[] };
});
vi.mock('@/lib/api', () => ({
  api: new Proxy({}, { get: (_t, k: string) => (k === 'mlReplay' ? async () => m.replay : async () => { m.calls.push(k); return null; }) }),
  isCanceled: () => false,
}));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));
// uPlot jsdom'da çizilmez (ResizeObserver/canvas yok); düzen testi grafiği değil yerleşimi ölçer.
vi.mock('@/components/charts/TimeChart', () => ({ TimeChart: () => null }));

import { MLReplayContent, ML_EVENT_SECTIONS, contextRows } from './MLReplaySheet';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const card: ExplainCard = {
  summary: 'orders-api: hata logları normalin yaklaşık 9 katı, 13 dakikadır (14:31\'den beri).',
  what: 'orders-api servisinde hata logları arttı.', howMuch: 'Son 13 dakikada 640 hata satırı; bu saatte normalde 50–90. Yaklaşık 9 kat.',
  since: 'Bugün 14:31\'den beri, 13 dakikadır sürüyor.', impact: '', whyItMatters: 'Bu servis günün her saati kullanılıyor.',
  whatChanged: [{ text: '14:20\'de yeni sürüm yayına alındı (v2.42.0).', link: { label: 'Rollouts', href: '/service?name=orders-api&tab=gitops' } }],
  whereToLook: [{ label: 'Hata logları', href: '/logs?service=orders-api' }, { label: 'Servis sayfası', href: '/service?name=orders-api' }],
  technical: { detector: 'Hata oranı (log) · Log', score: '9,4 (etkin eşik 4)', signature: 'orders-api | log | error_rate', thresholds: 'Süreklilik 2 kova (10 dk)', expected: 'Son 21 günün aynı saat dilimi', revision: '—' },
  header: { kind: 'log', detector: 'error_rate', service: 'orders-api', serviceHref: '/service?name=orders-api', open: true, status: 'Sürüyor · 13 dk', profile: 'always_on', profileLabel: '7/24' },
};
const snap: MLSnapshot = {
  eventId: 'e1', detector: 'error_rate', signal: 'log', service: 'orders-api', part: '_error', detectedAt: 1_800_000_000e9, lastSeen: 1_800_000_600e9,
  bucketSec: 300, series: '[]', baselineMed: 23, baselineMad: 4, thresholds: '{"criticalZ":4}', configHash: '', dayClass: 'weekday', dwell: '[]',
  confirmIds: null, evidence: '{}', samples: JSON.stringify([{ t: 1_800_000_100e9, traceId: 'abcdef0123456789ff', kind: 'span' }]), deploys: '[]', complete: false,
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

async function render(replay: MLReplay): Promise<HTMLElement> {
  m.replay = replay;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><MLReplayContent id="e1" /></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

describe('ML olay sayfası düzeni', () => {
  it('bölümler mockup sırasında, kart etiketleri birebir, metin sunucudan', async () => {
    const el = await render({ snapshot: snap, related: [], card, verdict: { verdict: 'anomaly', by: 'op@x' } });
    const order = ML_EVENT_SECTIONS.map(id => el.querySelector(`[data-testid="${id}"]`));
    expect(order.every(Boolean)).toBe(true);
    for (let i = 1; i < order.length; i++) {
      expect(order[i - 1]!.compareDocumentPosition(order[i]!) & Node.DOCUMENT_POSITION_FOLLOWING, ML_EVENT_SECTIONS[i]).toBeTruthy();
    }
    expect(el.querySelector('[data-testid="ml-summary"]')!.textContent).toBe(card.summary);
    const labels = [...el.querySelectorAll('[data-testid="ml-card"] dt')].map(d => d.textContent);
    expect(labels).toEqual(['Ne oldu', 'Ne kadar', 'Ne zamandan beri', 'Neden önemli', 'Yakında ne değişti', 'Nereye bakmalı']);
    expect(el.querySelector('[data-testid="ml-header"]')!.textContent).toContain('Sürüyor · 13 dk');
    expect((el.querySelector('[data-testid="ml-technical"]') as HTMLDetailsElement).open).toBe(false);
    expect(el.querySelector('[data-testid="ml-evidence"]')!.textContent).toContain('abcdef0123456789');
    expect(el.querySelector('[data-testid="ml-trace-heatmap"]')).toBeNull(); // course yok → katman yok
  });
  it('viewer etiketi görür, düğmeler devre dışı', async () => {
    m.role = 'viewer';
    const el = await render({ snapshot: snap, related: [], card, verdict: { verdict: 'anomaly', by: 'op@x' } });
    expect(el.querySelector('[data-testid="ml-verdict"]')!.textContent).toContain('gerçek ✓');
    const btns = [...el.querySelectorAll('[data-testid="ml-actions"] button')] as HTMLButtonElement[];
    expect(btns.length).toBe(2);
    expect(btns.every(b => b.disabled)).toBe(true);
  });
  it('kayıt yoksa kart yine çizilir, grafik yerine not', async () => {
    m.role = 'editor';
    const el = await render({ snapshot: null, related: [], card });
    expect(el.querySelector('[data-testid="ml-card"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="ml-course"]')!.textContent).toContain('grafik kaydı yok');
    const btns = [...el.querySelectorAll('[data-testid="ml-actions"] button')] as HTMLButtonElement[];
    expect(btns.some(b => b.disabled)).toBe(false);
  });
  it('v0.10.1870: endpoint\'li trace olayında ısı haritası katmanı olayın seyrinde, oynatıcının üstünde', async () => {
    const tc = { view: 'heatmap', service: 'notify-gateway', route: '/notify/order-status', fromNs: 1, toNs: 2, onsetNs: 1, endNs: 2, line: { ms: 620, label: 'olay öncesi p95 ≈ 620 ms' } };
    const el = await render({ snapshot: snap, related: [], card, course: { trace: tc } });
    const hm = el.querySelector('[data-testid="ml-course"] [data-testid="ml-trace-heatmap"]');
    expect(hm).not.toBeNull();
    expect(hm!.textContent).toContain('olay öncesi p95 ≈ 620 ms');
    expect(hm!.textContent).toContain("örneklenmiş span'ler");
    const player = el.querySelector('[data-testid="ml-player"]')!;
    expect(player.contains(hm)).toBe(true);
  });
  it('v0.10.1872: az çağrılan serviste ısı haritasının YERİNE nokta grafiği (ikisi birlikte yok)', async () => {
    const tc = {
      view: 'dots', service: 'branch-teller', fromNs: 1, toNs: 2, onsetNs: 1, endNs: 2,
      line: { ms: 1200, label: "alışılmış üst sınır ≈ 1,2 sn (normalde 10 çağrıdan 1'i aşar)" },
      dots: [{ t: 1000, ms: 600 }, { t: 2000, ms: 2400 }], dotsSummary: 'özet sunucudan',
    };
    const el = await render({ snapshot: snap, related: [], card, course: { trace: tc } });
    const dots = el.querySelector('[data-testid="ml-course"] [data-testid="ml-trace-dots"]');
    expect(dots).not.toBeNull();
    expect(el.querySelector('[data-testid="ml-trace-dots-summary"]')!.textContent).toBe('özet sunucudan');
    expect(dots!.textContent).toContain('alışılmış üst sınır ≈ 1,2 sn');
    expect(el.querySelector('[data-testid="ml-trace-heatmap"]')).toBeNull();
  });
  it('v0.10.1871: log olayında yığılı çubuk lejantı (mesaj kalıbı varsayılan, Seviye seçeneği)', async () => {
    const t0 = 1_800_000_000;
    const s3 = { ...snap, series: JSON.stringify([{ t: t0, v: 5 }, { t: t0 + 300, v: 60 }]) };
    const log = {
      times: [t0, t0 + 300], shadow: [7, 7],
      byPattern: [{ key: 'tpl|Connection pool exhausted', label: 'Connection pool exhausted', new: true, values: [0, 40] }, { key: '_rest', label: 'diğer hata mesajları', values: [5, 20] }],
      byLevel: [{ key: '_error', label: 'hata', values: [5, 60] }, { key: '_other', label: 'diğer seviyeler', values: [40, 50] }],
    };
    const el = await render({ snapshot: s3, related: [], card, course: { log } });
    const lg = el.querySelector('[data-testid="ml-course"] [data-testid="ml-log-legend"]');
    expect(lg).not.toBeNull();
    expect(lg!.textContent).toContain('Connection pool exhausted(yeni)');
    expect(lg!.textContent).toContain('gölge çubuk: bu kovada beklenen');
    expect([...lg!.querySelectorAll('[role="radio"]')].map(b => b.textContent)).toEqual(['Mesaj kalıbı', 'Seviye']);
    expect(el.querySelector('[data-testid="ml-trace-heatmap"]')).toBeNull();
  });
  it('v0.10.1873: log kanıtı (mesaj kalıpları) kapalı gelir, açılmadan logstore çağrısı yok', async () => {
    m.calls = [];
    const el = await render({ snapshot: snap, related: [], card });
    const det = el.querySelector('[data-testid="ml-evidence"] [data-testid="ml-evidence-patterns"]') as HTMLDetailsElement | null;
    expect(det).not.toBeNull();
    expect(det!.open).toBe(false);
    expect(m.calls).not.toContain('mlEvidence');
    expect(el.querySelector('[data-testid="ml-evidence"] h3')!.textContent).toContain('yeni ve artan mesajlar');
  });
  it('bağlam satırları: aynı servis kayıtları + yayınlar zaman sıralı', () => {
    const s2 = { ...snap, deploys: JSON.stringify([{ service: 'orders-api', version: 'v2', firstSeenNs: 1_799_999_000e9, spanCount: 1 }]) };
    const rel = { ...snap, eventId: 'e2', detector: 'op_latency', signal: 'trace', detectedAt: 1_800_000_300e9 };
    expect(contextRows(s2, [rel]).map(r => r.rel)).toEqual(['Yayın', 'Aynı servis']);
    expect(contextRows(null, [])).toEqual([]);
  });
});
