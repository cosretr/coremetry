// tracePodFullName.test — v0.10.1644 (operator-reported): Trace › Metrics
// yüzeyleri pod adını `…<rs-hash>-<suffix>` / `<suffix>` diye kırpıyordu
// (shortPod / podSuffix / suffixLabels); deployment öneki yoktu. Aynı hata
// sınıfı: v0.10.1640 podLineLabel. Tam ad her yerde (seri, tooltip); dar alan
// CSS ellipsis + title ile kısalır. v0.10.2207 — çip ve zaman çizelgesi kalktı.
import { describe, it, expect } from 'vitest';
import type { SpanMetricSeries } from '@/lib/types';
import type { PodMetricState, TracePodInfo } from './traceMetricsModel';
import { jvmPodSeries } from './traceMetrics';
import { chartPod } from './tracePodPanelModel';

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

  it.each(NAMES)('jvmPodSeries seri adı (lejant/tooltip) tam: %s', (name) => {
    const s: SpanMetricSeries = { groupKey: [name], points: [{ time: 0, value: 1 }] };
    const out = jvmPodSeries([s]);
    expect(out[0].groupKey).toEqual([name]);
    expect(out[0].fullKey).toEqual([name]);
  });

  it.each(NAMES)('grafik (Bellek/CPU) seri etiketi tam: %s', (name) => {
    const p = ps.find(x => x.pod === name)!;
    expect(chartPod(p, () => okState())?.label).toBe(name);
  });
});
