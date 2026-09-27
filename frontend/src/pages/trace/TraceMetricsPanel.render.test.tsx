// @vitest-environment jsdom
//
// TraceMetricsPanel.render.test.tsx — v0.10.962 (Trace Metrics beş hata
// düzeltmesi; yeniden tasarım mockup'ı onaylandı, önce bugünkü panelin
// hataları ayrı sürümde):
//
//   1. Seçili pod GÖRÜNMÜYORDU: her çipe `tone="accent"` veriliyordu ve
//      `.ch-accent` ile `.active` globals.css'te AYNI tint — seçili/seçisiz
//      aynı boyanıyordu. Seçim yalnız `active` ile işaretlenir.
//   2. "Pod sayfasında aç" pencereyi DÜŞÜRÜYORDU (/pod son 1 saate düşer).
//   3. TEK pod sorgusunun hatası TÜM grafikleri gizliyordu (`failed ? …`).
//   4. Tavandayken (4 pod) aynı servisten tıklama sessizce yutuluyordu.
//   5. Servis grupları alfabetikti.
//
// İnceleme ekleri (aynı sürüm): biri boş biri düşen karışık seçimde başlık
// "metrik yok" demez ve hata `.is-err` içinde; hata gövdesi gerçek writeErr
// biçiminde (`HTTP 500: {"error":…}`) — apiErrorDetail iç mesajı açar, ham
// JSON yazılmaz; tavan `.sr-only` canlı bölgeyle duyurulur.
//
// Grafik STUB'LANIYOR (jsdom'da canvas yok, uPlot patlar); ölçülen şey
// hangi pod'un grafiğe GİRDİĞİ, çizim değil.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SpanRow } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// uPlot modül yüklenirken matchMedia çağırıyor (TraceJvmPanel → RuntimeCharts
// zinciri); import'lardan ÖNCE durmalı (emsal: ChatBubble.render.test.tsx).
vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

const m = vi.hoisted(() => ({ fail: new Set<string>(), empty: new Set<string>(), calls: [] as string[] }));
const calls = vi.hoisted(() => ({
  entityClusters: async () => ({ clusters: [{ id: 'c1', name: 'ist', spanClusterValue: 'prod-ist' }] }),
  clusterPodDetail: async (cluster: string, namespace: string, pod: string) => {
    m.calls.push(pod);
    // Gerçek biçim: writeErr `{"error": …}` JSON + json.Encoder'ın '\n'i.
    if (m.fail.has(pod)) throw new Error('HTTP 500: {"error":"thanos upstream timeout"}\n');
    if (m.empty.has(pod)) return { cluster, namespace, pod, trend: [] };
    return { cluster, namespace, pod, trend: [{ bucket: 1_700_000_000, cpuCores: 0.2, memBytes: 1e8 }] };
  },
  serviceRuntime: async () => ({ language: 'go' }),
}));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, api: { ...(mod.api as Record<string, unknown>), ...calls } };
});
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: ({ series, unit }: { series: { groupKey: string[] }[]; unit?: string }) =>
    <div data-mlc={unit} data-series={series.map(s => s.groupKey[0]).join('|')} />,
}));

import { TraceMetricsPanel } from './TraceMetricsPanel';
import { shortPod, TRACE_METRICS_MAX_PODS } from './traceMetrics';

const T0 = 1_700_000_000_000 * 1e6;
const span = (id: string, parent: string, svc: string, pod: string, err = false): SpanRow => ({
  spanId: id, parentSpanId: parent, serviceName: svc, startTime: T0, durationMs: 10,
  statusCode: err ? 'error' : 'ok', statusMessage: '', attributes: {},
  resourceAttributes: { 'k8s.pod.name': pod, 'k8s.namespace.name': 'ns1', 'k8s.cluster.name': 'prod-ist' },
} as unknown as SpanRow);

// 5 login pod'u (tavan 4'ü aşmak için) + hatalı "zeta" + kök "alpha".
const LOGIN = Array.from({ length: 5 }, (_, i) => `login-7b9949bb74-p${i}aaa`);
const SPANS: SpanRow[] = [
  span('r', '', 'alpha', 'alpha-6d9f7c8b5-r0000'),
  ...LOGIN.map((p, i) => span(`l${i}`, 'r', 'login', p)),
  span('z', 'r', 'zeta', 'zeta-5f4c9d8b7-z0000', true),
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let loc = '';
function Probe() { loc = useLocation().search; return null; }
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
// Hata yolu başarı yolundan birkaç tik geç oturuyor (ölçüldü): sabit tek
// bekleme yerine spinner kalkana dek (üst sınırlı) bekle.
async function settle(el: HTMLElement) {
  for (let i = 0; i < 40 && el.querySelector('.spinner'); i++) await wait();
}

async function mount(search: string): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[`/trace${search}`]}>
          <TraceMetricsPanel spans={SPANS} />
          <Probe />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await wait();
  await settle(host!);
  return host!;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null; loc = '';
  m.fail.clear(); m.empty.clear(); m.calls.length = 0;
});

const chipOf = (el: HTMLElement, pod: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.title.startsWith(`${pod} · `))!;
const seriesOf = (el: HTMLElement, unit: string) =>
  el.querySelector(`[data-mlc="${unit}"]`)?.getAttribute('data-series')?.split('|') ?? null;

describe('TraceMetricsPanel — v0.10.962', () => {
  it('hata 1: seçili pod seçisizden AYIRT edilir (yalnız seçili çip tint alır)', async () => {
    const el = await mount(`?mpod=${LOGIN[0]}`);
    const on = chipOf(el, LOGIN[0]);
    const off = chipOf(el, LOGIN[1]);
    expect(on.getAttribute('aria-pressed')).toBe('true');
    expect(off.getAttribute('aria-pressed')).toBe('false');
    expect(on.classList.contains('active')).toBe(true);
    // `.ch-accent` = `.active` ile aynı tint: seçisiz çipte OLMAMALI.
    expect(off.classList.contains('ch-accent')).toBe(false);
    expect(off.classList.contains('active')).toBe(false);
    expect(on.className).not.toBe(off.className);
  });

  it('hata 2: "Pod sayfasında aç" panelin penceresini (range + at) taşır', async () => {
    const el = await mount(`?mpod=${LOGIN[0]}&mwin=5`);
    const a = [...el.querySelectorAll('a')].find(x => x.textContent?.includes('Pod sayfasında aç'))!;
    expect(a).toBeTruthy();
    const q = new URLSearchParams(a.getAttribute('href')!.split('?')[1]);
    const fromMs = Math.floor((T0 - 5 * 60e9) / 1e6);
    const toMs = Math.ceil((T0 + 10e6 + 5 * 60e9) / 1e6);
    expect(q.get('range')).toBe(`custom:${fromMs}-${toMs}`);
    expect(q.get('at')).toBe(String(Math.floor(T0 / 1e6)));
    expect(q.get('pod')).toBe(LOGIN[0]);
  });

  it('hata 3: bir pod sorgusu düşünce diğerleri ÇİZİLİR, hatalı pod notta adıyla', async () => {
    m.fail.add(LOGIN[1]);
    const el = await mount(`?mpod=${LOGIN[0]},${LOGIN[1]}`);
    expect(m.calls.sort()).toEqual([LOGIN[0], LOGIN[1]].sort());
    expect(seriesOf(el, 'bytes')).toEqual([shortPod(LOGIN[0])]);
    expect(seriesOf(el, 'cores')).toEqual([shortPod(LOGIN[0])]);
    expect(el.textContent).not.toContain('Thanos pod metrikleri okunamadı.');
    const cap = el.querySelector('.pod-cap')!.textContent!;
    expect(cap).toContain(shortPod(LOGIN[1]));
    expect(cap).toContain('okunamadı');
    // İç mesaj açılır; ham JSON gövdesi yazılmaz.
    expect(cap).toContain('thanos upstream timeout');
    expect(cap).not.toContain('{"error"');
    // Hata BOŞ-sonuç gibi yazılmaz.
    expect(cap).not.toContain(`${shortPod(LOGIN[1])}: bu pencerede Thanos örneği yok`);
    // Hata rengi sınıftan (`.is-err`), statik inline style değil.
    const red = el.querySelector('.pod-cap .is-err')!;
    expect(red.textContent).toContain(shortPod(LOGIN[1]));
    expect(el.querySelector('.pod-cap [style*="var(--err)"]')).toBeNull();
  });

  it('hata 3 karışık: biri boş biri düşer → başlık "metrik yok" DEMEZ, hata .is-err içinde', async () => {
    m.empty.add(LOGIN[0]); m.fail.add(LOGIN[1]);
    const el = await mount(`?mpod=${LOGIN[0]},${LOGIN[1]}`);
    expect(el.querySelector('[data-mlc]')).toBeNull();
    const empty = el.querySelector('.empty')!;
    const h3 = empty.querySelector('h3')!.textContent!;
    expect(h3).not.toBe("Seçili pod'lar için metrik yok.");
    expect(h3).toContain('1/2');
    expect(h3).toContain('okunamadı');
    expect(empty.querySelector('.icon')!.textContent).toBe('✗');
    const red = empty.querySelector('.is-err')!;
    expect(red.textContent).toContain(shortPod(LOGIN[1]));
    expect(red.textContent).toContain('thanos upstream timeout');
    expect(red.textContent).not.toContain(shortPod(LOGIN[0]));
    expect(empty.textContent).toContain(`${shortPod(LOGIN[0])}: bu pencerede Thanos örneği yok`);
  });

  it('hata 3: hepsi boş (hata yok) → nötr "metrik yok", kırmızı yok', async () => {
    m.empty.add(LOGIN[0]); m.empty.add(LOGIN[1]);
    const el = await mount(`?mpod=${LOGIN[0]},${LOGIN[1]}`);
    const empty = el.querySelector('.empty')!;
    expect(empty.querySelector('h3')!.textContent).toBe("Seçili pod'lar için metrik yok.");
    expect(empty.querySelector('.icon')!.textContent).toBe('—');
    expect(empty.querySelector('.is-err')).toBeNull();
  });

  it('hata 3: HEPSİ düşünce hata durumu — pod başına neden gövdede', async () => {
    m.fail.add(LOGIN[0]); m.fail.add(LOGIN[1]);
    const el = await mount(`?mpod=${LOGIN[0]},${LOGIN[1]}`);
    expect(el.querySelector('[data-mlc]')).toBeNull();
    const empty = el.querySelector('.empty')!;
    expect(empty.textContent).toContain('Thanos pod metrikleri okunamadı.');
    const red = empty.querySelector('.is-err')!;
    expect(red.textContent).toContain(shortPod(LOGIN[0]));
    expect(red.textContent).toContain(shortPod(LOGIN[1]));
    expect(red.textContent).toContain('thanos upstream timeout');
    expect(empty.textContent).not.toContain('{"error"');
  });

  it('hata 4: tavanda aynı servisin seçisiz pod\'u devre dışı + nedeni yazılı; başka servis seçilebilir', async () => {
    const sel = LOGIN.slice(0, TRACE_METRICS_MAX_PODS);
    const el = await mount(`?mpod=${sel.join(',')}`);
    const blocked = chipOf(el, LOGIN[4]);
    expect(blocked.disabled).toBe(true);
    expect(blocked.title).toMatch(/en çok 4 pod/i);
    // Seçili olan çıkarılabilir, başka servis seçimi değiştirir → açık.
    expect(chipOf(el, LOGIN[0]).disabled).toBe(false);
    expect(chipOf(el, 'zeta-5f4c9d8b7-z0000').disabled).toBe(false);
    // Görünür not (title yalnız fareyle okunur).
    expect(el.textContent).toMatch(/en çok 4 pod/i);
    // Ekran okuyucu: tek `.sr-only` canlı bölge duyurur (Spinner de
    // role=status taşır → `.sr-only` ile sorgulanır); görünür sayaç canlı değil.
    const live = el.querySelectorAll('.sr-only[role="status"]');
    expect(live.length).toBe(1);
    expect(live[0].getAttribute('aria-live')).toBe('polite');
    expect(live[0].textContent).toMatch(/en çok 4 pod/i);
    const counter = [...el.querySelectorAll('span')].find(s => !s.classList.contains('sr-only') && /\/4 pod/.test(s.textContent ?? ''))!;
    expect(counter).toBeTruthy();
    expect(counter.hasAttribute('aria-live')).toBe(false);
    // Tavan altı: not yok, çip açık; canlı bölge BAĞLI ama boş.
    act(() => { root?.unmount(); }); host?.remove();
    const el2 = await mount(`?mpod=${sel.slice(0, 3).join(',')}`);
    expect(chipOf(el2, LOGIN[4]).disabled).toBe(false);
    expect(el2.textContent).not.toMatch(/en çok 4 pod/i);
    const live2 = el2.querySelector('.sr-only[role="status"]');
    expect(live2).not.toBeNull();
    expect(live2!.textContent).toBe('');
  });

  it('hata 4: tavanda başka servise tıklamak seçimi değiştirir (URL, replace)', async () => {
    const el = await mount(`?mpod=${LOGIN.slice(0, 4).join(',')}`);
    act(() => { chipOf(el, 'zeta-5f4c9d8b7-z0000').click(); });
    await wait();
    expect(new URLSearchParams(loc).get('mpod')).toBe('zeta-5f4c9d8b7-z0000');
  });

  it('hata 5: servis grupları trace ilgisine göre (hata kaynağı önce), alfabetik değil', async () => {
    const el = await mount(`?mpod=${LOGIN[0]}`);
    const labels = [...el.querySelectorAll('span.mono')].map(s => s.textContent);
    // zeta: tek hatalı (kaynak) · login: 5 span · alpha: 1 span.
    expect(labels).toEqual(['zeta', 'login', 'alpha']);
  });
});
