import { describe, it, expect } from 'vitest';
import type { SpanRow, TraceAnalysis, TracePodMetricsResponse } from '@/lib/types';
import { computeCriticalPath, type CriticalPath } from '@/lib/criticalPath';
import { decodeRange } from '@/lib/urlState';
import {
  buildRows, buildTraceMetricsModel, chunkEligible, chunksToEnable, coverageSummary, defaultOpenGroups, defaultTracePod,
  hottestPodState, jvmPodSeries, metricErrorReason, metricLevel, moreRowNotes, okCellText, podAtCap, podMetricState,
  podNameParts, quoteMetaLen, selectPod, shortPod, spanDepthMap, spanErrorStatus, stateCellText, togglePod,
  traceMetricsPodHref, traceMetricsWindow, traceMomentValue, tracePodChunks, tracePods,
  type ChunkPhase, type ChunkSnapshot, type TraceMetricsRow, type TraceMetricsRowView,
} from './traceMetrics';
import {
  TRACE_METRICS_MAX_COMPARE, TRACE_POD_AUTO_CHUNKS, TRACE_POD_CHUNK_MAX, TRACE_POD_CHUNK_REGEX_MAX,
  type PodMetricSeries, type PodMetricState, type TraceMetricsModel,
} from './traceMetricsModel';

// v0.10.968 — Trace › Metrics yeniden tasarımı (operatör onayı 2026-09-27):
// model (pod çıkarma, hata kuralı, öz süre, kritik yol payı, kök neden, sıra,
// bayraklar), satırlar, toplu uç dilimleri, dilim → pod durumu, seçim kuralı.
// v0.10.962'nin togglePod / podAtCap / href / jvm tabloları aynen korunur;
// sıra kuralı ve varsayılan seçim BİLEREK değişti (aşağıda "hata 5").

const MS = 1e6;
type SpanOpts = {
  err?: boolean; start?: number; dur?: number; ns?: string; cv?: string; name?: string;
  res?: Record<string, string>; attrs?: Record<string, string>; events?: SpanRow['events'];
  statusMessage?: string; httpStatus?: number; podAttr?: boolean;
};
function span(id: string, parent: string, svc: string, pod: string, o: SpanOpts = {}): SpanRow {
  const start = (o.start ?? 0) * MS;
  const dur = (o.dur ?? 10) * MS;
  const podRes: Record<string, string> = pod && !o.podAttr ? { 'k8s.pod.name': pod, 'k8s.namespace.name': o.ns ?? 'payments', 'k8s.cluster.name': o.cv ?? 'dc-east-1' } : {};
  const podAttrs: Record<string, string> = pod && o.podAttr ? { 'k8s.pod.name': pod } : {};
  return {
    traceId: 'tr-synthetic', spanId: id, parentSpanId: parent, name: o.name ?? `op-${id}`, kind: 'SERVER',
    serviceName: svc, hostName: '', startTime: start, endTime: start + dur, durationMs: dur / MS,
    statusCode: o.err ? 'error' : 'ok', statusMessage: o.statusMessage ?? '',
    attributes: { ...podAttrs, ...(o.attrs ?? {}) }, resourceAttributes: { ...podRes, ...(o.res ?? {}) },
    events: o.events ?? null, scopeName: '', httpStatus: o.httpStatus,
  };
}
function analysisOf(spans: SpanRow[], self: Record<string, number>, rootSpanId = ''): TraceAnalysis {
  const depth = spanDepthMap(spans);
  return {
    v: 1,
    nodes: spans.map((s, i) => ({
      spanId: s.spanId, parentSpanId: s.parentSpanId, depth: depth.get(s.spanId) ?? 0, order: i,
      childCount: 0, subtreeCount: 1, subtreeErrors: 0, subtreeNs: 0, selfNs: (self[s.spanId] ?? 0) * MS,
    })),
    criticalNs: 0, criticalIds: [], services: [],
    rootSpanId: rootSpanId || spans.find(s => !s.parentSpanId)?.spanId || '', orphanCount: 0, truncated: false,
  };
}
const cpOf = (spans: SpanRow[]) => computeCriticalPath(spans.map(s => ({
  spanId: s.spanId, parentId: s.parentSpanId, startTime: s.startTime, duration: s.endTime - s.startTime,
})));
function handCp(rootWallMs: number, onPath: Record<string, number>): CriticalPath {
  const ids = Object.keys(onPath);
  return {
    ids: new Set(ids), order: ids, spanCount: ids.length, rootWallNs: rootWallMs * MS,
    onPathSelfNs: new Map(ids.map(k => [k, onPath[k] * MS])), rootId: ids[0] ?? null, leafId: ids[ids.length - 1] ?? null,
  };
}

// Ana örnek trace: kök checkout-api; fraud-score-prod'da iki hatalı pod (en
// derin hata b, pod F2); audit-log-prod'da exception OLAYI (durum ok); pod'suz
// iki checkout-api span'i kritik yolun yarısını taşıyor.
const P_CK = 'checkout-api-7c8977f965-7hrqz';
const P_F1 = 'fraud-score-prod-6b7d9f8c5-m3t9w';
const P_F2 = 'fraud-score-prod-6b7d9f8c5-h8k2v';
const P_AU = 'audit-log-prod-5f9c7d6b8-q2w4e';
const EXC = [{ name: 'exception', timeNano: 1, attributes: { 'exception.type': 'IllegalStateException' } }];
const LONG = 'x'.repeat(80);
const SPANS: SpanRow[] = [
  span('r', '', 'checkout-api', P_CK, { start: 0, dur: 1000, res: { 'telemetry.sdk.language': 'java' } }),
  span('a', 'r', 'fraud-score-prod', P_F1, { start: 100, dur: 300, err: true, httpStatus: 503 }),
  span('b', 'a', 'fraud-score-prod', P_F2, { start: 150, dur: 200, err: true, attrs: { 'rpc.grpc.status_code': '14' } }),
  span('c', 'r', 'audit-log-prod', P_AU, { start: 420, dur: 20, events: EXC }),
  span('d', 'r', 'checkout-api', '', { start: 450, dur: 500 }),
  span('e', 'd', 'checkout-api', '', { start: 500, dur: 400, err: true, statusMessage: LONG }),
  span('f', 'r', 'checkout-api', P_CK, { start: 960, dur: 30 }),
];
const SELF = { r: 150, a: 100, b: 200, c: 20, d: 100, e: 400, f: 30 };
const MODEL = buildTraceMetricsModel(SPANS, analysisOf(SPANS, SELF), cpOf(SPANS), false);

describe('buildTraceMetricsModel — çıkarma, hata, pod\'suz, kritik yol', () => {
  it('1. pod adı resource önce, yoksa span özniteliği', () => {
    const sp = [
      span('x', '', 'checkout-api', 'checkout-api-7c8977f965-aaaaa', { podAttr: true }),
      { ...span('y', '', 'checkout-api', 'checkout-api-7c8977f965-bbbbb'), attributes: { 'k8s.pod.name': 'checkout-api-7c8977f965-ccccc' } },
    ];
    const m = buildTraceMetricsModel(sp, undefined, null, false);
    expect([...m.byPod.keys()].sort()).toEqual(['checkout-api-7c8977f965-aaaaa', 'checkout-api-7c8977f965-bbbbb']);
    expect(tracePods(sp).map(p => p.pod).sort()).toEqual(['checkout-api-7c8977f965-aaaaa', 'checkout-api-7c8977f965-bbbbb']);
  });

  it('2. hata = spanHasError: exception olayı durum ok olsa da sayılır', () => {
    expect(MODEL.byPod.get(P_AU)!.errors).toBe(1);
    expect(MODEL.byPod.get(P_CK)!.errors).toBe(0);
    expect(MODEL.errorSpans).toBe(4);
    expect(MODEL.errorMarks.map(e => e.spanId)).toEqual(['a', 'b', 'c', 'e']);
  });

  it('3. pod\'suz span\'lar düşmez: servis başına span / hata / öz süre / kritik yol', () => {
    expect(MODEL.noPod).toEqual({
      spans: 2, errors: 1, maxSelfNs: 400 * MS, critShare: 0.5,
      services: [{ service: 'checkout-api', spans: 2, errors: 1, maxSelfNs: 400 * MS, critShare: 0.5 }],
    });
  });

  it('4. kritik yol payı: pod\'lar + pod\'suz satır ≈ 1 (zincir kapsanınca)', () => {
    const pods = MODEL.pods.reduce((a, p) => a + p.critShare, 0);
    expect(pods + MODEL.noPod!.critShare).toBeCloseTo(1, 9);
    expect(MODEL.byPod.get(P_CK)!.critShare).toBeCloseTo(0.5, 9);
    expect(MODEL.rootWallNs).toBe(1000 * MS);
  });

  it('5. analysis yok → öz süre bilinmez: maxSelf 0, topSelf yok, Yavaş 0', () => {
    const m = buildTraceMetricsModel(SPANS, undefined, cpOf(SPANS), false);
    expect(m.selfKnown).toBe(false);
    expect(m.pods.every(p => p.maxSelfNs === 0 && !p.topSelf)).toBe(true);
    expect(m.counts.slow).toBe(0);
    // Bayrak yine hata / kritik yoldan gelir.
    expect(m.pods.every(p => p.flagged)).toBe(true);
  });

  it('6a. kök neden = en derin hatalı span\'ın pod\'u; analysis ve yedek derinlik AYNI; giriş pod\'u', () => {
    expect(MODEL.rootCausePod).toBe(P_F2);
    const fallback = buildTraceMetricsModel(SPANS, undefined, cpOf(SPANS), false);
    expect(fallback.rootCausePod).toBe(P_F2);
    expect(MODEL.entryPod).toBe(P_CK);
    expect(MODEL.byPod.get(P_F2)!.rootCause).toBe(true);
    expect(MODEL.byPod.get(P_CK)!.entry).toBe(true);
  });

  it('6b. derinlik eşitliğinde erken başlayan; pod\'suz hatalı span aday değil', () => {
    const sp = [
      span('r', '', 'checkout-api', P_CK, { dur: 100 }),
      span('x', 'r', 'fraud-score-prod', P_F1, { start: 30, err: true }),
      span('y', 'r', 'fraud-score-prod', P_F2, { start: 20, err: true }),
      span('z', 'y', 'checkout-api', '', { start: 21, err: true }),
    ];
    expect(buildTraceMetricsModel(sp, undefined, null, false).rootCausePod).toBe(P_F2);
  });

  it('6c. en büyük 5 öz süre (pod\'lu span\'lar arasında) → topSelf; bayraklar', () => {
    const pods = Array.from({ length: 7 }, (_, i) => `checkout-api-7c8977f965-p${i}aaa`);
    const sp = [span('root', '', 'checkout-api', '', { dur: 1000 }),
      ...pods.map((p, i) => span(`s${i}`, 'root', 'checkout-api', p, { start: i * 10 }))];
    const self: Record<string, number> = { root: 999 };
    pods.forEach((_, i) => { self[`s${i}`] = (i + 1) * 10; });
    const m = buildTraceMetricsModel(sp, analysisOf(sp, self), null, false);
    const top = m.pods.filter(p => p.topSelf).map(p => p.pod).sort();
    expect(top).toEqual(pods.slice(2).sort());
    expect(m.counts.slow).toBe(5);
    expect(m.byPod.get(pods[0])!.flagged).toBe(false);
    expect(m.byPod.get(pods[6])!.maxSelfNs).toBe(70 * MS);
  });

  it('6d. ayrıntılar: ilk hata, durum metni, etkin aralık, runtime, ad parçaları', () => {
    const f1 = MODEL.byPod.get(P_F1)!;
    expect(f1.firstError).toEqual({ spanId: 'a', name: 'op-a', status: '503', timeNs: 100 * MS });
    expect(MODEL.byPod.get(P_F2)!.firstError!.status).toBe('UNAVAILABLE');
    expect(MODEL.byPod.get(P_AU)!.firstError!.status).toBe('IllegalStateException');
    const ck = MODEL.byPod.get(P_CK)!;
    expect([ck.activeFromNs, ck.activeToNs]).toEqual([0, 1000 * MS]);
    expect(ck.runtime).toBe('java');
    expect(ck.nameParts).toEqual({ head: 'checkout-api', rs: '7c8977f965', tail: '7hrqz' });
    expect(ck.replicaSet).toBe('checkout-api-7c8977f965');
    expect(MODEL.podSpans.get(P_CK)!.map(s => s.spanId)).toEqual(['r', 'f']);
    expect(MODEL.criticalIds.has('d')).toBe(true);
  });
});

describe('spanErrorStatus — D7 (ilk eşleşen kazanır)', () => {
  it.each([
    ['httpStatus', span('h', '', 'checkout-api', '', { err: true, httpStatus: 502 }), '502'],
    ['http.response.status_code', span('h', '', 'checkout-api', '', { err: true, attrs: { 'http.response.status_code': '404' } }), '404'],
    ['http.status_code', span('h', '', 'checkout-api', '', { err: true, attrs: { 'http.status_code': '500', 'rpc.grpc.status_code': '2' } }), '500'],
    ['gRPC adı', span('g', '', 'checkout-api', '', { err: true, attrs: { 'rpc.grpc.status_code': '4' } }), 'DEADLINE_EXCEEDED'],
    ['exception tipi', span('x', '', 'checkout-api', '', { events: EXC }), 'IllegalStateException'],
    ['statusMessage ≤60', span('m', '', 'checkout-api', '', { err: true, statusMessage: LONG }), 'x'.repeat(59) + '…'],
    ['yedek', span('n', '', 'checkout-api', '', { err: true }), 'hata'],
  ])('%s', (_n, sp, want) => {
    expect(spanErrorStatus(sp)).toBe(want);
  });
  it('HTTP 2xx ve gRPC 0 atlanır', () => {
    expect(spanErrorStatus(span('o', '', 'checkout-api', '', { err: true, httpStatus: 200, attrs: { 'rpc.grpc.status_code': '0' } }))).toBe('hata');
  });
});

describe('podNameParts — D13', () => {
  it('replicaset adı varsa ondan; yoksa kalıp; o da yoksa bölünmez', () => {
    expect(podNameParts('fraud-score-prod-6b7d9f8c5-m3t9w', 'fraud-score-prod-6b7d9f8c5'))
      .toEqual({ head: 'fraud-score-prod', rs: '6b7d9f8c5', tail: 'm3t9w' });
    expect(podNameParts('fraud-score-prod-6b7d9f8c5-m3t9w', '')).toEqual({ head: 'fraud-score-prod', rs: '6b7d9f8c5', tail: 'm3t9w' });
    expect(podNameParts('audit-log-prod-0', '')).toEqual({ head: 'audit-log-prod-0', rs: '', tail: '' });
  });
  it('spanDepthMap döngüde sonsuza gitmez', () => {
    const d = spanDepthMap([span('p', 'q', 'checkout-api', ''), span('q', 'p', 'checkout-api', '')]);
    expect(d.size).toBe(2);
  });
});

describe('sıra — v0.10.968 kuralı', () => {
  it('7. servis: lead → hatalı → kritik yol ↓ → öz süre ↓ → span ↓ → ad', () => {
    expect(MODEL.groups.map(g => g.service)).toEqual(['fraud-score-prod', 'audit-log-prod', 'checkout-api']);
    expect(MODEL.groups[0].lead).toBe(true);
    const sp = [
      span('r', '', 'svc-root', '', { dur: 1000 }),
      span('c1', 'r', 'svc-crit', 'svc-crit-7c8977f965-aaaaa'),
      span('s1', 'r', 'svc-self', 'svc-self-7c8977f965-aaaaa'),
      span('n1', 'r', 'svc-many', 'svc-many-7c8977f965-aaaaa'), span('n2', 'r', 'svc-many', 'svc-many-7c8977f965-aaaaa'),
      span('b1', 'r', 'svc-b', 'svc-b-7c8977f965-aaaaa'), span('a1', 'r', 'svc-a', 'svc-a-7c8977f965-aaaaa'),
    ];
    const m = buildTraceMetricsModel(sp, analysisOf(sp, { s1: 50, c1: 10 }), handCp(1000, { r: 900, c1: 100 }), false);
    expect(m.groups.map(g => g.service)).toEqual(['svc-crit', 'svc-self', 'svc-many', 'svc-a', 'svc-b']);
  });

  it('8. pod: kök neden → hatalı → kritik yol ↓ → öz süre ↓ → span ↓ → ad', () => {
    expect(MODEL.pods.map(p => p.pod)).toEqual([P_F2, P_F1, P_AU, P_CK]);
    expect(MODEL.groups[0].pods.map(p => p.pod)).toEqual([P_F2, P_F1]);
  });

  // v0.10.962 hata 5'in örneği — kural BİLEREK değişti: hatasız servisler
  // artık span sayısından önce KRİTİK YOL payına göre (kök alpha öne geçer).
  it('9. v0.10.962 "hata 5" örneği yeni kuralla', () => {
    const sp = [
      span('r', '', 'alpha', 'alpha-aaaaaa-r0000', { dur: 1000 }),
      span('z1', 'r', 'zeta', 'zeta-aaaaaa-z0001', { err: true }),
      span('z2', 'r', 'zeta', 'zeta-aaaaaa-z0002', { err: true }),
      span('m', 'z1', 'mid', 'mid-aaaaaa-m0000', { err: true }),
      span('b1', 'r', 'beta', 'beta-aaaaaa-b0001'), span('b2', 'r', 'beta', 'beta-aaaaaa-b0001'), span('b3', 'r', 'beta', 'beta-aaaaaa-b0002'),
      span('g1', 'r', 'gamma', 'gamma-aaaaaa-g0001'), span('g2', 'r', 'gamma', 'gamma-aaaaaa-g0001'), span('g3', 'r', 'gamma', 'gamma-aaaaaa-g0001'),
    ];
    // Kritik yol bilgisi yokken eski sıra aynen çıkar…
    expect(buildTraceMetricsModel(sp, undefined, null, false).groups.map(g => g.service))
      .toEqual(['mid', 'zeta', 'beta', 'gamma', 'alpha']);
    // …kökün kritik yol payı alpha'yı span sayısı çok olan hatasız servislerin önüne alır.
    expect(buildTraceMetricsModel(sp, undefined, handCp(1000, { r: 1000 }), false).groups.map(g => g.service))
      .toEqual(['mid', 'zeta', 'alpha', 'beta', 'gamma']);
  });

  it('10. sıra girdi permütasyonundan bağımsız', () => {
    const want = { g: MODEL.groups.map(g => g.service), p: MODEL.pods.map(p => p.pod), rc: MODEL.rootCausePod };
    for (const perm of [[...SPANS].reverse(), [...SPANS.slice(3), ...SPANS.slice(0, 3)]]) {
      const m = buildTraceMetricsModel(perm, analysisOf(SPANS, SELF), cpOf(SPANS), false);
      expect({ g: m.groups.map(g => g.service), p: m.pods.map(p => p.pod), rc: m.rootCausePod }).toEqual(want);
    }
  });

  it('11. varsayılan seçim: kök neden pod\'u; yoksa giriş; yoksa ilk', () => {
    expect(defaultTracePod(MODEL)).toBe(P_F2);
    const ok = SPANS.map(s => ({ ...s, statusCode: 'ok', events: null }));
    expect(defaultTracePod(buildTraceMetricsModel(ok, undefined, null, false))).toBe(P_CK);
    // Kök span'in pod'u yok → giriş pod'u yok → ilgi sırasındaki ilk pod.
    const noRootPod = ok.map(s => (s.spanId === 'r' ? { ...s, resourceAttributes: {} } : s));
    const m = buildTraceMetricsModel(noRootPod, undefined, null, false);
    expect(m.entryPod).toBe('');
    expect(defaultTracePod(m)).toBe(m.pods[0].pod);
  });
});

describe('togglePod / podAtCap — v0.10.962 tabloları aynen', () => {
  const many = [
    ...Array.from({ length: 6 }, (_, i) => span(`x${i}`, '', 'fraud-score-prod', `fraud-score-prod-aaaaaa-p${i}xxx`)),
    span('o', '', 'audit-log-prod', 'audit-log-prod-aaaaaa-o0000'),
  ];
  const mp = tracePods(many);
  const sel = mp.slice(0, TRACE_METRICS_MAX_COMPARE).map(p => p.pod);
  it('ekle / çıkar / başka servis değiştirir / tavan', () => {
    let s = [mp[0].pod];
    s = togglePod(s, mp[1].pod, mp);
    expect(s).toHaveLength(2);
    expect(togglePod(s, 'audit-log-prod-aaaaaa-o0000', mp)).toEqual(['audit-log-prod-aaaaaa-o0000']);
    expect(togglePod(s, mp[1].pod, mp)).toEqual([mp[0].pod]);
    expect(togglePod([mp[0].pod], mp[0].pod, mp)).toEqual([mp[0].pod]);
    let all: string[] = [mp[0].pod];
    for (const p of mp.slice(1, 6)) all = togglePod(all, p.pod, mp);
    expect(all).toHaveLength(TRACE_METRICS_MAX_COMPARE);
  });
  it('podAtCap yalnız aynı servisten SEÇİLİ OLMAYAN pod için, tavanda true', () => {
    expect(podAtCap(sel, mp[4].pod, mp)).toBe(true);
    expect(podAtCap(sel, mp[0].pod, mp)).toBe(false);
    expect(podAtCap(sel, 'audit-log-prod-aaaaaa-o0000', mp)).toBe(false);
    expect(podAtCap(sel.slice(0, 3), mp[4].pod, mp)).toBe(false);
  });
  it('togglePod SAF kalır; seçili olmayan pod için no-op ⇔ podAtCap', () => {
    const before = [...sel];
    for (const p of mp) {
      if (sel.includes(p.pod)) continue;
      expect(togglePod(sel, p.pod, mp) === sel).toBe(podAtCap(sel, p.pod, mp));
    }
    expect(sel).toEqual(before);
  });
});

describe('13. selectPod', () => {
  const pods = Array.from({ length: 6 }, (_, i) => `fraud-score-prod-6b7d9f8c5-p${i}aaa`);
  const sp = [span('r', '', 'checkout-api', P_CK, { dur: 100 }), ...pods.map((p, i) => span(`f${i}`, 'r', 'fraud-score-prod', p))];
  const m = buildTraceMetricsModel(sp, undefined, null, false);
  const all = () => true;
  it('aynı servis + ikisi de ok → yeni pod başa, diğerleri kalır, ≤4 (sondaki düşer)', () => {
    expect(selectPod([pods[0], pods[1]], pods[2], m, all)).toEqual({ next: [pods[2], pods[0], pods[1]], serviceChanged: false });
    expect(selectPod(pods.slice(0, 4), pods[4], m, all).next).toEqual([pods[4], pods[0], pods[1], pods[2]]);
    expect(selectPod([pods[0], pods[1]], pods[1], m, all).next).toEqual([pods[1], pods[0]]);
  });
  it('başka servis → [pod] + serviceChanged', () => {
    expect(selectPod([pods[0], pods[1]], P_CK, m, all)).toEqual({ next: [P_CK], serviceChanged: true });
    expect(selectPod([], P_CK, m, all)).toEqual({ next: [P_CK], serviceChanged: false });
  });
  it('metrik yok (ok değil) → sıfırla', () => {
    expect(selectPod([pods[0], pods[1]], pods[2], m, p => p !== pods[2])).toEqual({ next: [pods[2]], serviceChanged: false });
    expect(selectPod([pods[0]], pods[2], m, p => p !== pods[0])).toEqual({ next: [pods[2]], serviceChanged: false });
  });
});

// Satır testleri için 14 pod'luk trace: fraud-score-prod 8 pod (1 hatalı),
// audit-log-prod 4 pod (işaretsiz), checkout-api 2 pod (kök, kritik yol).
const FR = Array.from({ length: 8 }, (_, i) => `fraud-score-prod-6b7d9f8c5-f${i}aaa`);
const AU = Array.from({ length: 4 }, (_, i) => `audit-log-prod-5f9c7d6b8-a${i}aaa`);
const CK = ['checkout-api-7c8977f965-c0aaa', 'checkout-api-7c8977f965-c1aaa'];
const BIG_SPANS: SpanRow[] = [
  span('r', '', 'checkout-api', CK[0], { dur: 1000 }),
  span('r2', 'r', 'checkout-api', CK[1], { start: 900 }),
  ...FR.map((p, i) => span(`f${i}`, 'r', 'fraud-score-prod', p, { start: 10 + i, err: i === 0 })),
  ...AU.map((p, i) => span(`a${i}`, 'r', 'audit-log-prod', p, { start: 100 + i, ns: i === 3 ? 'audit' : 'payments' })),
  span('np', 'r', 'checkout-api', '', { start: 500 }),
];
const BIG = buildTraceMetricsModel(BIG_SPANS, undefined, handCp(1000, { r: 1000 }), false);
const view = (o: Partial<TraceMetricsRowView> = {}): TraceMetricsRowView => ({
  grouping: 'service', filter: 'all', query: '', openGroups: defaultOpenGroups(BIG), expanded: new Set(),
  noPodOpen: false, selected: '', ...o,
});
const keysOf = (rows: TraceMetricsRow[]) => rows.map(r => r.key);

describe('14. buildRows', () => {
  it('varsayılan açık: ≤12 pod hepsi; aksi hâlde işaretli pod taşıyan ilk 6 grup (+ seçilinin grubu)', () => {
    expect([...defaultOpenGroups(MODEL)].sort()).toEqual(['audit-log-prod', 'checkout-api', 'fraud-score-prod']);
    expect([...defaultOpenGroups(BIG)].sort()).toEqual(['checkout-api', 'fraud-score-prod']);
    expect([...defaultOpenGroups(BIG, AU[1])].sort()).toEqual(['audit-log-prod', 'checkout-api', 'fraud-score-prod']);
  });

  it('daraltma: >3 pod, ≥1 işaretli, ≥2 işaretsiz → işaretliler + seçili görünür, kalanı "N pod daha"', () => {
    const rows = buildRows(BIG, view({ selected: FR[5] }));
    expect(keysOf(rows)).toEqual([
      'g:fraud-score-prod', `p:${FR[0]}`, `p:${FR[5]}`, 'm:fraud-score-prod',
      'g:checkout-api', `p:${CK[0]}`, `p:${CK[1]}`,
      'g:audit-log-prod',
      'n:',
    ]);
    const more = rows.find(r => r.kind === 'more');
    expect(more?.kind === 'more' && more.hidden.length).toBe(6);
    // aria: grup düzey 1, pod düzey 2; setsize/posinset grup içinde.
    const g = rows[0];
    expect([g.level, g.setsize, g.posinset]).toEqual([1, 4, 1]);
    expect([rows[1].level, rows[1].setsize, rows[1].posinset]).toEqual([2, 3, 1]);
    // "N pod daha" açılınca hepsi görünür.
    const exp = buildRows(BIG, view({ expanded: new Set(['fraud-score-prod']) }));
    expect(exp.filter(r => r.kind === 'pod' && r.pod.service === 'fraud-score-prod')).toHaveLength(8);
    // İşaretli pod yoksa daraltma yok (audit-log-prod 4 pod, işaretsiz).
    const au = buildRows(BIG, view({ openGroups: new Set(['audit-log-prod']) }));
    expect(au.filter(r => r.kind === 'pod')).toHaveLength(4);
    expect(au.some(r => r.kind === 'more')).toBe(false);
  });

  it('"N pod daha" notları gizlenen pod\'lar üzerinden', () => {
    const series = (cpuLevel: 'none' | 'err', restarts: number): PodMetricState => ({ kind: 'ok', data: {
      ...OK_SERIES, cpuLevel, now: { restarts },
    } });
    const st: Record<string, PodMetricState> = {
      [FR[1]]: series('err', 0), [FR[2]]: series('none', 3), [FR[3]]: { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' },
      [FR[4]]: { kind: 'error', message: 'x', timeout: false },
    };
    const notes = moreRowNotes(FR.slice(1).map(p => BIG.byPod.get(p)!), p => st[p] ?? { kind: 'loading' });
    expect(notes).toEqual([
      { text: 'CPU ≥%90: 1 pod', tone: 'err' },
      { text: '3 restart (şu an)', tone: 'warn' },
      { text: 'dc-east-2 eşlenmemiş: 1 pod', tone: 'faint' },
      { text: 'okunamadı: 1 pod', tone: 'err' },
    ]);
  });

  it('süzgeç/arama: eşleşen gruplar zorla açık, daraltma yok, k/n, pod\'suz satır gizli', () => {
    const err = buildRows(BIG, view({ filter: 'err', openGroups: new Set() }));
    expect(keysOf(err)).toEqual(['g:fraud-score-prod', `p:${FR[0]}`]);
    expect(err[0].kind === 'group' && [err[0].pods.length, err[0].group.pods.length, err[0].filtered]).toEqual([1, 8, true]);
    const q = buildRows(BIG, view({ query: 'AUDIT', openGroups: new Set() }));
    expect(q.filter(r => r.kind === 'pod')).toHaveLength(4);
    expect(q.some(r => r.kind === 'nopod' || r.kind === 'more')).toBe(false);
    // Namespace'te de arar.
    expect(buildRows(BIG, view({ query: 'audit', filter: 'all' })).length).toBeGreaterThan(0);
    const ns = buildRows(BIG, view({ query: 'payments' }));
    expect(ns.filter(r => r.kind === 'pod').length).toBe(BIG.pods.length - 1);
    expect(buildRows(BIG, view({ query: 'yok-böyle-bir-şey' }))).toEqual([]);
  });

  it('Düz kip: pod sırası, önekli, düzey 1; pod\'suz satır süzgeçsizken durur', () => {
    const flat = buildRows(BIG, view({ grouping: 'flat' }));
    const pods = flat.filter(r => r.kind === 'pod');
    expect(pods.map(r => r.kind === 'pod' && r.pod.pod)).toEqual(BIG.pods.map(p => p.pod));
    expect(pods.every(r => r.kind === 'pod' && r.prefix && r.level === 1)).toBe(true);
    expect(flat[flat.length - 1].kind).toBe('nopod');
  });

  it('Tümünü aç: her grup açık + her "daha" açık → daraltma yok; pod\'suz satır açılır', () => {
    const all = buildRows(BIG, view({ openGroups: new Set(BIG.groups.map(g => g.service)), expanded: new Set(BIG.groups.map(g => g.service)), noPodOpen: true }));
    expect(all.filter(r => r.kind === 'pod')).toHaveLength(BIG.pods.length);
    expect(keysOf(all).slice(-2)).toEqual(['n:', 'n:checkout-api']);
  });
});

describe('15. tracePodChunks', () => {
  it('cluster değeri başına, tablo sırasında, ≤64 pod', () => {
    const e1 = Array.from({ length: 70 }, (_, i) => `fraud-score-prod-6b7d9f8c5-${String(i).padStart(5, 'a')}`);
    const e2 = Array.from({ length: 3 }, (_, i) => `audit-log-prod-5f9c7d6b8-${String(i).padStart(5, 'b')}`);
    const sp = [span('r', '', 'checkout-api', '', { dur: 100 }),
      ...e1.map((p, i) => span(`x${i}`, 'r', 'fraud-score-prod', p, { cv: 'dc-east-1' })),
      ...e2.map((p, i) => span(`y${i}`, 'r', 'audit-log-prod', p, { cv: 'dc-east-2', ns: '' }))];
    const m = buildTraceMetricsModel(sp, undefined, null, false);
    const ch = tracePodChunks(m);
    expect(ch.map(c => [c.clusterValue, c.index, c.pods.length])).toEqual([
      ['dc-east-1', 0, TRACE_POD_CHUNK_MAX], ['dc-east-1', 1, 6], ['dc-east-2', 0, 3],
    ]);
    const tableOrder = m.groups.flatMap(g => g.pods).filter(p => p.clusterValue === 'dc-east-1').map(p => p.pod);
    expect(ch.slice(0, 2).flatMap(c => c.pods.map(p => p.pod))).toEqual(tableOrder);
    expect(ch[2].podsParam.startsWith('/audit-log-prod')).toBe(true);
    expect(ch[0].key).toBe('dc-east-1#0');
  });
  it('≤4000 seçici karakteri (QuoteMeta: "." 2 sayılır)', () => {
    expect(quoteMetaLen('a.b+c')).toBe(7);
    expect(quoteMetaLen('\\.+*?()|[]{}^$')).toBe(28);
    const dotted = Array.from({ length: 40 }, (_, i) => `${'a.'.repeat(60)}${String(i).padStart(3, '0')}`); // 123 kr, QuoteMeta 183
    const sp = [span('r', '', 'checkout-api', '', { dur: 100 }), ...dotted.map((p, i) => span(`d${i}`, 'r', 'fraud-score-prod', p))];
    const ch = tracePodChunks(buildTraceMetricsModel(sp, undefined, null, false));
    for (const c of ch) {
      expect(c.pods.reduce((a, p) => a + quoteMetaLen(p.pod) + 1, 0)).toBeLessThanOrEqual(TRACE_POD_CHUNK_REGEX_MAX);
    }
    expect(ch.map(c => c.pods.length)).toEqual([21, 19]);
  });
});

describe('16. chunksToEnable', () => {
  const ch = (cv: string, n: number) => Array.from({ length: n }, (_, i) => ({ key: `${cv}#${i}`, index: i }));
  it('cluster değeri başına ilk 4 dilim kendiliğinden; en çok 4 uçuşta', () => {
    const c = [...ch('dc-east-1', 6), ...ch('dc-east-2', 2)];
    expect(chunksToEnable(c, c.map((): ChunkPhase => 'idle'), { loadRest: false }))
      .toEqual([true, true, true, true, false, false, false, false]);
    // Biri bitti → sıradaki uygun dilim (dc-east-2#0) açılır; 5. dilim otomatik sınırda.
    const ph = c.map((_, i): ChunkPhase => (i === 0 ? 'settled' : i < 4 ? 'pending' : 'idle'));
    expect(chunksToEnable(c, ph, { loadRest: false })).toEqual([true, true, true, true, false, false, true, false]);
    expect(TRACE_POD_AUTO_CHUNKS).toBe(4);
  });
  it('chunkEligible: otomatik sınır içi ya da açıkça istenmiş', () => {
    expect(chunkEligible({ key: 'dc-east-1#3', index: 3 }, { loadRest: false })).toBe(true);
    expect(chunkEligible({ key: 'dc-east-1#4', index: 4 }, { loadRest: false })).toBe(false);
    expect(chunkEligible({ key: 'dc-east-1#4', index: 4 }, { loadRest: true })).toBe(true);
    expect(chunkEligible({ key: 'dc-east-1#4', index: 4 }, { loadRest: false, forced: new Set(['dc-east-1#4']) })).toBe(true);
  });
  it('"Kalan … yükle" ve tek dilim zorlaması otomatik sınırı aşar (uçuş sınırı sürer)', () => {
    const c = ch('dc-east-1', 6);
    const settled = c.map((_, i): ChunkPhase => (i < 4 ? 'settled' : 'idle'));
    expect(chunksToEnable(c, settled, { loadRest: true })).toEqual([true, true, true, true, true, true]);
    expect(chunksToEnable(c, settled, { loadRest: false, forced: new Set(['dc-east-1#5']) }))
      .toEqual([true, true, true, true, false, true]);
    const busy = c.map((_, i): ChunkPhase => (i < 4 ? 'pending' : 'idle'));
    expect(chunksToEnable(c, busy, { loadRest: true })).toEqual([true, true, true, true, false, false]);
  });
});

describe('17–18. traceMomentValue / metricLevel', () => {
  it('±1 adım, en büyük, null atlanır', () => {
    // kovalar: 0,15,30,45,60,75 sn; trace 40–41 sn → trace'i kesen 30 kovası
    // ±1 adım: 15, 30, 45 (60 ve 0 dışarıda).
    const v = [9, 1, null, 2, 3, 8];
    expect(traceMomentValue(v, 0, 15, 40e9, 41e9)).toBe(2);
    expect(traceMomentValue([1, 7, null, 2, 30, 8], 0, 15, 40e9, 41e9)).toBe(7);
    expect(traceMomentValue([null, null], 0, 15, 0, 1e9)).toBeNull();
    expect(traceMomentValue(v, 0, 0, 40e9, 41e9)).toBeNull();
  });
  it('bir adımdan kısa trace (kova ortasında) yine komşu kovaları görür', () => {
    expect(traceMomentValue([5, 1, 7, 2], 100, 15, 131.2e9, 131.5e9)).toBe(7);
  });
  it('metricLevel sınırları 0,7 / 0,9; limit yok → none', () => {
    expect(metricLevel(0.69, 1)).toBe('none');
    expect(metricLevel(0.7, 1)).toBe('warn');
    expect(metricLevel(0.89, 1)).toBe('warn');
    expect(metricLevel(0.9, 1)).toBe('err');
    expect(metricLevel(5, undefined)).toBe('none');
    expect(metricLevel(5, 0)).toBe('none');
    expect(metricLevel(null, 1)).toBe('none');
  });
});

const OK_SERIES: PodMetricSeries = {
  startSec: 0, stepSec: 15, cpu: [0.5], mem: [1e9], cpuAt: 0.5, memAt: 1e9, cpuLevel: 'none', memLevel: 'none',
  inventory: 'present', now: {}, cluster: { id: 'c-1', name: 'dc-east-1' }, namespace: 'payments', fetchedAtMs: 1,
};

describe('19. podMetricState — her cevap / durum birleşimi', () => {
  const pod = { pod: P_F1, namespace: 'payments' };
  const trace = { startNs: 40e9, endNs: 41e9 };
  const snap = (o: Partial<ChunkSnapshot>): ChunkSnapshot => ({
    eligible: true, status: 'pending', fetching: false, error: null, data: undefined, dataUpdatedAt: 0, ...o,
  });
  const body = (o: Partial<TracePodMetricsResponse>): TracePodMetricsResponse => ({
    thanos: true, clusterValue: 'dc-east-1', mapped: true, cluster: { id: 'c-1', name: 'dc-east-1' },
    start: 0, step: 15, points: 6, instant: 'ok', pods: [], ...o,
  });
  const ok = (o: Partial<ChunkSnapshot>) => snap({ status: 'success', dataUpdatedAt: 42, ...o });

  it('yüklenmeyecek → idle; bekleyen (uçuş sınırında da) → loading; tekrar denenen hata → loading', () => {
    expect(podMetricState(pod, snap({ eligible: false }), trace)).toEqual({ kind: 'idle' });
    expect(podMetricState(pod, snap({ fetching: true }), trace)).toEqual({ kind: 'loading' });
    expect(podMetricState(pod, snap({}), trace)).toEqual({ kind: 'loading' });
    expect(podMetricState(pod, undefined, trace)).toEqual({ kind: 'loading' });
    expect(podMetricState(pod, snap({ status: 'error', fetching: true, error: new Error('x') }), trace)).toEqual({ kind: 'loading' });
  });
  it('hata: iç mesaj açılır; zaman aşımı düzenli ifadeyle', () => {
    // v0.10.968 — sunucunun GERÇEK biçimi (errUpstream: "upstream metrics backend: …").
    const t = podMetricState(pod, snap({ status: 'error', error: new Error('HTTP 502: {"error":"upstream metrics backend: dc-east-1: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)"}\n') }), trace);
    expect(t).toEqual({ kind: 'error', message: 'dc-east-1: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)', timeout: true });
    expect(t.kind === 'error' && metricErrorReason(t)).toBe('10 sn zaman aşımı');
    const e = podMetricState(pod, snap({ status: 'error', error: new Error('HTTP 502: {"error":"upstream metrics backend: dc-east-1: CPU sorgusu: HTTP 500 internal: boom"}') }), trace);
    expect(e).toEqual({ kind: 'error', message: 'dc-east-1: CPU sorgusu: HTTP 500 internal: boom', timeout: false });
    expect(metricErrorReason({ message: 'y'.repeat(100), timeout: false })).toHaveLength(80);
  });
  it('v0.10.968 — errUpstream öneki 80 karakterlik kırpmadan ÖNCE atılır: asıl sebep ("boom") görünür', () => {
    const cl = 'dc-east-1-bankasi-uzun-cluster-adi';
    const e = podMetricState(pod, snap({ status: 'error', error: new Error(`HTTP 502: {"error":"upstream metrics backend: ${cl}: CPU sorgusu: HTTP 500 internal: boom"}`) }), trace);
    expect(e.kind).toBe('error');
    if (e.kind !== 'error') return;
    expect(e.message.startsWith('upstream metrics backend')).toBe(false);
    expect(metricErrorReason(e)).toContain('boom');
    expect(stateCellText(e).tip).toContain('boom');
  });
  it('thanos:false → off; eşlenmemiş (neden taşınır)', () => {
    expect(podMetricState(pod, ok({ data: body({ thanos: false, mapped: false }) }), trace)).toEqual({ kind: 'off' });
    expect(podMetricState(pod, ok({ data: body({ mapped: false, clusterValue: 'dc-east-2', unmappedReason: 'no_remote_cluster' }) }), trace))
      .toEqual({ kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' });
    expect(podMetricState(pod, ok({ data: body({ mapped: false, clusterValue: '', unmappedReason: 'no_cluster_value' }) }), trace))
      .toEqual({ kind: 'unmapped', clusterValue: '', reason: 'no_cluster_value' });
  });
  it('satır durumu: no_samples / ambiguous / ok (trace anı + seviye + şu an)', () => {
    const c = { id: 'c-1', name: 'dc-east-1' };
    expect(podMetricState(pod, ok({ data: body({ pods: [{ ns: 'payments', pod: P_F1, state: 'no_samples', inventory: 'absent' }] }) }), trace))
      .toEqual({ kind: 'no_samples', cluster: c });
    expect(podMetricState(pod, ok({ data: body({ pods: [{ ns: '', pod: P_F1, state: 'ambiguous', inventory: 'unknown' }] }) }), trace))
      .toEqual({ kind: 'ambiguous', cluster: c });
    expect(podMetricState(pod, ok({ data: body({ pods: [] }) }), trace)).toEqual({ kind: 'no_samples', cluster: c });
    const st = podMetricState(pod, ok({ data: body({ pods: [{
      ns: 'payments', pod: P_F1, state: 'ok', cpu: [0.1, 0.2, 0.97, null, 0.4, 0.1], mem: [1, 2, 3, 4, 5, 6],
      cpuLimit: 1, memLimit: 10, inventory: 'present', phase: 'Running', restarts: 3, lastTermReason: 'OOMKilled', lastTermAt: 99,
    }] }) }), trace);
    expect(st.kind).toBe('ok');
    if (st.kind !== 'ok') return;
    expect([st.data.cpuAt, st.data.memAt, st.data.cpuLevel, st.data.memLevel]).toEqual([0.97, 4, 'err', 'none']);
    expect(st.data.now).toEqual({ phase: 'Running', restarts: 3, lastTermReason: 'OOMKilled', lastTermAtSec: 99 });
    expect([st.data.fetchedAtMs, st.data.cluster.name, st.data.namespace]).toEqual([42, 'dc-east-1', 'payments']);
    expect(st.data.instantPartial).toBe(false);
  });
  it('v0.10.968 — instant "partial" cevabı pod serisine taşınır', () => {
    const st = podMetricState(pod, ok({ data: body({ instant: 'partial', pods: [{
      ns: 'payments', pod: P_F1, state: 'ok', cpu: [0.5], mem: [1], cpuRequest: 0.5, inventory: 'present',
    }] }) }), trace);
    expect(st.kind === 'ok' && st.data.instantPartial).toBe(true);
  });
});

describe('hücre kopyası + grup sıcaklığı + kapsam', () => {
  it('okCellText: limit / istek / envanter', () => {
    expect(okCellText({ ...OK_SERIES, cpuAt: 0.97, cpuLimit: 1 }, 'cpu'))
      .toEqual({ text: '%97', tip: "0,97 çekirdek · limitin %97'si (limit şu an 1 çekirdek)" });
    expect(okCellText({ ...OK_SERIES, memAt: 1.42 * 1024 ** 3, memLimit: 2 * 1024 ** 3 }, 'mem'))
      .toEqual({ text: '%71', tip: "1,42 GiB · limitin %71'i (limit şu an 2 GiB)" });
    expect(okCellText({ ...OK_SERIES, cpuAt: 0.62, cpuRequest: 0.5 }, 'cpu'))
      .toEqual({ text: '0,62 c', tip: "limit tanımsız · isteğin %124'ü (şu an)" });
    expect(okCellText({ ...OK_SERIES, cpuAt: 0.62 }, 'cpu').tip).toBe('limit ve istek tanımsız');
    expect(okCellText({ ...OK_SERIES, inventory: 'absent' }, 'cpu').tip).toBe('Pod şu anki envanterde yok — limit bilinmiyor');
    expect(okCellText({ ...OK_SERIES, inventory: 'unknown' }, 'cpu').tip).toBe('limit okunamadı (kube-state-metrics sorgusu başarısız)');
  });
  it('v0.10.968 — instant partial + limit yok: "tanımsız" DEĞİL "bilinmiyor"; renk yok, mutlak değer', () => {
    const withReq = okCellText({ ...OK_SERIES, cpuAt: 0.62, cpuRequest: 0.5, instantPartial: true }, 'cpu');
    expect(withReq).toEqual({ text: '0,62 c', tip: "limit bilinmiyor (kube-state-metrics sorgusu kısmen başarısız) · isteğin %124'ü (şu an)" });
    const bare = okCellText({ ...OK_SERIES, memAt: 1.42 * 1024 ** 3, instantPartial: true }, 'mem');
    expect(bare.tip).toBe('limit bilinmiyor (kube-state-metrics sorgusu kısmen başarısız)');
    expect(bare.tip).not.toContain('tanımsız');
    // Limit okunduysa partial olsa da normal oran.
    expect(okCellText({ ...OK_SERIES, cpuAt: 0.97, cpuLimit: 1, instantPartial: true }, 'cpu').text).toBe('%97');
  });
  it('stateCellText: hata boş sonuç gibi YAZILMAZ', () => {
    expect(stateCellText({ kind: 'error', message: 'x', timeout: true }))
      .toEqual({ text: 'okunamadı', tip: 'Veri okunamadı — bu bir hata, boş sonuç değil. 10 sn zaman aşımı' });
    expect(stateCellText({ kind: 'no_samples', cluster: { id: '', name: '' } }).text).toBe('örnek yok');
    expect(stateCellText({ kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' }).tip)
      .toBe('dc-east-2 bir Remote Cluster kaydına eşlenmemiş');
  });
  it('hottestPodState: limite en yüksek oran, yoksa mutlak; ok yoksa bekleyen → hata', () => {
    const a: PodMetricState = { kind: 'ok', data: { ...OK_SERIES, cpuAt: 0.5, cpuLimit: 1 } };
    const b: PodMetricState = { kind: 'ok', data: { ...OK_SERIES, cpuAt: 0.9, cpuLimit: 4 } };
    const c: PodMetricState = { kind: 'ok', data: { ...OK_SERIES, cpuAt: 3 } };
    const map: Record<string, PodMetricState> = { a, b, c, l: { kind: 'loading' }, e: { kind: 'error', message: '', timeout: false } };
    expect(hottestPodState(['a', 'b', 'c'], p => map[p], 'cpu')).toBe(a);
    expect(hottestPodState(['c', 'e'], p => map[p], 'cpu')).toBe(c);
    expect(hottestPodState(['e', 'l'], p => map[p], 'cpu')).toBe(map.l);
    expect(hottestPodState(['e'], p => map[p], 'cpu')).toBe(map.e);
  });
  it('coverageSummary: eşlenen / eşlenmeyen cluster, sonuç dökümü', () => {
    const sp = [
      span('r', '', 'checkout-api', P_CK, { dur: 10 }),
      span('x', 'r', 'fraud-score-prod', P_F1, { cv: 'dc-east-2' }),
      span('y', 'r', 'fraud-score-prod', P_F2),
    ];
    const m: TraceMetricsModel = buildTraceMetricsModel(sp, undefined, null, false);
    const st: Record<string, PodMetricState> = {
      [P_CK]: { kind: 'ok', data: OK_SERIES },
      [P_F2]: { kind: 'error', message: '… (zaman aşımı)', timeout: true },
      [P_F1]: { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' },
    };
    const c = coverageSummary(m, p => st[p]);
    expect(c.lines).toEqual([
      { clusterValue: 'dc-east-1', name: 'dc-east-1', pods: 2, status: 'mapped' },
      { clusterValue: 'dc-east-2', name: 'dc-east-2', pods: 1, status: 'unmapped' },
    ]);
    expect([c.mapped, c.total, c.withData, c.failed, c.timeout]).toEqual([2, 3, 1, [P_F2], true]);
    expect(c.failedSvcs).toEqual(['fraud-score-prod']);
  });
  it('v0.10.968 — tümü okunamayan cluster değeri kapsam açığı DEĞİL: eşlenmiş sayılır, bekleme sonsuza kalmaz', () => {
    const sp = [
      span('r', '', 'checkout-api', P_CK, { dur: 10 }),
      span('x', 'r', 'audit-log-prod', P_AU, { cv: 'dc-east-2' }),
      span('y', 'r', 'fraud-score-prod', P_F2),
    ];
    const m: TraceMetricsModel = buildTraceMetricsModel(sp, undefined, null, false);
    const st: Record<string, PodMetricState> = {
      [P_CK]: { kind: 'ok', data: OK_SERIES },
      [P_F2]: { kind: 'no_samples', cluster: { id: 'c-1', name: 'dc-east-1' } },
      [P_AU]: { kind: 'error', message: 'dc-east-2: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)', timeout: true },
    };
    const c = coverageSummary(m, p => st[p]);
    expect(c.lines.find(l => l.clusterValue === 'dc-east-2')!.status).toBe('error');
    expect([c.mapped, c.total, c.pending]).toEqual([3, 3, false]);
    expect(c.failedSvcs).toEqual(['audit-log-prod']);
    expect(c.noSamplesSvcs).toEqual(['fraud-score-prod']);
    // Otomatik dilimi düşmüş + kalanı yüklenmemiş değer de "pending"de takılmaz
    // (öteki değerlerin uyarısını sonsuza dek gizlemesin).
    const P_AU2 = 'audit-log-prod-5f9c7d6b8-z7x5c';
    const sp2 = [...sp, span('z', 'r', 'audit-log-prod', P_AU2, { cv: 'dc-east-2' })];
    const m2 = buildTraceMetricsModel(sp2, undefined, null, false);
    const mixed = coverageSummary(m2, p => (p === P_AU2 ? { kind: 'idle' } : st[p]));
    expect(mixed.lines.find(l => l.clusterValue === 'dc-east-2')!.status).toBe('error');
    expect(mixed.pending).toBe(false);
    // Yalnız yüklenmemiş (hatasız) değer hâlâ bekliyor sayılır.
    expect(coverageSummary(m, p => (p === P_AU ? { kind: 'idle' } : st[p])).pending).toBe(true);
  });
});

describe('20. v0.10.962 — "Pod sayfasında aç" pencereyi taşır (hata 2) + jvmPodSeries', () => {
  const t0 = 1_700_000_000_123 * 1e6;
  const target = { pod: P_F1, cluster: 'dc-east-1', namespace: 'payments', service: 'fraud-score-prod' };
  it.each([5, 15, 60])('±%i dk → range=custom:<fromMs>-<toMs> + at=trace başlangıcı', (min) => {
    const w = traceMetricsWindow([{ ...span('a', '', 'fraud-score-prod', target.pod), startTime: t0, endTime: 0, durationMs: 10 }], min);
    const href = traceMetricsPodHref(target, w);
    expect(href.startsWith('/pod?')).toBe(true);
    const q = new URLSearchParams(href.slice('/pod?'.length));
    expect([q.get('pod'), q.get('cluster'), q.get('namespace'), q.get('service')])
      .toEqual([target.pod, 'dc-east-1', 'payments', 'fraud-score-prod']);
    const r = decodeRange(q.get('range'), { preset: '1h' });
    expect(r).toEqual({ preset: 'custom', fromMs: Math.floor(w.from / 1e6), toMs: Math.ceil(w.to / 1e6) });
    expect(r.toMs! - r.fromMs!).toBeGreaterThanOrEqual(2 * min * 60_000);
    expect(q.get('at')).toBe(String(Math.floor(w.startNs / 1e6)));
    expect(w.to).toBe(t0 + 10e6 + min * 60e9);
  });
  const s = (pod: string, vals: number[]) => ({ groupKey: [pod], points: vals.map((v, i) => ({ time: i * 1e9, value: v })) });
  it('jvm: boş seriler düşer, etiket kısa pod, tam ad fullKey; ölçek; null güvenli', () => {
    const out = jvmPodSeries([s('checkout-api-7c8977f965-7hrqz', [1, 2]), s('checkout-api-7c8977f965-empty', [])]);
    expect(out).toHaveLength(1);
    expect(out[0].groupKey).toEqual([shortPod('checkout-api-7c8977f965-7hrqz')]);
    expect(out[0].fullKey).toEqual(['checkout-api-7c8977f965-7hrqz']);
    expect(jvmPodSeries([s('p-a-b', [0.012])], 1000)[0].points[0].value).toBeCloseTo(12);
    const pts = [{ time: 0, value: 5 }];
    expect(jvmPodSeries([{ groupKey: ['x-y-z'], points: pts }])[0].points).toBe(pts);
    expect(jvmPodSeries(null)).toEqual([]);
    expect(jvmPodSeries(undefined)).toEqual([]);
    expect(shortPod('fraud-score-prod-6b7d9f8c5-m3t9w')).toBe('…6b7d9f8c5-m3t9w');
  });
  it('v0.10.968 — jvmPodSeries labelOf: etiket çiple aynı, tam ad fullKey\'de kalır', () => {
    const out = jvmPodSeries([s('checkout-api-7c8977f965-7hrqz', [1])], 1, () => '7hrqz');
    expect(out[0].groupKey).toEqual(['7hrqz']);
    expect(out[0].fullKey).toEqual(['checkout-api-7c8977f965-7hrqz']);
  });
});
