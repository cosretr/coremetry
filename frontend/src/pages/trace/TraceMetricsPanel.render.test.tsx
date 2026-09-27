// @vitest-environment jsdom
//
// TraceMetricsPanel.render.test.tsx — v0.10.968 (Trace › Metrics yeniden
// tasarımı, operatör onayı 2026-09-27). Kabuk + gruplu tablo jsdom'da;
// toplu uç (api.tracePodMetrics) sahte, seçili pod paneli ve odak görünümü
// (L-pod) aldıkları prop'ları kaydeden STUB'lar.
//
// v0.10.962'nin beş düzeltmesi yeni tasarımda sürer ve burada çivili:
//   hata 1 — seçim görünür (row-selected + aria-selected), URL yazımı replace
//            ve yabancı span/tab korunur (test 5);
//   hata 2 — "Pod sayfasında aç" range + at taşır (test 6);
//   hata 3 — tek dilimin hatası "okunamadı"dır, "örnek yok" DEĞİL; şerit
//            "N servis okunamadı"; yeniden deneme yalnız o dilimi çeker (test 3);
//   hata 4 — geri bildirim kanalı her zaman bağlı canlı bölge (test 11);
//   hata 5 — sıra trace ilgisi, metrik gelince DEĞİŞMEZ (test 1).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useState, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SpanRow, TraceAnalysis, TracePodMetricsQuery, TracePodMetricsResponse, TracePodSeriesRow } from '@/lib/types';
import { computeCriticalPath } from '@/lib/criticalPath';
import { __resetEscLayers, topEscLayer } from '@/lib/escLayer';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

type Mode = 'ok' | 'unmapped' | 'fail' | 'off' | 'hold';
const m = vi.hoisted(() => ({
  calls: [] as TracePodMetricsQuery[],
  mode: {} as Record<string, Mode>,
  podState: {} as Record<string, 'no_samples' | 'ambiguous'>,
  cpu: {} as Record<string, number>,
  held: [] as (() => void)[],
  // v0.10.968 — kube-state-metrics kısmen düştü: instant "partial", limitsiz satır.
  partial: false,
}));
const stubs = vi.hoisted(() => ({ panel: null as null | Record<string, unknown>, focus: null as null | Record<string, unknown> }));

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      tracePodMetrics: (q: TracePodMetricsQuery) => {
        m.calls.push(q);
        return respond(q);
      },
    },
  };
});
vi.mock('./TracePodPanel', () => ({
  TracePodPanel: (p: Record<string, unknown> & { selected: { pod: string } }) => {
    stubs.panel = p;
    return <div data-testid="pod-panel">{p.selected.pod}</div>;
  },
}));
vi.mock('./TracePodFocus', () => ({
  TracePodFocus: (p: Record<string, unknown> & { selected: { pod: string } }) => {
    stubs.focus = p;
    return <div data-testid="pod-focus">{p.selected.pod}</div>;
  },
}));

import { TraceMetricsPanel } from './TraceMetricsPanel';
import { buildTraceMetricsModel, spanDepthMap } from './traceMetrics';

const MS = 1e6;
const T0 = 1_700_000_000_000 * MS; // 2023 — açık pencere değil (yeniden çekme zamanlayıcısı yok)
const START_SEC = Math.floor(T0 / 1e9 / 15) * 15 - 15 * 60;

function span(id: string, parent: string, svc: string, pod: string, o: { err?: boolean; start?: number; dur?: number; cv?: string; ns?: string } = {}): SpanRow {
  const start = T0 + (o.start ?? 0) * MS;
  const dur = (o.dur ?? 10) * MS;
  return {
    traceId: 'tr-synthetic', spanId: id, parentSpanId: parent, name: `op-${id}`, kind: 'SERVER', serviceName: svc,
    hostName: '', startTime: start, endTime: start + dur, durationMs: dur / MS, statusCode: o.err ? 'error' : 'ok',
    statusMessage: '', attributes: {}, events: null, scopeName: '',
    resourceAttributes: pod ? { 'k8s.pod.name': pod, 'k8s.namespace.name': o.ns ?? 'payments', 'k8s.cluster.name': o.cv ?? 'dc-east-1' } : {},
  };
}
function analysisOf(spans: SpanRow[], self: Record<string, number>): TraceAnalysis {
  const depth = spanDepthMap(spans);
  return {
    v: 1, criticalNs: 0, criticalIds: [], services: [], rootSpanId: spans[0].spanId, orphanCount: 0, truncated: false,
    nodes: spans.map((s, i) => ({
      spanId: s.spanId, parentSpanId: s.parentSpanId, depth: depth.get(s.spanId) ?? 0, order: i,
      childCount: 0, subtreeCount: 1, subtreeErrors: 0, subtreeNs: 0, selfNs: (self[s.spanId] ?? 1) * MS,
    })),
  };
}
const cpOf = (spans: SpanRow[]) => computeCriticalPath(spans.map(s => ({
  spanId: s.spanId, parentId: s.parentSpanId, startTime: s.startTime, duration: s.endTime - s.startTime,
})));

function row(p: { ns: string; pod: string }): TracePodSeriesRow {
  const st = m.podState[p.pod];
  if (st) return { ns: p.ns, pod: p.pod, state: st, inventory: 'unknown' };
  const v = m.cpu[p.pod] ?? 0.2;
  const lim = m.partial ? { cpuRequest: 0.5 } : { cpuLimit: 1, memLimit: 2 * 1024 ** 3 };
  return {
    ns: p.ns, pod: p.pod, state: 'ok', inventory: 'present',
    cpu: Array.from({ length: 121 }, () => v), mem: Array.from({ length: 121 }, () => 512 * 1024 ** 2),
    ...lim, phase: 'Running', restarts: 0,
  };
}
function respond(q: TracePodMetricsQuery): Promise<TracePodMetricsResponse> {
  const mode = m.mode[q.clusterValue] ?? 'ok';
  if (mode === 'fail') {
    // v0.10.968 — sunucunun GERÇEK errUpstream biçimi.
    return Promise.reject(new Error(`HTTP 502: {"error":"upstream metrics backend: ${q.clusterValue}: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)"}\n`));
  }
  if (mode === 'off') return Promise.resolve({ thanos: false, clusterValue: q.clusterValue, mapped: false, pods: [] });
  if (mode === 'unmapped') {
    return Promise.resolve({ thanos: true, clusterValue: q.clusterValue, mapped: false, unmappedReason: 'no_remote_cluster', pods: [] });
  }
  const body: TracePodMetricsResponse = {
    thanos: true, clusterValue: q.clusterValue, mapped: true, cluster: { id: 'c-1a2b3c4d', name: q.clusterValue },
    start: START_SEC, step: 15, points: 121, instant: m.partial ? 'partial' : 'ok', pods: q.pods.map(row),
  };
  if (mode === 'hold') return new Promise(res => { m.held.push(() => res(body)); });
  return Promise.resolve(body);
}

// Küçük trace (≤12 pod → bütün gruplar açık): fraud-score-prod'da iki hatalı
// pod (F2 en derin → kök neden), audit-log-prod dc-east-2'de, checkout-api kök.
const P_CK = 'checkout-api-7c8977f965-7hrqz';
const P_F1 = 'fraud-score-prod-6b7d9f8c5-m3t9w';
const P_F2 = 'fraud-score-prod-6b7d9f8c5-h8k2v';
const P_F3 = 'fraud-score-prod-6b7d9f8c5-q4n7r';
const P_AU = 'audit-log-prod-5f9c7d6b8-q2w4e';
const SPANS: SpanRow[] = [
  span('r', '', 'checkout-api', P_CK, { dur: 1000 }),
  span('a', 'r', 'fraud-score-prod', P_F1, { start: 100, dur: 300, err: true }),
  span('b', 'a', 'fraud-score-prod', P_F2, { start: 150, dur: 200, err: true }),
  span('c', 'r', 'fraud-score-prod', P_F3, { start: 410, dur: 20 }),
  span('d', 'r', 'audit-log-prod', P_AU, { start: 450, dur: 30, cv: 'dc-east-2' }),
  span('n1', 'r', 'checkout-api', '', { start: 600, dur: 40 }),
  span('n2', 'n1', 'checkout-api', '', { start: 610, dur: 20 }),
];

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const shown: string[] = [];
const opened: string[] = [];

const flush = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });
async function settle(n = 6) { for (let i = 0; i < n; i++) await flush(); }

// v0.10.968 — sekme gidiş-dönüşü: panel AYNI BrowserRouter içinde sökülüp
// yeniden bağlanır (Trace.tsx sekme değişince paneli söker; router kalır).
let setShown: (b: boolean) => void = () => {};
function Toggle({ children }: { children: ReactNode }) {
  const [on, set] = useState(true);
  setShown = set;
  return on ? <>{children}</> : null;
}

async function mount(search: string, spans: SpanRow[] = SPANS, opts: { analysis?: boolean } = {}): Promise<HTMLElement> {
  window.history.replaceState({}, '', `/trace${search}`);
  host = document.createElement('div');
  document.body.appendChild(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const analysis = opts.analysis === false ? undefined : analysisOf(spans, {});
  act(() => {
    root = createRoot(host!);
    root.render(
      <QueryClientProvider client={qc}>
        <BrowserRouter>
          <Toggle>
            <TraceMetricsPanel traceId="tr-synthetic" spans={spans} analysis={analysis} criticalPath={cpOf(spans)}
              spanCapped={false} onShowPodSpans={p => shown.push(p)} onOpenSpan={s => opened.push(s)} />
          </Toggle>
        </BrowserRouter>
      </QueryClientProvider>,
    );
  });
  await settle();
  return host!;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  m.calls.length = 0; m.held.length = 0;
  m.mode = {}; m.podState = {}; m.cpu = {}; m.partial = false;
  stubs.panel = null; stubs.focus = null;
  shown.length = 0; opened.length = 0;
  __resetEscLayers();
  window.history.replaceState({}, '', '/');
});

const rowKeys = (el: HTMLElement) => [...el.querySelectorAll<HTMLElement>('tbody tr[data-key]')].map(r => r.dataset.key!);
const rowOf = (el: HTMLElement, key: string) => el.querySelector<HTMLElement>(`tbody tr[data-key="${key}"]`)!;
const metricTds = (tr: HTMLElement) => [...tr.querySelectorAll<HTMLElement>('td.tpm-mtd')];
const params = () => new URLSearchParams(window.location.search);
const click = (n: Element) => act(() => { (n as HTMLElement).click(); });
const key = (target: Element, k: string) => act(() => {
  target.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }));
});
function typeInto(input: HTMLInputElement, value: string) {
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  act(() => { set.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); });
}

describe('TraceMetricsPanel — v0.10.968', () => {
  it('1. satırlar istek beklerken ilgi sırasında (iskelet hücre); cevap gelince sıra DEĞİŞMEZ', async () => {
    m.mode = { 'dc-east-1': 'hold', 'dc-east-2': 'hold' };
    // Metrik sıraya girseydi en sıcak pod (P_F3) öne geçerdi.
    m.cpu = { [P_F3]: 0.99, [P_CK]: 0.95 };
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const before = rowKeys(el);
    expect(before).toEqual([
      'g:fraud-score-prod', `p:${P_F2}`, `p:${P_F1}`, `p:${P_F3}`,
      'g:checkout-api', `p:${P_CK}`,
      'g:audit-log-prod', `p:${P_AU}`,
      'n:',
    ]);
    expect(rowOf(el, `p:${P_F3}`).querySelector('[aria-label="Yükleniyor"]')).not.toBeNull();
    await act(async () => { m.held.forEach(f => f()); });
    await settle();
    expect(rowOf(el, `p:${P_F3}`).querySelector('[aria-label="Yükleniyor"]')).toBeNull();
    expect(metricTds(rowOf(el, `p:${P_F3}`))[0].textContent).toContain('%99');
    expect(rowKeys(el)).toEqual(before);
  });

  it('2. 64 pod / 2 cluster değeri → TAM 2 istek, pod\'lar tablo sırasında; eşlenmemiş → soluk "—"', async () => {
    const e1 = Array.from({ length: 40 }, (_, i) => `fraud-score-prod-6b7d9f8c5-${String(i).padStart(5, 'f')}`);
    const e2 = Array.from({ length: 24 }, (_, i) => `audit-log-prod-5f9c7d6b8-${String(i).padStart(5, 'a')}`);
    const spans = [span('r', '', 'checkout-api', '', { dur: 100 }),
      ...e1.map((p, i) => span(`x${i}`, 'r', 'fraud-score-prod', p, { cv: 'dc-east-1', start: i })),
      ...e2.map((p, i) => span(`y${i}`, 'r', 'audit-log-prod', p, { cv: 'dc-east-2', start: i }))];
    m.mode = { 'dc-east-2': 'unmapped' };
    const el = await mount('?id=tr-synthetic&tab=metrics', spans, { analysis: false });
    expect(m.calls).toHaveLength(2);
    const model = buildTraceMetricsModel(spans, undefined, cpOf(spans), false);
    const order = (cv: string) => model.groups.flatMap(g => g.pods).filter(p => p.clusterValue === cv).map(p => p.pod);
    const byCv = Object.fromEntries(m.calls.map(c => [c.clusterValue, c.pods.map(p => p.pod)]));
    expect(byCv['dc-east-1']).toEqual(order('dc-east-1'));
    expect(byCv['dc-east-2']).toEqual(order('dc-east-2'));
    const cpu = metricTds(rowOf(el, 'g:audit-log-prod'))[0];
    expect(cpu.classList.contains('cell-faint')).toBe(true);
    expect(cpu.textContent).toBe('—');
    expect(cpu.querySelector('[aria-description]')!.getAttribute('aria-description'))
      .toBe('dc-east-2 bir Remote Cluster kaydına eşlenmemiş');
    // Kapsam tetiği eşlenmeyen pod varken uyarı renginde.
    const cov = [...el.querySelectorAll('button')].find(b => b.textContent?.startsWith('Metrik kapsamı'))!;
    expect(cov.textContent).toContain('40/64 pod');
    expect(cov.classList.contains('is-warn')).toBe(true);
  });

  it('3. bir dilim 502 (zaman aşımı) → "okunamadı" (cell-err), "örnek yok" DEĞİL; şerit + yalnız o dilim yeniden', async () => {
    m.mode = { 'dc-east-2': 'fail' };
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const au = metricTds(rowOf(el, `p:${P_AU}`));
    for (const td of au) {
      expect(td.textContent).toBe('okunamadı');
      expect(td.classList.contains('cell-err')).toBe(true);
      expect(td.querySelector('[aria-description]')!.getAttribute('aria-description'))
        .toBe('Veri okunamadı — bu bir hata, boş sonuç değil. 10 sn zaman aşımı');
    }
    expect(el.textContent).not.toContain('örnek yok');
    // Diğer cluster'ın hücreleri etkilenmez.
    expect(metricTds(rowOf(el, `p:${P_F1}`))[0].textContent).toContain('%20');
    expect(el.querySelector('.tpm-strip')!.textContent).toContain('1 servis okunamadı');
    // v0.10.968 (MF-4) — zaman aşımı kapsam açığı DEĞİL: tetik 5/5, uyarı renginde değil.
    const cov = [...el.querySelectorAll('button')].find(b => b.textContent?.startsWith('Metrik kapsamı'))!;
    expect(cov.textContent).toContain('5/5 pod');
    expect(cov.classList.contains('is-warn')).toBe(false);
    // v0.10.968 (MF-10) — sonuç satırı hash eki değil SERVİS adını söyler.
    click(cov);
    const dlg = el.querySelector('[role="dialog"][aria-label="Metrik kapsamı"]')!;
    expect(dlg.querySelector('.tpm-cov-result')!.textContent).toContain('1 pod okunamadı (audit-log-prod, 10 sn zaman aşımı)');
    click(cov);
    const before = m.calls.map(c => c.clusterValue);
    expect(before.sort()).toEqual(['dc-east-1', 'dc-east-2']);
    const retry = [...el.querySelectorAll<HTMLButtonElement>('.tpm-strip button')].find(b => b.textContent === 'Yeniden dene')!;
    m.mode = { 'dc-east-2': 'hold' };
    click(retry);
    await settle(2);
    // Yeniden deneme sürerken şerit "Yükleniyor…" + aria-busy; hücreler iskelet.
    const busy = [...el.querySelectorAll<HTMLButtonElement>('.tpm-strip button')].find(b => b.textContent === 'Yükleniyor…')!;
    expect(busy.getAttribute('aria-busy')).toBe('true');
    expect(rowOf(el, `p:${P_AU}`).querySelector('[aria-label="Yükleniyor"]')).not.toBeNull();
    await act(async () => { m.held.forEach(f => f()); });
    await settle();
    expect(m.calls.map(c => c.clusterValue).slice(2)).toEqual(['dc-east-2']);
    expect(metricTds(rowOf(el, `p:${P_AU}`))[0].textContent).toContain('%20');
    expect(el.querySelector('.tpm-strip')!.textContent).not.toContain('okunamadı');
    // v0.10.968 (F6 ui) — şerit düğmesi kalktı; sonuç canlı bölgede.
    await act(async () => { await new Promise(r => setTimeout(r, 80)); });
    expect(el.querySelector('.sr-only[role="status"]')!.textContent).toBe('Metrikler yeniden yüklendi.');
  });

  it('4. no_samples → nötr "örnek yok"; ambiguous → "belirsiz"', async () => {
    m.podState = { [P_F3]: 'no_samples', [P_F1]: 'ambiguous' };
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const ns = metricTds(rowOf(el, `p:${P_F3}`))[0];
    expect(ns.textContent).toBe('örnek yok');
    expect(ns.classList.contains('cell-faint')).toBe(true);
    expect(ns.classList.contains('cell-err')).toBe(false);
    const amb = metricTds(rowOf(el, `p:${P_F1}`))[0];
    expect(amb.textContent).toBe('belirsiz');
    expect(amb.querySelector('[aria-description]')!.getAttribute('aria-description'))
      .toBe("Pod adı birden çok namespace'te — span'de k8s.namespace.name yok");
  });

  it('5. satır tıkı: aria-selected + row-selected; mpod replace ile yazılır, span ve tab korunur', async () => {
    const el = await mount('?id=tr-synthetic&span=s0&tab=metrics');
    // Varsayılan seçim: kök neden pod'u (tek pod).
    expect(rowOf(el, `p:${P_F2}`).getAttribute('aria-selected')).toBe('true');
    expect(stubs.panel!.compare).toEqual([P_F2]);
    // v0.10.968 (F8) — Trace.tsx span/tab/xn'i router'a haber vermeden ham
    // replaceState ile yazar; `prev` kopyalayan bir yazıcı bunları düşürürdü.
    window.history.replaceState({}, '', '/trace?id=tr-synthetic&span=s9&tab=metrics&xn=1');
    const len = window.history.length;
    click(rowOf(el, `p:${P_CK}`).querySelector('td.mono')!);
    await settle(2);
    const tr = rowOf(el, `p:${P_CK}`);
    expect(tr.getAttribute('aria-selected')).toBe('true');
    expect(tr.classList.contains('row-selected')).toBe(true);
    expect(rowOf(el, `p:${P_F2}`).getAttribute('aria-selected')).toBe('false');
    expect([params().get('mpod'), params().get('span'), params().get('tab'), params().get('id'), params().get('xn')])
      .toEqual([P_CK, 's9', 'metrics', 'tr-synthetic', '1']);
    expect(window.history.length).toBe(len);
    expect(el.querySelector('[data-testid="pod-panel"]')!.textContent).toBe(P_CK);
  });

  it('6. satır menüsü "Pod sayfasında aç" range + at taşır', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics&mwin=5');
    const btn = el.querySelector<HTMLButtonElement>(`[aria-label="${P_F1} için eylemler"]`)!;
    click(btn);
    const menu = el.querySelector('[role="menu"]')!;
    expect(menu.getAttribute('aria-label')).toBe(`${P_F1} için eylemler`);
    const a = [...menu.querySelectorAll('a')].find(x => x.textContent?.includes('Pod sayfasında aç'))!;
    const q = new URLSearchParams(a.getAttribute('href')!.split('?')[1]);
    const start = T0; const end = T0 + 1000 * MS;
    expect(q.get('range')).toBe(`custom:${Math.floor((start - 5 * 60e9) / 1e6)}-${Math.ceil((end + 5 * 60e9) / 1e6)}`);
    expect(q.get('at')).toBe(String(Math.floor(start / 1e6)));
    expect([q.get('pod'), q.get('cluster'), q.get('namespace'), q.get('service')])
      .toEqual([P_F1, 'dc-east-1', 'payments', 'fraud-score-prod']);
    // "Span'ları Trace'te göster (N)" sayfanın geri çağrısına gider.
    const showSpans = [...menu.querySelectorAll('[role="menuitem"]')].find(x => x.textContent?.startsWith("Span'ları Trace'te göster"))!;
    expect(showSpans.textContent).toBe("Span'ları Trace'te göster (1)");
    click(showSpans);
    expect(shown).toEqual([P_F1]);
    expect(el.querySelector('[role="menu"]')).toBeNull();
  });

  it('7. süzgeç sayıları etikette; arama daraltır; eşleşme yok → durum satırı; temizle ikisini de sıfırlar', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const radios = [...el.querySelectorAll<HTMLButtonElement>('[aria-label="Pod süzgeci"] [role="radio"]')];
    expect(radios.map(r => r.textContent)).toEqual(['Hepsi 5', 'Hatalı 2', 'Kritik yol 3', 'Yavaş 5']);
    expect(radios[1].title).toBe("En az bir hatalı span taşıyan pod'lar");
    click(radios[1]);
    await settle(2);
    expect(params().get('mf')).toBe('err');
    expect(rowKeys(el)).toEqual(['g:fraud-score-prod', `p:${P_F2}`, `p:${P_F1}`]);
    expect(rowOf(el, 'g:fraud-score-prod').textContent).toContain('2/3 pod');
    const input = el.querySelector<HTMLInputElement>('input[type="search"]')!;
    typeInto(input, 'yok-boyle-bir-pod');
    await settle(2);
    expect(el.querySelector('[data-dt-state="no-match"]')).not.toBeNull();
    await act(async () => { await new Promise(r => setTimeout(r, 250)); });
    expect(params().get('mq')).toBe('yok-boyle-bir-pod');
    const clear = [...el.querySelectorAll('button')].find(b => b.textContent === 'Filtreleri temizle')!;
    click(clear);
    await settle(2);
    expect(params().has('mf')).toBe(false);
    expect(params().has('mq')).toBe(false);
    expect(input.value).toBe('');
    expect(rowKeys(el)).toContain(`p:${P_AU}`);
    // Arama namespace/servis/pod'da, büyük/küçük harf duyarsız.
    typeInto(input, 'AUDIT');
    await settle(2);
    expect(rowKeys(el)).toEqual(['g:audit-log-prod', `p:${P_AU}`]);
  });

  it('8. klavye: ↓ → Enter seçer; ← grubu kapatır; Esc önce aramayı sonra seçimi temizler', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    expect(el.querySelector('input[data-shortcut-search]')).not.toBeNull();
    const stop = el.querySelector<HTMLElement>('tbody tr[tabindex="0"]')!;
    expect(stop.dataset.key).toBe(`p:${P_F2}`); // Tab durağı = seçili satır
    act(() => { stop.focus(); });
    key(stop, 'ArrowDown');
    expect((document.activeElement as HTMLElement).dataset.key).toBe(`p:${P_F1}`);
    key(document.activeElement!, 'Enter');
    await settle(2);
    expect(params().get('mpod')).toBe(`${P_F1},${P_F2}`); // aynı servis + ikisi ok → karşılaştırmaya eklenir
    expect(rowOf(el, `p:${P_F1}`).getAttribute('aria-selected')).toBe('true');
    key(document.activeElement!, 'ArrowLeft');
    expect((document.activeElement as HTMLElement).dataset.key).toBe('g:fraud-score-prod');
    key(document.activeElement!, 'ArrowLeft');
    await settle(1);
    expect(rowOf(el, 'g:fraud-score-prod').getAttribute('aria-expanded')).toBe('false');
    expect(rowKeys(el)).not.toContain(`p:${P_F2}`);
    // Arama kutusunda Esc metni temizler (katmana düşmez).
    const input = el.querySelector<HTMLInputElement>('input[type="search"]')!;
    typeInto(input, 'fraud');
    act(() => { input.focus(); });
    key(input, 'Escape');
    expect(input.value).toBe('');
    expect(params().get('mpod')).toBe(`${P_F1},${P_F2}`);
    // Kabuğun katmanı: arama boş → seçimi kapatır (mpod=-).
    typeInto(input, 'x');
    act(() => { topEscLayer()!(); });
    expect(input.value).toBe('');
    act(() => { topEscLayer()!(); });
    await settle(2);
    expect(params().get('mpod')).toBe('-');
    expect(el.querySelector('[data-testid="pod-panel"]')).toBeNull();
  });

  it('9. thanos:false → CPU/Bellek başlıkları yok + not', async () => {
    m.mode = { 'dc-east-1': 'off', 'dc-east-2': 'off' };
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const heads = [...el.querySelectorAll('thead th')].map(t => t.textContent ?? '');
    expect(heads.some(h => h.startsWith('CPU') || h.startsWith('Bellek'))).toBe(false);
    expect(heads.some(h => h.startsWith('Kritik yol'))).toBe(true);
    expect(el.querySelector('td.tpm-mtd')).toBeNull();
    expect(el.textContent).toContain('Thanos Remote Cluster tanımlı değil');
  });

  it('10. pod\'suz satır span sayısını taşır; süzgeç altında gizlenir', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const np = rowOf(el, 'n:');
    expect(np.textContent).toContain("Pod bilgisi olmayan span'lar · 2 span · 1 servis");
    expect(np.getAttribute('aria-expanded')).toBe('false');
    click(np.querySelector('td')!);
    expect(rowKeys(el)).toContain('n:checkout-api');
    await act(async () => {
      window.history.replaceState({}, '', '/trace?id=tr-synthetic&tab=metrics&mf=err');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    await settle(2);
    expect(rowKeys(el).some(k => k.startsWith('n:'))).toBe(false);
  });

  it('11. canlı bölge hep bağlı; announce metni oraya düşer (aynı cümle yeniden duyurulur)', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const live = el.querySelectorAll('.sr-only[role="status"][aria-live="polite"]');
    expect(live).toHaveLength(1);
    expect(live[0].textContent).toBe('');
    const announce = stubs.panel!.announce as (s: string) => void;
    act(() => { announce('Karşılaştırmada en çok 4 pod.'); });
    await act(async () => { await new Promise(r => setTimeout(r, 80)); });
    expect(live[0].textContent).toBe('Karşılaştırmada en çok 4 pod.');
    act(() => { announce('Karşılaştırmada en çok 4 pod.'); });
    expect(live[0].textContent).toBe('');
    await act(async () => { await new Promise(r => setTimeout(r, 80)); });
    expect(live[0].textContent).toBe('Karşılaştırmada en çok 4 pod.');
    // Servis değişince kabuk kendisi duyurur.
    click(rowOf(el, `p:${P_AU}`).querySelector('td.mono')!);
    await act(async () => { await new Promise(r => setTimeout(r, 80)); });
    expect(live[0].textContent).toBe('Karşılaştırma servisi değişti: audit-log-prod.');
  });

  it('12. mview=pod + seçim → tablo yerine TracePodFocus', async () => {
    const el = await mount(`?id=tr-synthetic&tab=metrics&mpod=${P_F1}&mview=pod`);
    expect(el.querySelector('[data-testid="pod-focus"]')!.textContent).toBe(P_F1);
    expect(el.querySelector('table[role="treegrid"]')).toBeNull();
    expect(stubs.focus!.focus).toBe(true);
    // v0.10.968 (MF-5) — odak görünümü tablo araç çubuğunun da YERİNE geçer.
    expect(el.querySelector('[aria-label="Pod süzgeci"]')).toBeNull();
    expect(el.querySelector('[data-shortcut-search]')).toBeNull();
    // Odaktan çıkış (Esc katmanı ilk olarak odak görünümünü kapatır).
    act(() => { topEscLayer()!(); });
    await settle(2);
    expect(params().has('mview')).toBe(false);
    expect(el.querySelector('table[role="treegrid"]')).not.toBeNull();
    expect(el.querySelector('[aria-label="Pod süzgeci"]')).not.toBeNull();
    expect(el.querySelector('[data-shortcut-search]')).not.toBeNull();
    // v0.10.968 (F1 ui) — odak <body>'de kalmaz, çıkılan pod'un satırına döner.
    expect((document.activeElement as HTMLElement).dataset.key).toBe(`p:${P_F1}`);
  });
});

// ── v0.10.968 — inceleme turu ───────────────────────────────────────────────
describe('TraceMetricsPanel — v0.10.968 inceleme turu', () => {
  it('F2: sekme gidiş-dönüşünde durum ADRES ÇUBUĞUNDAN okunur (router bayat kalsa da); sonraki yazım seçimi korur', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const radio = (label: string) => [...el.querySelectorAll<HTMLButtonElement>('[aria-label="Pod süzgeci"] [role="radio"]')]
      .find(r => r.textContent?.startsWith(label))!;
    click(radio('Hatalı'));
    await settle(2);
    expect(params().get('mf')).toBe('err');
    // Trace.tsx: sekmeden çıkınca paneli söker ve m* parametrelerini ham replaceState ile siler (popstate YOK).
    act(() => { setShown(false); });
    window.history.replaceState({}, '', '/trace?id=tr-synthetic&tab=metrics');
    act(() => { setShown(true); });
    await settle(2);
    expect(radio('Hepsi').getAttribute('aria-checked')).toBe('true');
    expect(rowKeys(el)).toContain('n:');
    expect(rowOf(el, `p:${P_F2}`).getAttribute('aria-selected')).toBe('true');
    // Sonraki yazım: pencere 5 dk → yalnız mwin eklenir; seçim ve süzgeç ZIPLAMAZ.
    const win = [...el.querySelectorAll<HTMLButtonElement>('[aria-label="Metrik penceresi"] [role="radio"]')].find(r => r.textContent === '5 dk')!;
    click(win);
    await settle(2);
    expect([params().get('mwin'), params().get('tab'), params().get('id'), params().has('mf')]).toEqual(['5', 'metrics', 'tr-synthetic', false]);
    expect(radio('Hepsi').getAttribute('aria-checked')).toBe('true');
    expect(rowOf(el, `p:${P_F2}`).getAttribute('aria-selected')).toBe('true');
  });

  it('F1 ui: panel kapanınca odak kapanan pod\'un satırına; odak görünümünden "Daralt" ile dönünce de', async () => {
    const el = await mount('?id=tr-synthetic&tab=metrics');
    act(() => { (stubs.panel!.onClose as () => void)(); });
    await settle(2);
    expect(params().get('mpod')).toBe('-');
    expect((document.activeElement as HTMLElement).dataset.key).toBe(`p:${P_F2}`);
    click(rowOf(el, `p:${P_F1}`).querySelector('td.mono')!);
    await settle(2);
    act(() => { (document.activeElement as HTMLElement).blur(); });
    act(() => { (stubs.panel!.onToggleFocus as () => void)(); });
    await settle(2);
    expect(el.querySelector('[data-testid="pod-focus"]')).not.toBeNull();
    act(() => { (stubs.focus!.onToggleFocus as () => void)(); });
    await settle(2);
    expect(el.querySelector('[data-testid="pod-focus"]')).toBeNull();
    expect((document.activeElement as HTMLElement).dataset.key).toBe(`p:${P_F1}`);
  });

  it('F12 ui: "N pod daha" FAREYLE açılınca odak ilk açılan pod\'a geçer (<body>\'ye düşmez)', async () => {
    const X = ['q1aaa', 'q2bbb', 'q3ccc', 'q4ddd'].map(x => `fraud-score-prod-6b7d9f8c5-${x}`);
    const spans = [
      span('r', '', 'checkout-api', P_CK, { dur: 1000 }),
      span('a', 'r', 'fraud-score-prod', P_F1, { start: 100, dur: 800, err: true }),
      ...X.map((p, i) => span(`x${i}`, 'r', 'fraud-score-prod', p, { start: 150 + i, dur: 5 })),
    ];
    const el = await mount('?id=tr-synthetic&tab=metrics', spans, { analysis: false });
    const more = rowOf(el, 'm:fraud-score-prod');
    expect(more.textContent).toContain('4 pod daha (işaretsiz)');
    // v0.10.968 (F10 ui) — etiket sapma değil: vurgu sınıfı yok.
    expect(more.querySelector('.tpm-more-label')).toBeNull();
    act(() => { more.focus(); });
    click(more.querySelector('td.tpm-more-cell')!);
    await settle(2);
    const a = document.activeElement as HTMLElement;
    expect(a.dataset.key?.startsWith('p:fraud-score-prod-6b7d9f8c5-q')).toBe(true);
    expect(rowKeys(el)).not.toContain('m:fraud-score-prod');
  });

  it('MF-3/6/7: limit okunamadı (instant partial) → "bilinmiyor", sparkline yok, mutlak değer; limitli hücrede sparkline süs + ipucu tüm hücrede', async () => {
    m.partial = true;
    const el = await mount('?id=tr-synthetic&tab=metrics');
    const [cpu, mem] = metricTds(rowOf(el, `p:${P_F1}`));
    const tipOf = (td: HTMLElement) => td.querySelector('.tpm-mcell')!.getAttribute('aria-description') ?? '';
    expect(cpu.textContent).toBe('0,2 c');
    expect(tipOf(cpu)).toBe("limit bilinmiyor (kube-state-metrics sorgusu kısmen başarısız) · isteğin %40'ı (şu an)");
    expect(tipOf(mem)).not.toContain('tanımsız');
    expect(cpu.querySelector('svg')).toBeNull();
    expect(mem.textContent).toBe('512 MiB');
    act(() => { root?.unmount(); });
    host?.remove();
    m.partial = false;
    const el2 = await mount('?id=tr-synthetic&tab=metrics');
    const [cpu2] = metricTds(rowOf(el2, `p:${P_F1}`));
    const spark = cpu2.querySelector('.tpm-spark')!;
    expect(spark.getAttribute('aria-hidden')).toBe('true');
    expect(spark.querySelector('svg')).not.toBeNull();
    // İpucu sparkline'a İKİNCİ kez yazılmaz (ekran okuyucu tek kez duyar); süs aria-hidden.
    expect(spark.querySelector('svg title')?.textContent ?? '').not.toContain('limitin');
    expect(tipOf(cpu2)).toBe("0,2 çekirdek · limitin %20'si (limit şu an 1 çekirdek)");
    // Sayısal kolon başlığı sağa yaslı (F5 ui).
    const th = [...el2.querySelectorAll<HTMLElement>('thead th')].find(t => t.textContent?.startsWith('CPU'))!;
    expect(th.style.textAlign).toBe('right');
  });
});
