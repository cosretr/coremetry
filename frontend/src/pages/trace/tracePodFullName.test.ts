// tracePodFullName.test — v0.10.1644 (operator-reported): Trace › Metrics
// yüzeyleri pod adını `…<rs-hash>-<suffix>` / `<suffix>` diye kırpıyordu
// (shortPod / podSuffix / suffixLabels); deployment öneki yoktu. Aynı hata
// sınıfı: v0.10.1640 podLineLabel. Tam ad her yerde (çip, lejant, tooltip,
// seri listesi, işaret başlığı); dar alan CSS ellipsis + title ile kısalır.
import { describe, it, expect } from 'vitest';
import type { SpanMetricSeries } from '@/lib/types';
import type { PodMetricState, TraceMetricsModel, TracePodInfo } from './traceMetricsModel';
import { jvmPodSeries } from './traceMetrics';
import { chartPods, compareChipLayout, timelineModel } from './tracePodPanelModel';

const T0 = 1_790_496_000 * 1e9;
const SVC = 'orders-api';

function pod(name: string): TracePodInfo {
  const m = /^(.+)-([a-z0-9]{6,10})-([a-z0-9]{5})$/.exec(name);
  return {
    pod: name, namespace: 'shop', clusterValue: 'dc-1', service: SVC,
    spans: 3, errors: 0, maxSelfNs: 0, maxSelfSpanId: '', critNs: 0, critShare: 0,
    topSelf: false, rootCause: false, entry: false, firstError: null,
    activeFromNs: T0, activeToNs: T0 + 1e9, flagged: false, runtime: 'java', replicaSet: m ? `${m[1]}-${m[2]}` : '',
    nameParts: m ? { head: m[1], rs: m[2], tail: m[3] } : { head: name, rs: '', tail: '' },
  };
}

function model(pods: TracePodInfo[], o: Partial<TraceMetricsModel> = {}): TraceMetricsModel {
  return {
    pods, groups: [{ service: SVC, pods, spans: 0, errors: 0, maxSelfNs: 0, critShare: 0, flagged: 0, lead: false }],
    byPod: new Map(pods.map(p => [p.pod, p])), podSpans: new Map(), criticalIds: new Set(), errorMarks: [],
    noPod: null, clusterValues: ['dc-1'], primaryCluster: 'dc-1',
    traceStartNs: T0, traceEndNs: T0 + 2e9, rootWallNs: 2e9, selfKnown: true, truncated: false,
    errorSpans: 0, rootCausePod: '', entryPod: '', counts: { all: pods.length, err: 0, crit: 0, slow: 0 },
    ...o,
  };
}

const okState = (): PodMetricState => ({
  kind: 'ok',
  data: {
    startSec: T0 / 1e9 - 300, stepSec: 15, cpu: [0.4], mem: [1e9], cpuAt: 0.4, memAt: 1e9,
    cpuLevel: 'none', memLevel: 'none', inventory: 'present', now: { phase: 'Running', restarts: 0 },
    cluster: { id: 'c-1', name: 'dc-1' }, namespace: 'shop', fetchedAtMs: T0 / 1e6,
  },
});

const NAMES = [
  'orders-api-6b49d8f679-69tf7',   // deployment pod (rs hash + suffix)
  'orders-api-7c8977f965-x2kqp',   // ikinci ReplicaSet
  'orders-api-db-0',               // StatefulSet
  'orders',                        // tire yok
];

describe('v0.10.1644 — Trace Metrics pod etiketi = tam ad', () => {
  const ps = NAMES.map(pod);
  const m = model(ps);

  it.each(NAMES)('jvmPodSeries seri adı (lejant/tooltip) tam: %s', (name) => {
    const s: SpanMetricSeries = { groupKey: [name], points: [{ time: 0, value: 1 }] };
    const out = jvmPodSeries([s]);
    expect(out[0].groupKey).toEqual([name]);
    expect(out[0].fullKey).toEqual([name]);
  });

  it.each(NAMES)('karşılaştırma çipi etiketi tam: %s', (name) => {
    const { visible } = compareChipLayout(ps, [ps[0].pod], () => okState());
    expect(visible.find(c => c.pod === name)?.label).toBe(name);
  });

  it.each(NAMES)('grafik (Bellek/CPU) seri + lejant etiketi tam: %s', (name) => {
    const cp = chartPods(m, ps[0], NAMES.slice(0, 4), () => okState());
    expect([...cp.compared, ...cp.siblings].find(c => c.pod === name)?.label).toBe(name);
  });

  it('zaman çizelgesi hata işareti başlığı tam ad', () => {
    const tm = model(ps, { errorMarks: [{ spanId: 'e1', pod: ps[0].pod, service: SVC, name: 'GET /orders', status: '500', timeNs: T0 + 1e9 }] });
    expect(timelineModel(tm, ps[0]).marks[0].title).toBe(`${SVC} orders-api-6b49d8f679-69tf7 · GET /orders · 500 · +1,000 sn`);
  });
});
