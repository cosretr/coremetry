// @vitest-environment jsdom
//
// TracePodPanel.render.test.tsx — v0.10.968 — Trace › Metrics seçili pod paneli
// ve pod odak görünümü (onaylı mockup, operatör "3 onay" 2026-09-27).
//
// v0.10.962 davranışları bu yüzeyde de pinli:
//   hata 2 — "Pod sayfasında aç" panelin penceresini (range + at) taşır;
//   hata 3 — pod başına hata ASLA boş sonuç gibi yazılmaz, "Yeniden dene" var;
//   hata 4 — tavanda aynı servisin seçisiz çipi devre dışı + gerekçe, tık
//            karşılaştırmayı değiştirmez; son çip çıkarılamaz (duyuru);
//   v0.10.916 — bellek CPU'dan önce, ikisi de zeroBase (pin buraya taşındı).
// TraceJvmPanel pinleri (traceMetrics.test.ts'ten taşındı): GC sonrası heap
// önce, jvm.gc.duration duruyor, sorgu anahtarı karşılaştırma kümesi DEĞİL
// queryPods (servisin trace'teki tüm pod'ları).
//
// Grafikler STUB (jsdom'da canvas yok, uPlot patlar); ölçülen şey hangi
// öğenin hangi rolle, hangi sırayla grafiğe girdiği.
//
// v0.10.976 — panel 400 px yan panel DEĞİL, tablonun satır altı KOMPAKT
// ayrıntısı (operatör: "inline daha iyi olur"). Başlık satırı: ad, kopyala,
// "Odak görünümü", "Pod sayfasında aç", "Span'ları Trace'te göster", ×.
// Gövde iki sütun: solda üç özet bloğu yan yana, sağda Karşılaştır + yan yana
// Bellek/CPU (168 px). JVM yalnız JVM servisinde, KAPALI açılır bölüm —
// açılmadan ClickHouse isteği YOK. v0.10.968'in her parçası yerinde (son
// describe bunu tek tek sayar).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, type ReactElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SpanRow } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// uPlot modül yüklenirken matchMedia çağırıyor (RuntimeCharts zinciri);
// import'lardan ÖNCE durmalı (emsal: TraceMetricsPanel.render.test.tsx).
vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

const m = vi.hoisted(() => ({
  runtimeCalls: 0,
  metricCalls: [] as { name: string; filters: string }[],
  // v0.10.968 — JVM sorgusu seri döndürsün (etiket/renk tutarlılığı testi).
  jvmPods: [] as string[],
  mlc: [] as { names: string[]; colors: (string | undefined)[] }[],
  copyOk: true,
}));
// v0.10.968 — kopyalama lib/clipboard'dan (düz HTTP yedeği); sonuç test kontrolünde.
vi.mock('@/lib/clipboard', () => ({ copyToClipboard: vi.fn(async () => m.copyOk) }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceRuntime: async () => { m.runtimeCalls++; return { language: 'java' }; },
      metricQuery: async (p: { name: string; filters: string }) => {
        m.metricCalls.push({ name: p.name, filters: p.filters });
        return m.jvmPods.map(pod => ({ groupKey: [pod], points: [{ time: T0, value: 1 }] }));
      },
    },
  };
});
vi.mock('@/components/chart/corePanelEntry', () => ({
  CorePanelMulti: (p: { unit?: string; height?: number; zeroBase?: boolean; items: { name: string; role?: string; color?: string }[]; thresholds?: { value: number }[]; regions?: { fromSec: number; toSec: number }[] }) => (
    <div data-cpm={p.unit} data-h={p.height} data-zero={String(!!p.zeroBase)}
      data-items={p.items.map(i => `${i.name}:${i.role}`).join('|')}
      data-colors={p.items.filter(i => i.role === 'data').map(i => `${i.name}=${i.color ?? ''}`).join('|')}
      data-thr={(p.thresholds ?? []).map(t => t.value).join('|')}
      data-region={(p.regions ?? []).map(r => `${r.fromSec}-${r.toSec}`).join('|')} />
  ),
}));
vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: ({ unit, series, seriesColors }: { unit?: string; series: { groupKey: string[] }[]; seriesColors?: ReadonlyMap<string, string> }) => {
    const names = series.map(x => x.groupKey.join(' · '));
    m.mlc.push({ names, colors: names.map(n => seriesColors?.get(n)) });
    return <div data-mlc={unit} />;
  },
}));

import { useState } from 'react';
import { TracePodPanel } from './TracePodPanel';
import { TracePodFocus } from './TracePodFocus';
import type {
  PodMetricSeries, PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo, TracePodPanelProps,
} from './traceMetricsModel';
import { CAP_REASON } from './tracePodPanelModel';

const T0 = 1_790_496_000 * 1e9;
const SVC = 'fraud-score-prod';

function pod(name: string, o: Partial<TracePodInfo> = {}): TracePodInfo {
  const mm = /^(.+)-([a-z0-9]{6,10})-([a-z0-9]{5})$/.exec(name);
  return {
    pod: name, namespace: 'payments', clusterValue: 'dc-east-1', service: SVC,
    spans: 5, errors: 0, maxSelfNs: 0, maxSelfSpanId: '', critNs: 0, critShare: 0,
    topSelf: false, rootCause: false, entry: false, firstError: null,
    activeFromNs: T0 + 0.294e9, activeToNs: T0 + 2.81e9, flagged: false, runtime: 'java',
    replicaSet: mm ? `${mm[1]}-${mm[2]}` : '',
    nameParts: mm ? { head: mm[1], rs: mm[2], tail: mm[3] } : { head: name, rs: '', tail: '' },
    ...o,
  };
}
function model(pods: TracePodInfo[], o: Partial<TraceMetricsModel> = {}): TraceMetricsModel {
  const bySvc = new Map<string, TracePodInfo[]>();
  for (const p of pods) bySvc.set(p.service, [...(bySvc.get(p.service) ?? []), p]);
  return {
    pods, groups: [...bySvc.entries()].map(([service, ps]) => ({
      service, pods: ps, spans: 0, errors: 0, maxSelfNs: 0, critShare: 0, flagged: 0, lead: false,
    })),
    byPod: new Map(pods.map(p => [p.pod, p])), podSpans: new Map(), criticalIds: new Set(), errorMarks: [],
    noPod: null, clusterValues: ['dc-east-1'], primaryCluster: 'dc-east-1',
    traceStartNs: T0, traceEndNs: T0 + 3.42e9, rootWallNs: 3.42e9, selfKnown: true, truncated: false,
    errorSpans: 0, rootCausePod: '', entryPod: '', counts: { all: pods.length, err: 0, crit: 0, slow: 0 },
    ...o,
  };
}
function series(o: Partial<PodMetricSeries> = {}): PodMetricSeries {
  return {
    startSec: T0 / 1e9 - 300, stepSec: 15, cpu: [0.4, 0.97, 0.5], mem: [1e9, 1.1e9, 1.2e9],
    cpuAt: 0.97, memAt: 1.42 * 1024 ** 3, cpuLimit: 1, memLimit: 2 * 1024 ** 3, cpuRequest: 0.5, memRequest: 1.5 * 1024 ** 3,
    cpuLevel: 'err', memLevel: 'warn', inventory: 'present', now: { phase: 'Running', restarts: 3, lastTermReason: 'OOMKilled', lastTermAtSec: T0 / 1e9 + 720 },
    cluster: { id: 'c-1', name: 'dc-east-1' }, namespace: 'payments', fetchedAtMs: T0 / 1e6 + 18 * 60_000,
    ...o,
  };
}
const ok = (o: Partial<PodMetricSeries> = {}): PodMetricState => ({ kind: 'ok', data: series(o) });

const WIN: TraceMetricsWindowInfo = {
  fromNs: T0 - 300e9, toNs: T0 + 3.42e9 + 300e9, startNs: T0, endNs: T0 + 3.42e9, win: 5, stepSec: 15, openWindow: false,
};

const SUF = ['m3t9w', 'h8k2v', 'p5r8d', 'w9z2c', 'k7n4b'];
const PODS = SUF.map((s, i) => pod(`${SVC}-6b7d9f8c5-${s}`, {
  spans: [61, 48, 22, 19, 14][i], errors: i === 0 ? 4 : 0, flagged: i < 2,
  ...(i === 0 ? {
    rootCause: true, critShare: 0.38, critNs: 1.3e9, topSelf: true, maxSelfNs: 1.21e9, maxSelfSpanId: 's-self',
    firstError: { spanId: 's-err', name: 'POST /v1/score', status: 'DEADLINE_EXCEEDED', timeNs: T0 + 1.802e9 },
  } : {}),
}));
const OTHER = pod('audit-log-prod-5f9c7d6b8-q2w4e', { service: 'audit-log-prod', flagged: true, errors: 1 });
const SPAN = (id: string, name: string) => ({ spanId: id, name, startTime: T0 + 0.3e9, durationMs: 1210, statusCode: 'ok' } as unknown as SpanRow);
const M = model([...PODS, OTHER], { podSpans: new Map([[PODS[0].pod, [SPAN('s-self', 'score.model.evaluate'), SPAN('s-err', 'POST /v1/score')]]]) });

function props(o: Partial<TracePodPanelProps> = {}): TracePodPanelProps {
  const compare = o.compare ?? [PODS[0].pod];
  return {
    model: M, selected: M.byPod.get(compare[0])!, compare, metrics: () => ok(), window: WIN,
    siblingLines: true, focus: false,
    onCompareChange: vi.fn(), onSelect: vi.fn(), onClose: vi.fn(), onToggleFocus: vi.fn(), onToggleSiblingLines: vi.fn(),
    onShowSpans: vi.fn(), onOpenSpan: vi.fn(), onRetry: vi.fn(), announce: vi.fn(),
    ...o,
  };
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient | null = null;
const wait = () => act(async () => { await new Promise(r => setTimeout(r, 20)); });

async function mount(el: ReactElement): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root = createRoot(host!);
    root.render(<QueryClientProvider client={qc!}><MemoryRouter>{el}</MemoryRouter></QueryClientProvider>);
  });
  await wait();
  await wait();
  return host!;
}
function rerender(el: ReactElement) {
  act(() => { root!.render(<QueryClientProvider client={qc!}><MemoryRouter>{el}</MemoryRouter></QueryClientProvider>); });
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null; qc = null;
  m.runtimeCalls = 0; m.metricCalls.length = 0;
  m.jvmPods = []; m.mlc.length = 0; m.copyOk = true;
});

// v0.10.968 — renkleri karşılaştırmak için CSS normalleştirme (jsdom hex → rgb).
const norm = (c: string | undefined) => { const d = document.createElement('span'); d.style.background = c ?? ''; return d.style.background; };

// v0.10.968 — onCompareChange'i gerçekten uygulayan kabuk (URL yerine yerel durum).
function Harness(o: Partial<TracePodPanelProps> & { initial: string[] }) {
  const [compare, setCompare] = useState(o.initial);
  const base = props({ ...o, compare });
  return <TracePodPanel {...base} selected={base.model.byPod.get(compare[0])!} onCompareChange={setCompare} />;
}

const buttons = (el: HTMLElement) => [...el.querySelectorAll<HTMLButtonElement>('button')];
const byText = (el: HTMLElement, t: string) => buttons(el).find(b => b.textContent?.includes(t));
// v0.10.976 — JVM açılır bölümünün başlık düğmesi (DisclosureButton, aria-expanded).
const jvmToggle = (el: HTMLElement) => buttons(el).find(b => b.hasAttribute('aria-expanded') && b.textContent?.includes('JVM · heap ve GC'))!;
const chip = (el: HTMLElement, podName: string) =>
  [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.title.startsWith(`${podName} · `) || (b.title === CAP_REASON && b.textContent?.includes(podName.slice(-5))))!;

describe('TracePodPanel — v0.10.968', () => {
  it('başlık, alt satır ve rozetler', async () => {
    const el = await mount(<TracePodPanel {...props()} />);
    expect(el.querySelector('.tpp-name')!.textContent).toBe(PODS[0].pod);
    expect(el.querySelector('.tpp-name [title]')!.getAttribute('title')).toBe(PODS[0].pod);
    const sub = el.querySelector('.tpp-sub')!.textContent!;
    expect(sub).toContain(SVC);
    expect(sub).toContain('ns payments');
    expect(sub).toContain('dc-east-1');
    expect(sub).toContain('java');
    const badges = [...el.querySelectorAll('.tpp-badges .badge')].map(b => [b.textContent, b.className.includes('b-err')]);
    expect(badges).toEqual([['kök hata', true], ['4 hata', true], ['kritik yol %38 · 1,30 sn', false], ['en büyük öz süre', false]]);
    // v0.10.976 — satır altı ayrıntı: "Genişlet" ikonu yerine "Odak görünümü" düğmesi, × "Ayrıntıyı kapat".
    for (const l of ['Pod adını kopyala', 'Ayrıntıyı kapat']) expect(el.querySelector(`[aria-label="${l}"]`)).not.toBeNull();
    expect(byText(el, 'Odak görünümü')).not.toBeUndefined();
    // "Bu trace'te": hata zamanı ve en büyük öz süre span'i açan bağlantılar.
    expect(el.textContent).toContain('4 · ilk: POST /v1/score · DEADLINE_EXCEEDED · ');
    // Trace anında: CPU hata tonunda + kısıtlama notu, bellek uyarı tonunda.
    expect(el.querySelector('.cell-err.cell-strong')!.textContent).toBe("0,97 çekirdek · limitin %97'si");
    expect(el.querySelector('.cell-warn.cell-strong')!.textContent).toBe("1,42 GiB · limitin %71'i");
    expect(el.textContent).toContain('Limite dayandı: CPU kısıtlaması (throttling) olası.');
    expect(el.textContent).toContain('Trace anında · Thanos, ±1 adım (15 sn)');
    // Şu an: OOMKilled tehlike rozeti, restart uyarı.
    expect([...el.querySelectorAll('.badge.b-err')].some(b => b.textContent === 'OOMKilled')).toBe(true);
    expect(el.textContent).toMatch(/Şu an \(trace anı değil\) · \d\d:\d\d itibarıyla · trace /);
  });

  it('bağlantı eylemleri: en büyük öz süre / ilk hata span\'i açar, span\'ları göster, kopyala duyurur', async () => {
    const p = props();
    const el = await mount(<TracePodPanel {...p} />);
    act(() => { byText(el, 'score.model.evaluate · 1,21 sn ↗')!.click(); });
    expect(p.onOpenSpan).toHaveBeenCalledWith('s-self');
    act(() => { buttons(el).find(b => b.className.includes('btn-link') && /^\d\d:\d\d:\d\d\.\d{3}$/.test(b.textContent ?? ''))!.click(); });
    expect(p.onOpenSpan).toHaveBeenLastCalledWith('s-err');
    act(() => { byText(el, "Span'ları Trace'te göster (61)")!.click(); });
    expect(p.onShowSpans).toHaveBeenCalledWith(PODS[0].pod);
    // v0.10.968 — lib/clipboard (asenkron); başarı hem duyurulur hem görünür not.
    await act(async () => { (el.querySelector('[aria-label="Pod adını kopyala"]') as HTMLButtonElement).click(); });
    expect(p.announce).toHaveBeenCalledWith(`Pod adı kopyalandı: ${PODS[0].pod}`);
    expect(el.querySelector('[data-panel-note]')!.textContent).toBe(`Pod adı kopyalandı: ${PODS[0].pod}`);
    act(() => { (el.querySelector('[aria-label="Ayrıntıyı kapat"]') as HTMLButtonElement).click(); });
    expect(p.onClose).toHaveBeenCalled();
  });

  it('hata 2 korunur: "Pod sayfasında aç" range + at taşır; cluster yoksa gizli', async () => {
    const el = await mount(<TracePodPanel {...props()} />);
    const a = [...el.querySelectorAll('a')].find(x => x.textContent?.includes('Pod sayfasında aç'))!;
    const q = new URLSearchParams(a.getAttribute('href')!.split('?')[1]);
    expect(q.get('range')).toBe(`custom:${Math.floor(WIN.fromNs / 1e6)}-${Math.ceil(WIN.toNs / 1e6)}`);
    expect(q.get('at')).toBe(String(Math.floor(T0 / 1e6)));
    expect(q.get('pod')).toBe(PODS[0].pod);
    expect(q.get('cluster')).toBe('dc-east-1');
    expect(q.get('namespace')).toBe('payments');
    act(() => { root?.unmount(); }); host?.remove();
    const el2 = await mount(<TracePodPanel {...props({ metrics: () => ({ kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' }) })} />);
    expect([...el2.querySelectorAll('a')].some(x => x.textContent?.includes('Pod sayfasında aç'))).toBe(false);
  });

  it('v0.10.916 pin: Bellek grafiği CPU\'dan önce, ikisi de zeroBase; kardeş soluk, trace bandı bir adım', async () => {
    const el = await mount(<TracePodPanel {...props({ compare: [PODS[0].pod, PODS[1].pod] })} />);
    const charts = [...el.querySelectorAll('[data-cpm]')];
    expect(charts.map(c => c.getAttribute('data-cpm'))).toEqual(['bytes', 'cores']);
    expect(charts.every(c => c.getAttribute('data-zero') === 'true')).toBe(true);
    expect(charts[0].getAttribute('data-items')).toBe('m3t9w:data|h8k2v:data|p5r8d:muted|w9z2c:muted|k7n4b:muted');
    expect(charts[1].getAttribute('data-thr')).toBe('1');
    expect(charts[0].getAttribute('data-region')).toBe(`${T0 / 1e9}-${T0 / 1e9 + 15}`);
    const titles = [...el.querySelectorAll('[data-chart] .tpp-sec-title')].map(t => t.textContent);
    expect(titles).toEqual(['Bellek (working set)', 'CPU (çekirdek)']);
    expect(el.querySelector('.tpp-caption')!.textContent).toContain(`Soluk çizgiler: ${SVC} servisinin bu trace'teki diğer 3 pod'u.`);
  });

  it('hata 3 korunur: error → "Veri okunamadı — bu bir hata, boş sonuç değil." + Yeniden dene; no_samples ve unmapped metinleri', async () => {
    const p = props({ metrics: () => ({ kind: 'error', message: 'upstream: dc-east-1: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)', timeout: true }) });
    const el = await mount(<TracePodPanel {...p} />);
    const msg = el.querySelector('[data-state="error"]')!;
    expect(msg.classList.contains('is-err')).toBe(true);
    expect(msg.textContent).toContain('Veri okunamadı — bu bir hata, boş sonuç değil.');
    expect(msg.textContent).toContain('10 sn zaman aşımı. Diğer cluster\'ların metrikleri etkilenmedi.');
    expect(msg.textContent).not.toContain('örnek yok');
    expect(el.querySelector('[data-cpm]')).toBeNull();
    act(() => { byText(el, 'Yeniden dene')!.click(); });
    expect(p.onRetry).toHaveBeenCalledWith(PODS[0].pod);

    act(() => { root?.unmount(); }); host?.remove();
    const el2 = await mount(<TracePodPanel {...props({ metrics: () => ({ kind: 'no_samples', cluster: { id: 'c-1', name: 'dc-east-1' } }) })} />);
    expect(el2.querySelector('[data-state="no_samples"]')!.textContent)
      .toContain('Bu pencerede Thanos örneği yok.Cluster eşli ve sorgu başarılı, ama bu pod için CPU / bellek serisi dönmedi.');
    expect(el2.querySelector('.is-err')).toBeNull();

    act(() => { root?.unmount(); }); host?.remove();
    const el3 = await mount(<TracePodPanel {...props({ metrics: () => ({ kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' }) })} />);
    const u = el3.querySelector('[data-state="unmapped"]')!;
    expect(u.textContent).toContain("Bu pod'un cluster'ı (dc-east-2) bir Remote Cluster kaydına eşlenmemiş — metrik gösterilemiyor.");
    expect(u.querySelector('a')!.getAttribute('href')).toBe('/settings/clusters');
    expect(el3.textContent).toContain('Cluster eşlenmemiş: faz, restart ve limitler okunamıyor.');
  });

  it('hata 4 korunur: 4 karşılaştırmada seçisiz aynı-servis çipi devre dışı + gerekçe; tık compare\'i değiştirmez', async () => {
    const cmp = PODS.slice(0, 4).map(p => p.pod);
    const p = props({ compare: cmp });
    const el = await mount(<TracePodPanel {...p} />);
    const blocked = [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.textContent?.includes('k7n4b'))!;
    expect(blocked.disabled).toBe(true);
    expect(blocked.title).toBe(CAP_REASON);
    act(() => { blocked.click(); });
    expect(p.onCompareChange).not.toHaveBeenCalled();
    expect([...el.querySelectorAll('.tpp-note')].some(n => n.textContent === CAP_REASON)).toBe(true);
    expect(el.textContent).toContain('Karşılaştır · aynı servisten (4/4)');
    // Seçili çipler görünür (active + aria-pressed) ve açık.
    const on = chip(el, PODS[1].pod);
    expect(on.classList.contains('active')).toBe(true);
    expect(on.getAttribute('aria-pressed')).toBe('true');
    expect(on.disabled).toBe(false);
    // v0.10.962 hata 1 — seçisiz çip seçiliden AYIRT edilir (tint yalnız `active`).
    expect(blocked.classList.contains('active')).toBe(false);
    expect(blocked.classList.contains('ch-accent')).toBe(false);
    expect(blocked.getAttribute('aria-pressed')).toBe('false');
  });

  it('seçili çipi çıkarmak sıradakini öne alır; son çip çıkarılamaz (duyuru); 4. ekleme tavanı duyurur', async () => {
    const p = props({ compare: [PODS[0].pod, PODS[1].pod] });
    const el = await mount(<TracePodPanel {...p} />);
    act(() => { chip(el, PODS[0].pod).click(); });
    expect(p.onCompareChange).toHaveBeenCalledWith([PODS[1].pod]);

    act(() => { root?.unmount(); }); host?.remove();
    const p2 = props({ compare: [PODS[0].pod] });
    const el2 = await mount(<TracePodPanel {...p2} />);
    expect(chip(el2, PODS[0].pod).title).toBe(`${PODS[0].pod} · son pod karşılaştırmadan çıkarılamaz`);
    act(() => { chip(el2, PODS[0].pod).click(); });
    expect(p2.onCompareChange).not.toHaveBeenCalled();
    expect(p2.announce).toHaveBeenCalledWith('Son pod karşılaştırmadan çıkarılamaz.');
    // v0.10.968 (MF-8) — ret GÖRÜNÜR: gören kullanıcı için de sessiz değil.
    expect(el2.querySelector('[data-panel-note]')!.textContent).toBe('Son pod karşılaştırmadan çıkarılamaz.');

    act(() => { root?.unmount(); }); host?.remove();
    const p3 = props({ compare: PODS.slice(0, 3).map(x => x.pod) });
    const el3 = await mount(<TracePodPanel {...p3} />);
    act(() => { chip(el3, PODS[3].pod).click(); });
    expect(p3.onCompareChange).toHaveBeenCalledWith(PODS.slice(0, 4).map(x => x.pod));
    expect(p3.announce).toHaveBeenCalledWith('Karşılaştırmada en çok 4 pod.');
  });

  it('eşlenmemiş kardeş çipi "metrik yok (<cl> eşlenmemiş)" gerekçesiyle devre dışı', async () => {
    const st = (x: string): PodMetricState => (x === PODS[2].pod ? { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' } : ok());
    const el = await mount(<TracePodPanel {...props({ metrics: st })} />);
    const c = [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.textContent?.includes('p5r8d'))!;
    expect(c.disabled).toBe(true);
    expect(c.title).toBe(`${PODS[2].pod} · metrik yok (dc-east-2 eşlenmemiş)`);
  });

  it('"+N" taşma menüsü: menuitemcheckbox satırları, metriksiz satır aria-disabled + gerekçe duyurusu', async () => {
    const many = ['a1', 'b2', 'c3', 'd4', 'e5', 'f6', 'g7', 'h8'].map(s => pod(`${SVC}-6b7d9f8c5-${s}xyz`));
    const mm = model(many);
    const st = (x: string): PodMetricState => (x === many[7].pod ? { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' } : ok());
    const p = props({ model: mm, selected: many[0], compare: [many[0].pod], metrics: st });
    const el = await mount(<TracePodPanel {...p} />);
    const more = [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.textContent === '+2')!;
    expect(more.getAttribute('aria-haspopup')).toBe('menu');
    expect(more.getAttribute('aria-expanded')).toBe('false');
    act(() => { more.click(); });
    await wait();
    const menu = el.querySelector('[role="menu"]')!;
    expect(menu.getAttribute('aria-label')).toBe(`${SVC} için diğer pod'lar`);
    const rows = [...menu.querySelectorAll<HTMLButtonElement>('[role="menuitemcheckbox"]')];
    expect(rows.map(r => [r.textContent, r.getAttribute('aria-checked'), r.getAttribute('aria-disabled')])).toEqual([
      ['g7xyz · 5', 'false', null],
      ['h8xyz · 5 · metrik yok', 'false', 'true'],
    ]);
    expect(rows[1].title).toBe(`${many[7].pod} · metrik yok (dc-east-2 eşlenmemiş)`);
    act(() => { rows[1].click(); });
    expect(p.onCompareChange).not.toHaveBeenCalled();
    expect(p.announce).toHaveBeenCalledWith(`${many[7].pod} · metrik yok (dc-east-2 eşlenmemiş)`);
    act(() => { rows[0].click(); });
    expect(p.onCompareChange).toHaveBeenCalledWith([many[0].pod, many[6].pod]);
  });

  it('runtime go → "JVM paneli yok"; java → TraceJvmPanel queryPods = servisin tüm pod\'ları, runtime isteği YOK', async () => {
    const goSel = { ...PODS[0], runtime: 'go' };
    const el = await mount(<TracePodPanel {...props({ selected: goSel })} />);
    expect(el.textContent).toContain('JVM paneli yok · runtime: go');
    expect(m.metricCalls).toHaveLength(0);

    act(() => { root?.unmount(); }); host?.remove();
    const el2 = await mount(<TracePodPanel {...props({ compare: [PODS[0].pod, PODS[1].pod] })} />);
    await wait();
    expect(el2.textContent).toContain('JVM · heap ve GC');
    // v0.10.976 — kompakt ayrıntıda JVM KAPALI açılır bölüm: açılmadan istek YOK (ES/CH maliyet disiplini).
    expect(m.metricCalls).toHaveLength(0);
    act(() => { jvmToggle(el2).click(); });
    await wait();
    expect(m.runtimeCalls).toBe(0);
    expect(m.metricCalls.map(c => c.name).sort()).toEqual(['jvm.gc.duration', 'jvm.memory.used', 'jvm.memory.used_after_last_gc']);
    const inList = JSON.parse(m.metricCalls[0].filters).find((f: { op: string }) => f.op === 'IN').v;
    expect(inList).toEqual(PODS.map(p => p.pod));

    act(() => { root?.unmount(); }); host?.remove();
    const el3 = await mount(<TracePodPanel {...props({ selected: { ...PODS[0], runtime: '' } })} />);
    expect(el3.textContent).not.toContain('JVM paneli yok');
    expect(el3.textContent).not.toContain('JVM · heap ve GC');
  });
});

describe('TracePodFocus — v0.10.968', () => {
  const fp = (o: Partial<TracePodPanelProps> = {}) => props({ focus: true, ...o });

  it('ilk işaretli pod\'da "Önceki" devre dışı + gerekçe; "]" sonraki işaretliyi seçer; ray vurgusu; tabloya dön', async () => {
    const p = fp();
    const el = await mount(<TracePodFocus {...p} />);
    const prev = byText(el, '‹ Önceki işaretli')!;
    expect(prev.disabled).toBe(true);
    expect(prev.title).toBe('Bu, ilk işaretli pod');
    expect(byText(el, 'Sonraki işaretli ›')!.disabled).toBe(false);
    act(() => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: ']', bubbles: true })); });
    expect(p.onSelect).toHaveBeenCalledWith(PODS[1].pod);
    act(() => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '[', bubbles: true })); });
    expect(p.onSelect).toHaveBeenCalledTimes(1);          // önceki yok → no-op
    const rail = [...el.querySelectorAll('.tpp-rail-item')];
    expect(rail.map(r => r.getAttribute('title'))).toEqual([PODS[0].pod, PODS[1].pod, OTHER.pod]);
    const cur = el.querySelector('.tpp-rail-item.is-current')!;
    expect(cur.getAttribute('title')).toBe(PODS[0].pod);
    expect(cur.getAttribute('aria-current')).toBe('true');
    act(() => { (rail[2] as HTMLButtonElement).click(); });
    expect(p.onSelect).toHaveBeenLastCalledWith(OTHER.pod);
    act(() => { byText(el, `Tüm ${M.pods.length} pod'u tabloda göster`)!.click(); });
    expect(p.onToggleFocus).toHaveBeenCalledTimes(1);
    expect(el.querySelector('nav[aria-label="Konum"]')!.textContent).toBe(`${SVC}›m3t9w`);
    expect(el.textContent).toContain('ReplicaSet fraud-score-prod-6b7d9f8c5');
    expect(el.textContent).toContain("Metrik grafiklerinde bu 3,42 sn tek bir 15 sn'lik adıma düşer.");
    expect(el.querySelector('[role="img"][aria-label^="Bu pod\'un span\'ları"]')).not.toBeNull();
  });

  it('son işaretli pod\'da "Sonraki" devre dışı; bağlanmadan çözülünce kısayol kalkar', async () => {
    const p = fp({ compare: [OTHER.pod], selected: OTHER });
    const el = await mount(<TracePodFocus {...p} />);
    const next = byText(el, 'Sonraki işaretli ›')!;
    expect(next.disabled).toBe(true);
    expect(next.title).toBe('Bu, son işaretli pod');
    act(() => { root?.unmount(); });
    root = null;
    act(() => { document.body.dispatchEvent(new KeyboardEvent('keydown', { key: '[', bubbles: true })); });
    expect(p.onSelect).not.toHaveBeenCalled();
  });
});

describe('TraceJvmPanel — v0.10.968 pinleri (traceMetrics.test.ts\'ten taşındı)', () => {
  it('GC sonrası heap önce, anlık kullanım yedek; jvm.gc.duration duruyor; runtime verilince istek yok', async () => {
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const jvm = readFileSync(resolve(__dirname, 'TraceJvmPanel.tsx'), 'utf8');
    expect(jvm.indexOf("'jvm.memory.used_after_last_gc'")).toBeLessThan(jvm.indexOf("'jvm.memory.used'"));
    expect(jvm).toContain("'jvm.gc.duration'");
    expect(jvm).toMatch(/enabled: !!service && !runtime/);
  });

  it('sorgu anahtarı karşılaştırma kümesi DEĞİL queryPods; compare değişince yeniden istek YOK', async () => {
    const { TraceJvmPanel } = await import('./TraceJvmPanel');
    const all = PODS.map(p => p.pod);
    const el = <TraceJvmPanel service={SVC} pods={[all[0]]} queryPods={all} runtime="java" from={WIN.fromNs} to={WIN.toNs} syncKey="k" maxDataPoints={120} />;
    await mount(el);
    await wait();
    const keys = qc!.getQueryCache().findAll({ queryKey: ['trace-jvm'] }).map(q => q.queryKey);
    expect(keys).toHaveLength(3);
    for (const k of keys) expect(k[3]).toBe(all.join(','));
    const n = m.metricCalls.length;
    expect(n).toBe(3);
    rerender(<TraceJvmPanel service={SVC} pods={[all[0], all[1]]} queryPods={all} runtime="java" from={WIN.fromNs} to={WIN.toNs} syncKey="k" maxDataPoints={120} />);
    await wait();
    expect(m.metricCalls.length).toBe(n);
    expect(m.runtimeCalls).toBe(0);
  });
});

// ── v0.10.968 — inceleme turu ───────────────────────────────────────────────
describe('TracePodPanel — v0.10.968 inceleme turu', () => {
  it('kopyalama başarısızsa "kopyalanamadı" der, "kopyalandı" DEMEZ (düz HTTP)', async () => {
    m.copyOk = false;
    const p = props();
    const el = await mount(<TracePodPanel {...p} />);
    await act(async () => { (el.querySelector('[aria-label="Pod adını kopyala"]') as HTMLButtonElement).click(); });
    expect(p.announce).toHaveBeenCalledWith(`Pod adı kopyalanamadı: ${PODS[0].pod}`);
    expect(p.announce).not.toHaveBeenCalledWith(`Pod adı kopyalandı: ${PODS[0].pod}`);
  });

  it('MF-1: hash yuvası çakışan iki pod (p5r8d / b2q6f) farklı çizgi rengi; çip swatch\'ı çizgiyle aynı', async () => {
    const a = pod(`${SVC}-6b7d9f8c5-p5r8d`, { flagged: true });
    const b = pod(`${SVC}-6b7d9f8c5-b2q6f`, { flagged: true });
    const mm = model([a, b]);
    const el = await mount(<TracePodPanel {...props({ model: mm, selected: a, compare: [a.pod, b.pod] })} />);
    const mem = el.querySelector('[data-cpm="bytes"]')!;
    const pairs = Object.fromEntries(mem.getAttribute('data-colors')!.split('|').map(x => x.split('=')));
    expect(pairs.p5r8d).toBeTruthy();
    expect(pairs.p5r8d).not.toBe(pairs.b2q6f);
    const sw = (label: string) => [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')]
      .find(c => c.textContent?.includes(label))!.querySelector<HTMLElement>('.tpp-swatch')!.style.background;
    expect(sw('p5r8d')).toBe(norm(pairs.p5r8d));
    expect(sw('b2q6f')).toBe(norm(pairs.b2q6f));
    // Lejant swatch'ı da aynı.
    const leg = [...mem.parentElement!.querySelectorAll<HTMLElement>('.tpp-legend .tpp-swatch')].map(x => x.style.background);
    expect(leg).toEqual([norm(pairs.p5r8d), norm(pairs.b2q6f)]);
  });

  it('MF-2: JVM serileri çiple AYNI etiket ve renk (tek pod = dört grafikte tek renk)', async () => {
    const cmp = [PODS[0].pod, PODS[1].pod];
    m.jvmPods = PODS.map(p => p.pod);
    const el = await mount(<TracePodPanel {...props({ compare: cmp })} />);
    act(() => { jvmToggle(el).click(); }); // v0.10.976 — JVM bölümü kapalı başlar
    await wait();
    await wait();
    expect(m.mlc.length).toBeGreaterThan(0);
    const last = m.mlc[m.mlc.length - 1];
    expect(last.names).toEqual(['m3t9w', 'h8k2v']);
    const mem = el.querySelector('[data-cpm="bytes"]')!;
    const pairs = Object.fromEntries(mem.getAttribute('data-colors')!.split('|').map(x => x.split('=')));
    expect(last.colors).toEqual([pairs.m3t9w, pairs.h8k2v]);
  });

  it('F4 ui: "+N" menüsünde işaretlenen satır menüde kalır ve odağı tutar; taşma boşalınca menü kendiliğinden AÇILMAZ', async () => {
    const seven = ['a1', 'b2', 'c3', 'd4', 'e5', 'f6', 'g7'].map(x => pod(`${SVC}-6b7d9f8c5-${x}xyz`));
    const mm = model(seven);
    const el = await mount(<Harness model={mm} initial={[seven[0].pod]} metrics={() => ok()} />);
    const more = () => [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => /^\+\d+$/.test(b.textContent ?? ''));
    act(() => { more()!.click(); });
    await wait();
    const row = () => el.querySelector<HTMLButtonElement>('[role="menu"] [role="menuitemcheckbox"]');
    expect(document.activeElement).toBe(row());
    act(() => { row()!.click(); });
    await wait();
    // Menü açık, satır DOM'da, işaretli ve odaklı (odak <body>'ye düşmedi).
    expect(el.querySelector('[role="menu"]')).not.toBeNull();
    expect(row()!.getAttribute('aria-checked')).toBe('true');
    expect(document.activeElement).toBe(row());
    expect(more()!.textContent).toBe('+1');
    // Kapat: g7 görünür çiplere akar, taşma boşalır.
    act(() => { more()!.click(); });
    await wait();
    expect(el.querySelector('[role="menu"]')).toBeNull();
    expect(more()).toBeUndefined();
    const g7 = [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.textContent?.includes('g7xyz'))!;
    expect(g7.classList.contains('active')).toBe(true);
    // g7'yi çıkar → taşmaya geri döner: menü AÇILMAZ, odak "+1"e geçer.
    act(() => { g7.focus(); g7.click(); });
    await wait();
    expect(el.querySelector('[role="menu"]')).toBeNull();
    expect(more()!.textContent).toBe('+1');
    expect(document.activeElement).toBe(more());
  });

  it('F4 ui: menüde işaretlemeden sonra ↓ hâlâ menüde gezinir', async () => {
    const eight = ['a1', 'b2', 'c3', 'd4', 'e5', 'f6', 'g7', 'h8'].map(x => pod(`${SVC}-6b7d9f8c5-${x}xyz`));
    const el = await mount(<Harness model={model(eight)} initial={[eight[0].pod]} metrics={() => ok()} />);
    const more = [...el.querySelectorAll<HTMLButtonElement>('button.btn-chip')].find(b => b.textContent === '+2')!;
    act(() => { more.click(); });
    await wait();
    const rows = () => [...el.querySelectorAll<HTMLButtonElement>('[role="menu"] [role="menuitemcheckbox"]')];
    act(() => { rows()[0].click(); });
    await wait();
    expect(rows().map(r => r.getAttribute('aria-checked'))).toEqual(['true', 'false']);
    expect(document.activeElement).toBe(rows()[0]);
    act(() => { document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true })); });
    expect(document.activeElement).toBe(rows()[1]);
  });

  it('F6 ui: "Yeniden dene" basılıp durum yükleniyor olunca odak mesaj kutusunda kalır', async () => {
    const err: PodMetricState = { kind: 'error', message: 'dc-east-1: CPU sorgusu: HTTP 500 internal: boom', timeout: false };
    const p = props({ metrics: () => err });
    const el = await mount(<TracePodPanel {...p} />);
    const btn = byText(el, 'Yeniden dene')!;
    act(() => { btn.focus(); btn.click(); });
    expect(p.onRetry).toHaveBeenCalledWith(PODS[0].pod);
    rerender(<TracePodPanel {...props({ metrics: () => ({ kind: 'loading' }) })} />);
    const a = document.activeElement as HTMLElement;
    expect(a).not.toBe(document.body);
    expect(a.getAttribute('aria-busy')).toBe('true');
  });

  it('F6: bütün kardeşleri okunamayan pod — "Kardeşlere göre" hata tonunda "okunamadı"', async () => {
    const err: PodMetricState = { kind: 'error', message: 'x', timeout: false };
    const el = await mount(<TracePodPanel {...props({ metrics: pp => (pp === PODS[0].pod ? ok() : err) })} />);
    const seg = [...el.querySelectorAll('.cell-err.cell-strong')].find(x => x.textContent?.includes('okunamadı'))!;
    expect(seg.textContent).toBe("Bu trace'teki diğer 4 pod'un metrikleri okunamadı — kıyaslanacak kardeş yok");
    expect(el.textContent).not.toContain('metriği yok');
  });

  it('F1 ui: odak görünümü bağlanırken sahipsiz odak "← Tüm pod\'lar"a konur', async () => {
    const el = await mount(<TracePodFocus {...props({ focus: true })} />);
    expect(document.activeElement).toBe(byText(el, "← Tüm pod'lar"));
  });
});

// ── v0.10.976 — satır altı KOMPAKT ayrıntı (operatör: "inline daha iyi olur") ─
describe('TracePodPanel — v0.10.976 kompakt satır altı ayrıntı', () => {
  const secLabels = (root: Element) => [...root.querySelectorAll(':scope > section')].map(s => s.getAttribute('aria-label'));

  it('başlık satırı: ad (ortadan kırpma), kopyala, Odak görünümü, Pod sayfasında aç, Span\'ları göster, ×', async () => {
    const p = props();
    const el = await mount(<TracePodPanel {...p} />);
    const head = el.querySelector('.tpp-head')!;
    expect(head.querySelector('.tpp-name .mid-ellipsis')!.getAttribute('title')).toBe(PODS[0].pod);
    expect(head.querySelector('[aria-label="Pod adını kopyala"]')).not.toBeNull();
    const focusBtn = [...head.querySelectorAll('button')].find(b => b.textContent?.includes('Odak görünümü'))!;
    act(() => { focusBtn.click(); });
    expect(p.onToggleFocus).toHaveBeenCalledTimes(1);
    expect([...head.querySelectorAll('a')].some(a => a.textContent?.includes('Pod sayfasında aç'))).toBe(true);
    const spans = [...head.querySelectorAll('button')].find(b => b.textContent?.includes("Span'ları Trace'te göster (61)"))!;
    act(() => { spans.click(); });
    expect(p.onShowSpans).toHaveBeenCalledWith(PODS[0].pod);
    const close = head.querySelector<HTMLButtonElement>('[aria-label="Ayrıntıyı kapat"]')!;
    act(() => { close.click(); });
    expect(p.onClose).toHaveBeenCalledTimes(1);
    // Eski "Genişlet" ikon düğmesi yok (tek odak yolu: "Odak görünümü").
    expect(el.querySelector('[aria-label="Genişlet"]')).toBeNull();
    expect(el.querySelector('[aria-label="Paneli kapat"]')).toBeNull();
  });

  it('gövde iki sütun: solda üç özet bloğu yan yana (Bu trace\'te / Trace anında / Şu an), sağda Karşılaştır + yan yana Bellek/CPU 168 px', async () => {
    const el = await mount(<TracePodPanel {...props({ compare: [PODS[0].pod, PODS[1].pod] })} />);
    const body = el.querySelector('.tpp-detail-body')!;
    expect(body).not.toBeNull();
    const left = body.querySelector('.tpp-kv3')!;
    expect(secLabels(left)).toEqual(["Bu trace'te", 'Trace anında', 'Şu an']);
    expect(left.querySelectorAll('dl.keyval')).toHaveLength(3);
    const right = body.querySelector('.tpp-detail-charts')!;
    expect(right.querySelector('section[aria-label="Karşılaştır"]')).not.toBeNull();
    const row = right.querySelector('.tpp-chart-row')!;
    expect([...row.querySelectorAll('[data-chart]')].map(c => c.getAttribute('data-chart'))).toEqual(['mem', 'cpu']);
    // v0.10.976 (inceleme) — her grafik adlandırılmış grup (CorePanel'e title="" gider, h3 boş; ad kaptan).
    expect([...row.querySelectorAll('[data-chart][role="group"]')].map(c => c.getAttribute('aria-label')))
      .toEqual(['Bellek (working set)', 'CPU (çekirdek)']);
    // v0.10.976 (inceleme) — grafik satırı İÇSEL auto-fit: kap 2×240 px'ten darsa alt alta
    // (viewport eşiği yan çubuğun 220/56 px'ini göremiyordu; 1025 px'te grafik ~158 px'e düşüyordu).
    const { readFileSync } = await import('node:fs');
    const { resolve } = await import('node:path');
    const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
    expect(css).toMatch(/\.tpp-chart-row \{[^}]*repeat\(auto-fit, minmax\(min\(240px, 100%\), 1fr\)\)/);
    const charts = [...row.querySelectorAll('[data-cpm]')];
    expect(charts.map(c => c.getAttribute('data-h'))).toEqual(['168', '168']);
    // Limit çizgisi, etkin aralık bandı, kardeş çizgileri (muted) — hepsi kompakt grafikte.
    expect(charts[1].getAttribute('data-thr')).toBe('1');
    expect(charts[0].getAttribute('data-region')).toBe(`${T0 / 1e9}-${T0 / 1e9 + 15}`);
    expect(charts[0].getAttribute('data-items')).toBe('m3t9w:data|h8k2v:data|p5r8d:muted|w9z2c:muted|k7n4b:muted');
    // Çipler + "Kardeş çizgileri" çipi sağ sütunda, grafiklerin ÜSTÜNDE.
    const chips = right.querySelector('.tpp-chips')!;
    expect(chips.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect([...right.querySelectorAll('button.btn-chip')].some(b => b.textContent === 'Kardeş çizgileri')).toBe(true);
    // Açıklama ve "Kaynak" dipnotu DEĞİL: dipnot sekme altında kalır (kabuk), burada yalnız grafik açıklaması.
    expect(right.querySelector('.tpp-caption')!.textContent).toContain('Soluk çizgiler');
    expect(el.textContent).not.toContain('Kaynak: trace span');
  });

  it('"+N" taşma menüsü kompakt sağ sütunda da çalışır', async () => {
    const many = ['a1', 'b2', 'c3', 'd4', 'e5', 'f6', 'g7', 'h8'].map(s => pod(`${SVC}-6b7d9f8c5-${s}xyz`));
    const el = await mount(<TracePodPanel {...props({ model: model(many), selected: many[0], compare: [many[0].pod] })} />);
    const more = [...el.querySelectorAll<HTMLButtonElement>('.tpp-detail-charts button.btn-chip')].find(b => b.textContent === '+2')!;
    expect(more).not.toBeUndefined();
    act(() => { more.click(); });
    await wait();
    expect(el.querySelector('[role="menu"] [role="menuitemcheckbox"]')).not.toBeNull();
  });

  it('v0.10.968\'in her parçası yerinde (yan panelden düşen YOK)', async () => {
    const el = await mount(<TracePodPanel {...props()} />);
    const t = el.textContent!;
    // alt satır + rozetler
    for (const s of [SVC, 'ns payments', 'dc-east-1', 'java']) expect(el.querySelector('.tpp-sub')!.textContent).toContain(s);
    expect([...el.querySelectorAll('.tpp-badges .badge')].map(b => b.textContent)).toEqual(['kök hata', '4 hata', 'kritik yol %38 · 1,30 sn', 'en büyük öz süre']);
    // Bu trace'te
    for (const k of ['Span', 'Hata', 'En büyük öz süre', 'Kritik yol payı', 'Etkin aralık']) expect(t).toContain(k);
    expect(t).toContain('4 · ilk: POST /v1/score · DEADLINE_EXCEEDED · ');
    // Trace anında
    expect(t).toContain('Trace anında · Thanos, ±1 adım (15 sn)');
    for (const k of ['CPU', 'Bellek', 'Kardeşlere göre']) expect(t).toContain(k);
    expect(t).toContain('Limite dayandı: CPU kısıtlaması (throttling) olası.');
    // Karşılaştır
    expect(t).toContain('Karşılaştır · aynı servisten (1/4)');
    // Grafikler
    expect([...el.querySelectorAll('[data-chart] .tpp-sec-title')].map(x => x.textContent)).toEqual(['Bellek (working set)', 'CPU (çekirdek)']);
    expect(el.querySelector('.tpp-legend')!.textContent).toContain('limit');
    // JVM (java) — kapalı açılır bölüm
    expect(jvmToggle(el).getAttribute('aria-expanded')).toBe('false');
    // Şu an
    expect(t).toMatch(/Şu an \(trace anı değil\) · \d\d:\d\d itibarıyla · trace /);
    expect([...el.querySelectorAll('.badge.b-err')].some(b => b.textContent === 'OOMKilled')).toBe(true);
    for (const k of ['Faz', 'Restart']) expect(t).toContain(k);
  });

  it('JVM: yalnız JVM servisinde ve KAPALI başlar; açılınca sorgu gider ve panel çizilir; go → not; runtime yok → hiç', async () => {
    m.jvmPods = PODS.map(p => p.pod); // seri dönsün ki MultiLineChart bağlansın (MF-2 emsali)
    const el = await mount(<TracePodPanel {...props()} />);
    const tg = jvmToggle(el);
    expect(tg.getAttribute('aria-expanded')).toBe('false');
    expect(m.metricCalls).toHaveLength(0);
    expect(el.querySelector('[data-mlc]')).toBeNull();
    act(() => { tg.click(); });
    await wait();
    await wait(); // sorgu → seri → MultiLineChart (iki tik)
    expect(jvmToggle(el).getAttribute('aria-expanded')).toBe('true');
    expect(m.metricCalls.length).toBe(3);
    expect(el.querySelector('[data-mlc]')).not.toBeNull();
    // Tekrar kapat: panel kalkar (istek sayısı değişmez).
    act(() => { jvmToggle(el).click(); });
    await wait();
    expect(el.querySelector('[data-mlc]')).toBeNull();
    expect(m.metricCalls.length).toBe(3);

    act(() => { root?.unmount(); }); host?.remove();
    const el2 = await mount(<TracePodPanel {...props({ selected: { ...PODS[0], runtime: 'go' } })} />);
    expect(el2.textContent).toContain('JVM paneli yok · runtime: go');
    expect([...el2.querySelectorAll('button')].some(b => b.hasAttribute('aria-expanded'))).toBe(false);

    act(() => { root?.unmount(); }); host?.remove();
    const el3 = await mount(<TracePodPanel {...props({ selected: { ...PODS[0], runtime: '' } })} />);
    expect(el3.textContent).not.toContain('JVM');
  });

  it('metrik hatası: sağ sütunda durum kutusu (grafiğin yerine, Yeniden dene), solda Bu trace\'te + Şu an', async () => {
    const p = props({ metrics: () => ({ kind: 'error', message: 'x', timeout: false }) });
    const el = await mount(<TracePodPanel {...p} />);
    const body = el.querySelector('.tpp-detail-body')!;
    expect(secLabels(body.querySelector('.tpp-kv3')!)).toEqual(["Bu trace'te", 'Şu an']);
    const right = body.querySelector('.tpp-detail-charts')!;
    expect(right.querySelector('[data-state="error"]')).not.toBeNull();
    expect(right.querySelector('[data-cpm]')).toBeNull();
    expect(right.querySelector('section[aria-label="Karşılaştır"]')).toBeNull();
    act(() => { byText(el, 'Yeniden dene')!.click(); });
    expect(p.onRetry).toHaveBeenCalledWith(PODS[0].pod);
  });
});
