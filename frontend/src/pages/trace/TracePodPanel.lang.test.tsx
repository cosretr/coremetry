// @vitest-environment jsdom
//
// v0.10.2316 — pin: arayüz dili İngilizceyken Trace › Metrics pod paneli
// (başlık düğmeleri, rozetler, grafik başlıkları, özet satırı, durum
// mesajları, Teknik ayrıntı: Bu trace'te / Trace anında / Şu an, açıklama,
// JVM notu) bilinen Türkçe metin ÇİZMEZ (metin + title / aria-label); sayılar
// İngilizce biçimde (ondalık nokta, "cores", "97%"). Türkçe arayüzde aynen.
import { describe, it, expect, vi } from 'vitest';
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
  lang: 'en' as 'en' | 'tr',
  runtimeCalls: 0,
  metricCalls: [] as { name: string; filters: string }[],
  // v0.10.968 — JVM sorgusu seri döndürsün (etiket/renk tutarlılığı testi).
  jvmPods: [] as string[],
  mlc: [] as { names: string[]; colors: (string | undefined)[] }[],
  copyOk: true,
}));
// v0.10.968 — kopyalama lib/clipboard'dan (düz HTTP yedeği); sonuç test kontrolünde.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
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

import { TracePodPanel } from './TracePodPanel';
import type {
  PodMetricSeries, PodMetricState, TraceMetricsModel, TraceMetricsWindowInfo, TracePodInfo, TracePodPanelProps,
} from './traceMetricsModel';

const T0 = 1_790_496_000 * 1e9;
const SVC = 'svc-a';

function pod(name: string, o: Partial<TracePodInfo> = {}): TracePodInfo {
  const mm = /^(.+)-([a-z0-9]{6,10})-([a-z0-9]{5})$/.exec(name);
  return {
    pod: name, namespace: 'ns-a', clusterValue: 'cl-a', service: SVC,
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
    noPod: null, clusterValues: ['cl-a'], primaryCluster: 'cl-a',
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
    cluster: { id: 'c-1', name: 'cl-a' }, namespace: 'ns-a', fetchedAtMs: T0 / 1e6 + 18 * 60_000,
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
    firstError: { spanId: 's-err', name: 'op-a', status: 'DEADLINE_EXCEEDED', timeNs: T0 + 1.802e9 },
  } : {}),
}));
const OTHER = pod('svc-b-5f9c7d6b8-q2w4e', { service: 'svc-b', flagged: true, errors: 1 });
const SPAN = (id: string, name: string) => ({ spanId: id, name, startTime: T0 + 0.3e9, durationMs: 1210, statusCode: 'ok' } as unknown as SpanRow);
const M = model([...PODS, OTHER], { podSpans: new Map([[PODS[0].pod, [SPAN('s-self', 'op-b'), SPAN('s-err', 'op-a')]]]) });

function props(o: Partial<TracePodPanelProps> = {}): TracePodPanelProps {
  return {
    model: M, selected: PODS[0], metrics: () => ok(), window: WIN,
    onClose: vi.fn(), onShowSpans: vi.fn(), onOpenSpan: vi.fn(), onRetry: vi.fn(), announce: vi.fn(),
    ...o,
  };
}


const sibs = (st: PodMetricState) => (p: string) => (p === PODS[0].pod ? ok() : st);
const CASES: [string, Partial<TracePodPanelProps>][] = [
  ['ok', {}],
  ['nolimit', { metrics: (p: string) => (p === PODS[0].pod ? ok({ cpuLimit: undefined, memLimit: undefined }) : ok()) }],
  ['absent', { metrics: () => ok({ cpuLimit: undefined, memLimit: undefined, inventory: 'absent' }) }],
  ['unknown', { metrics: () => ok({ cpuLimit: undefined, memLimit: undefined, inventory: 'unknown' }) }],
  ['partialReq', { metrics: () => ok({ cpuLimit: undefined, memLimit: undefined, instantPartial: true }) }],
  ['partialNoReq', { metrics: () => ok({ cpuLimit: undefined, memLimit: undefined, cpuRequest: undefined, memRequest: undefined, instantPartial: true }) }],
  ['noreq', { metrics: () => ok({ cpuLimit: undefined, memLimit: undefined, cpuRequest: undefined, memRequest: undefined }) }],
  ['nosample', { metrics: () => ok({ cpuAt: undefined, memAt: undefined, now: { phase: '', restarts: undefined, lastTermReason: 'Error' } as unknown as PodMetricSeries['now'] }) }],
  ['alone', { selected: OTHER, metrics: () => ok({ now: { phase: 'Running', restarts: 0 } as unknown as PodMetricSeries['now'] }) }],
  ['sibErr', { metrics: sibs({ kind: 'error', message: 'x', timeout: false }) }],
  ['sibIdle', { metrics: sibs({ kind: 'idle' }) }],
  ['sibNoAt', { metrics: (p: string) => (p === PODS[0].pod ? ok({ cpuAt: undefined, memAt: undefined }) : ok()) }],
  ['memOnly', { metrics: (p: string) => (p === PODS[0].pod ? ok({ cpuAt: undefined }) : ok({ memAt: 0.5 * 1024 ** 3 })) }],
  ['loading', { metrics: () => ({ kind: 'loading' }) }],
  ['idle', { metrics: () => ({ kind: 'idle' }) }],
  ['off', { metrics: () => ({ kind: 'off' }) }],
  ['unmapped1', { metrics: () => ({ kind: 'unmapped', clusterValue: 'cv', reason: 'no_cluster_value' }) }],
  ['unmapped2', { metrics: () => ({ kind: 'unmapped', clusterValue: 'cv', reason: 'no_remote_cluster' }) }],
  ['nosamples', { metrics: () => ({ kind: 'no_samples', cluster: { id: 'c', name: 'cn' } }) }],
  ['ambiguous', { metrics: () => ({ kind: 'ambiguous', cluster: { id: 'c', name: 'cn' } }) }],
  ['errT', { metrics: () => ({ kind: 'error', message: 'boom', timeout: true }) }],
  ['errM', { metrics: () => ({ kind: 'error', message: '', timeout: false }) }],
  ['go', { selected: pod(`${SVC}-6b7d9f8c5-zzzzz`, { runtime: 'go', errors: 2, critNs: 0 }) }],
];
async function all(): Promise<string> {
  const out: string[] = [];
  for (const [, o] of CASES) {
    const host = document.createElement('div');
    document.body.appendChild(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let root: Root | null = null;
    act(() => { root = createRoot(host); root.render(<QueryClientProvider client={qc}><MemoryRouter><TracePodPanel {...props(o)} /></MemoryRouter></QueryClientProvider>); });
    await act(async () => { await new Promise(r => setTimeout(r, 20)); });
    await act(async () => { await new Promise(r => setTimeout(r, 20)); });
    const tog = [...host.querySelectorAll<HTMLButtonElement>('button')].find(b => b.hasAttribute('aria-expanded'));
    if (tog) act(() => { tog.click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 20)); });
    const attrs = [...host.querySelectorAll('[title],[aria-label]')].flatMap(e => ['title', 'aria-label'].map(a => e.getAttribute(a) ?? ''));
    out.push(host.textContent ?? '', ...attrs);
    act(() => { root!.unmount(); });
    host.remove();
  }
  return out.join('\n');
}

const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

describe('Trace pod paneli — İngilizce arayüz (v0.10.2316)', () => {
  it('Türkçe metin yok', async () => {
    m.lang = 'en';
    const text = await all();
    expect(text.split('\n').filter(l => TR_CHARS.test(l))).toEqual([]);
    for (const en of [
      'Copy pod name', 'Open on the pod page ↗', 'Show spans in Trace (61)', 'Close details', 'root error', '4 errors',
      'critical path 38% · 1.30 s', 'Memory', '0.97 cores', '1.42 GiB', 'limit 1', 'limit 2 GiB', 'no limit set', 'limit unknown',
      'At trace time: 0.97 cores · 97% of limit limit 1 core (now)', 'Hit the limit: CPU throttling is likely.',
      '4 errors ↗', 'Open the first failed span: op-a', 'largest self time:', 'Phase — now (not trace time)', 'Last termination: OOMKilled',
      'Technical details', 'In this trace', 'Largest self time', 'Critical path share', 'Active range', '4 · first: op-a',
      'At trace time · Thanos, ±1 step (15 s)', 'Vs siblings', 'CPU vs the median of the other 4 pods in this trace: ', ' · Memory ',
      'Threshold: 1.5× and above', 'Now (not trace time)', ' · as of ', 'Last termination', 'CPU limit / request', 'Memory limit / request',
      'Blue band: trace (3.42 s; drawn at least one 15 s step wide).', 'The charts show coincidence, not causation.',
      " · Pod is not in the current inventory — limit unknown", ' · limit could not be read (kube-state-metrics query failed)',
      ' · kube-state-metrics partly unreadable · 194% of request', '% of request (request 0.5 cores, now)', ' · no request set either',
      ' no sample at trace time', "The service's only pod in this trace — no sibling to compare",
      'Metrics of the other 4 pods in this trace could not be read', 'The other 4 pods in this trace have no metrics',
      'No value to compare at trace time', 'Loading', 'Metrics not loaded yet.', 'Load', 'No Thanos Remote Cluster defined.',
      'Settings › Remote Cluster ↗', "This pod's spans have no cluster attribute", "This pod's cluster (cv) is not mapped",
      'No Thanos samples in this window.', 'The pod name exists in more than one namespace', 'Could not read the data — this is an error, not an empty result.',
      '10 s timeout. Metrics of other clusters are not affected.', 'unknown error.', 'Retry',
      'Cluster not mapped: phase, restarts and limits cannot be read.', 'No JVM panel · runtime: go', 'not on the critical path',
      'No JVM runtime metrics arrive for these pods (no OTel jvm.*).',
    ]) expect(text, en).toContain(en);
  }, 30000);

  it('Türkçe arayüzde bilinen metin aynen', async () => {
    m.lang = 'tr';
    const text = await all();
    for (const tr of [
      'Pod adını kopyala', 'Pod sayfasında aç ↗', "Span'ları Trace'te göster (61)", 'Ayrıntıyı kapat', 'kök hata', '4 hata',
      'kritik yol %38 · 1,30 sn', 'Bellek', '0,97 çekirdek', '1,42 GiB', 'limit tanımsız', 'limit bilinmiyor',
      "Trace anında: 0,97 çekirdek · limitin %97'si limit 1 çekirdek (şu an)", 'Limite dayandı: CPU kısıtlaması',
      '4 hata ↗', "İlk hatalı span'i aç: op-a", 'en büyük öz süre:', 'Faz — şu an (trace anı değil)', 'Son sonlanma: OOMKilled',
      'Teknik ayrıntı', "Bu trace'te", 'En büyük öz süre', 'Kritik yol payı', 'Etkin aralık', '4 · ilk: op-a',
      'Trace anında · Thanos, ±1 adım (15 sn)', 'Kardeşlere göre', "CPU, bu trace'teki diğer 4 pod'un medyanının ", ' · Bellek ',
      'Eşik: 1,5 kat ve üstü', 'Şu an (trace anı değil)', ' itibarıyla · trace ', 'CPU limit / istek', 'Bellek limit / istek',
      'Mavi bant: trace (3,42 sn; en az bir 15 sn adım genişliğinde çizilir).', 'Grafikler eşzamanlılığı gösterir, nedenselliği değil.',
      ' · Pod şu anki envanterde yok — limit bilinmiyor', ' · istek de tanımsız', ' trace anında örnek yok',
      "Bu trace'te servisin tek pod'u — kıyaslanacak kardeş yok", 'Yükleniyor', 'Metrik henüz yüklenmedi.', 'Yükle',
      'Thanos Remote Cluster tanımlı değil.', 'Ayarlar › Remote Cluster ↗', 'Veri okunamadı — bu bir hata, boş sonuç değil.',
      "10 sn zaman aşımı. Diğer cluster'ların metrikleri etkilenmedi.", 'Yeniden dene', 'JVM paneli yok · runtime: go', 'kritik yolda değil',
      "JVM runtime metrikleri bu pod'lar için gelmiyor (OTel jvm.* yok).",
    ]) expect(text, tr).toContain(tr);
  }, 30000);
});
