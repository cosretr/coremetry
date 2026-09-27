import { describe, it, expect } from 'vitest';
import type { SpanRow } from '@/lib/types';
import type {
  PodMetricSeries, PodMetricState, TraceMetricsModel, TracePodInfo, TraceErrorMark,
} from './traceMetricsModel';
import { MAX_SIBLING_LINES } from './traceMetricsModel';
import {
  median, siblingRatio, siblingStats, siblingCopy, compareChipLayout, CAP_REASON, buildChartItems, chartPods,
  chartCaption, timelineLanes, timelineTicks, timelineModel, nowSection, nowNote, stateMessage, errorReason,
  momentRow, THROTTLE_NOTE, podSuffix, suffixLabels, flaggedNav, relToTrace, agoText, podBadges, subLine,
  clusterNameOf, funnelCaption, compareColors, type LaneSpan,
} from './tracePodPanelModel';
import { preferredSlot, seriesColor } from '@/lib/chartFmt';

// tracePodPanel.test.ts — v0.10.968 — Trace › Metrics seçili pod paneli ve
// odak görünümünün SAF yardımcıları (tablo testli). Model fikstürleri elle
// kurulur (L-table iç yollarına mock YOK).

const T0 = 1_790_496_000 * 1e9; // trace başlangıcı (ns)
const SVC = 'fraud-score-prod';

function pod(name: string, o: Partial<TracePodInfo> = {}): TracePodInfo {
  const m = /^(.+)-([a-z0-9]{6,10})-([a-z0-9]{5})$/.exec(name);
  return {
    pod: name, namespace: 'payments', clusterValue: 'dc-east-1', service: SVC,
    spans: 5, errors: 0, maxSelfNs: 0, maxSelfSpanId: '', critNs: 0, critShare: 0,
    topSelf: false, rootCause: false, entry: false, firstError: null,
    activeFromNs: T0, activeToNs: T0 + 1e9, flagged: false, runtime: 'java', replicaSet: m ? `${m[1]}-${m[2]}` : '',
    nameParts: m ? { head: m[1], rs: m[2], tail: m[3] } : { head: name, rs: '', tail: '' },
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
    startSec: T0 / 1e9 - 300, stepSec: 15, cpu: [0.4, null, 0.5], mem: [1e9, 1.1e9, null],
    cpuAt: 0.5, memAt: 1.1e9, cpuLimit: 1, memLimit: 2 * 1024 ** 3, cpuRequest: 0.5, memRequest: 1.5 * 1024 ** 3,
    cpuLevel: 'none', memLevel: 'none', inventory: 'present', now: { phase: 'Running', restarts: 0 },
    cluster: { id: 'c-1', name: 'dc-east-1' }, namespace: 'payments', fetchedAtMs: T0 / 1e6 + 60_000,
    ...o,
  };
}
const ok = (o: Partial<PodMetricSeries> = {}): PodMetricState => ({ kind: 'ok', data: series(o) });

// ── 1. siblingRatio ─────────────────────────────────────────────────────────
describe('v0.10.968 — kardeş oranı (medyan)', () => {
  it.each([
    [[3, 1, 2], 2],
    [[4, 1, 3, 2], 2.5],
    [[5], 5],
    [[], null],
  ])('median(%j) = %s', (xs, want) => {
    expect(median(xs)).toBe(want);
  });
  it('seçili ÷ kardeş medyanı; ≥1,5 uyarı, altı değil', () => {
    expect(siblingRatio(1, [0.4, 0.4, 0.5])).toEqual({ ratio: 2.5, n: 3, warn: true });
    expect(siblingRatio(0.6, [0.4, 0.5])).toMatchObject({ n: 2, warn: false });
    expect(siblingRatio(0.6, [0.4, 0.5])!.ratio).toBeCloseTo(0.6 / 0.45);
    expect(siblingRatio(0.75, [0.5])!.warn).toBe(true);        // tam 1,5 → uyarı
    expect(siblingRatio(0.74, [0.5])!.warn).toBe(false);
  });
  it('kardeş yok / değer yok / medyan 0 → null (sonsuz kat yazılmaz)', () => {
    expect(siblingRatio(1, [])).toBeNull();
    expect(siblingRatio(1, [null, undefined])).toBeNull();
    expect(siblingRatio(null, [1])).toBeNull();
    expect(siblingRatio(1, [0, 0])).toBeNull();
  });
  it('siblingStats: karşılaştırmadaki diğer pod\'lar da kardeş, seçili hariç; metriksiz kardeş sayılmaz', () => {
    const ps = ['a', 'b', 'c', 'd'].map(s => pod(`${SVC}-6b7d9f8c5-${s}0000`));
    const m = model(ps);
    const vals: Record<string, PodMetricState> = {
      [ps[0].pod]: ok({ cpuAt: 1, memAt: 2 }),
      [ps[1].pod]: ok({ cpuAt: 0.4, memAt: 2 }),
      [ps[2].pod]: ok({ cpuAt: 0.4, memAt: 2 }),
      [ps[3].pod]: { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' },
    };
    const st = siblingStats(m, ps[0], p => vals[p])!;
    expect(st).toMatchObject({ total: 3, withMetrics: 2 });
    expect(st.cpu).toEqual({ ratio: 2.5, n: 2, warn: true });
    expect(st.mem).toEqual({ ratio: 1, n: 2, warn: false });
    const segs = siblingCopy(st);
    expect(segs.map(s => s.t).join('')).toBe("CPU, bu trace'teki diğer 2 pod'un medyanının 2,5 katı · Bellek 1 katı");
    expect(segs.find(s => s.t === '2,5 katı')).toMatchObject({ tone: 'warn', strong: true });
    expect(segs.find(s => s.t === '1 katı')!.tone).toBeUndefined();
  });
  it('tek pod / metriksiz kardeşler → dürüst metin', () => {
    const solo = pod(`${SVC}-6b7d9f8c5-a0000`);
    expect(siblingCopy(siblingStats(model([solo]), solo, () => ok())!)[0].t)
      .toBe("Bu trace'te servisin tek pod'u — kıyaslanacak kardeş yok");
    const b = pod(`${SVC}-6b7d9f8c5-b0000`);
    const st = siblingStats(model([solo, b]), solo, p => (p === solo.pod ? ok() : { kind: 'loading' }))!;
    expect(siblingCopy(st)[0].t).toBe("Bu trace'teki diğer 1 pod'un metriği yok — kıyaslanacak kardeş yok");
    expect(siblingStats(model([solo]), solo, () => ({ kind: 'loading' }))).toBeNull();
  });
});

// ── 2. compareChipLayout ────────────────────────────────────────────────────
describe('v0.10.968 — karşılaştırma çipleri', () => {
  const ps = Array.from({ length: 10 }, (_, i) => pod(`${SVC}-6b7d9f8c5-p${i}aaa`, { flagged: i === 8 }));
  const allOk = () => ok();

  it('10 pod → ilk 6 + işaretli + karşılaştırılan görünür, gerisi taşmada (sıra korunur)', () => {
    const { visible, overflow } = compareChipLayout(ps, [ps[0].pod, ps[7].pod], allOk);
    expect(visible.map(c => c.pod)).toEqual([0, 1, 2, 3, 4, 5, 7, 8].map(i => ps[i].pod));
    expect(overflow.map(c => c.pod)).toEqual([ps[6].pod, ps[9].pod]);
    expect(visible[0]).toMatchObject({ on: true, slot: 0, label: 'p0aaa', disabled: false });
    expect(visible[6]).toMatchObject({ on: true, slot: 1 });
    expect(visible[0].title).toBe(`${ps[0].pod} · karşılaştırmadan çıkar`);
    expect(visible[1].title).toBe(`${ps[1].pod} · karşılaştırmaya ekle`);
  });
  it('tavanda (4) seçisiz aynı-servis çipi devre dışı + gerekçe BİREBİR; seçili çipler açık', () => {
    const cmp = ps.slice(0, 4).map(p => p.pod);
    const { visible } = compareChipLayout(ps, cmp, allOk);
    const blocked = visible.find(c => c.pod === ps[4].pod)!;
    expect(blocked).toMatchObject({ disabled: true, title: CAP_REASON });
    expect(CAP_REASON).toBe('En çok 4 pod üst üste çizilir — önce birini çıkarın');
    expect(visible.filter(c => c.on).every(c => !c.disabled)).toBe(true);
  });
  it('eşlenmemiş kardeş: "metrik yok (<cl> eşlenmemiş)" ile devre dışı; menü notu', () => {
    const st = (p: string): PodMetricState => (p === ps[2].pod
      ? { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' } : ok());
    const { visible } = compareChipLayout(ps, [ps[0].pod], st);
    const c = visible.find(x => x.pod === ps[2].pod)!;
    expect(c).toMatchObject({ disabled: true, note: 'metrik yok' });
    expect(c.title).toBe(`${ps[2].pod} · metrik yok (dc-east-2 eşlenmemiş)`);
    const noCv = compareChipLayout(ps, [ps[0].pod], p => (p === ps[2].pod
      ? { kind: 'unmapped', clusterValue: '', reason: 'no_cluster_value' } : ok())).visible.find(x => x.pod === ps[2].pod)!;
    expect(noCv.title).toBe(`${ps[2].pod} · metrik yok (span'lerde cluster özniteliği yok)`);
  });
});

// ── 3. buildChartItems ──────────────────────────────────────────────────────
describe('v0.10.968 — grafik öğeleri', () => {
  const ps = Array.from({ length: 16 }, (_, i) => pod(`${SVC}-6b7d9f8c5-p${String(i).padStart(2, '0')}aa`));
  const m = model(ps);
  const cmp = [ps[0].pod, ps[1].pod];
  const base = (siblingLines: boolean, st: (p: string) => PodMetricState = () => ok()) => {
    const cp = chartPods(m, ps[0], cmp, st);
    return { ...cp, siblingLines, traceStartNs: m.traceStartNs, traceEndNs: m.traceEndNs, stepSec: 15 };
  };

  it('karşılaştırılan → data, kardeş → muted; en çok 12 kardeş + "+N çizilmedi" sayısı', () => {
    const b = buildChartItems('mem', base(true));
    expect(b.items.slice(0, 2).map(i => [i.name, i.role])).toEqual([['p00aa', 'data'], ['p01aa', 'data']]);
    const muted = b.items.filter(i => i.role === 'muted');
    expect(muted).toHaveLength(MAX_SIBLING_LINES);
    expect(b.siblingsDrawn).toBe(12);
    expect(b.siblingsHidden).toBe(2);
    expect(b.legend.map(l => l.label)).toEqual(['p00aa', 'p01aa']);
    expect(chartCaption({ durNs: 3.42e9, stepSec: 15, service: SVC, siblingsDrawn: 12, siblingsHidden: 2, cpuNoLimit: false }))
      .toContain(`Soluk çizgiler: ${SVC} servisinin bu trace'teki diğer 14 pod'u (+2 çizilmedi).`);
  });
  it('kardeş çizgileri kapalı → kardeş yok, not yok', () => {
    const b = buildChartItems('cpu', base(false));
    expect(b.items.every(i => i.role === 'data')).toBe(true);
    expect(b.siblingsDrawn).toBe(0);
    expect(b.siblingsHidden).toBe(0);
    expect(chartCaption({ durNs: 3.42e9, stepSec: 15, service: SVC, siblingsDrawn: 0, siblingsHidden: 0, cpuNoLimit: false }))
      .not.toContain('Soluk çizgiler');
  });
  it('metriği olmayan pod ne karşılaştırmada ne kardeşte çizilir', () => {
    const b = buildChartItems('cpu', base(true, p => (p === ps[1].pod || p === ps[5].pod ? { kind: 'loading' } : ok())));
    expect(b.items.filter(i => i.role === 'data').map(i => i.name)).toEqual(['p00aa']);
    expect(b.items.some(i => i.name === 'p05aa')).toBe(false);
  });
  it('trace bandı en az BİR adım geniş; gerçek bitiş endSec\'te; renk accent2, etiket trace', () => {
    const b = buildChartItems('mem', base(false));
    expect(b.regions).toHaveLength(1);
    expect(b.regions[0]).toMatchObject({ fromSec: T0 / 1e9, toSec: T0 / 1e9 + 15, color: 'var(--accent2)', label: 'trace' });
    expect(b.regions[0].endSec).toBeCloseTo(T0 / 1e9 + 3.42, 3);
    const long = buildChartItems('mem', { ...base(false), traceEndNs: T0 + 40e9 });
    expect(long.regions[0].toSec).toBe(T0 / 1e9 + 40);
  });
  it('limit çizgisi yalnız limit BİLİNİRKEN; CPU limitsizse bayrak', () => {
    expect(buildChartItems('cpu', base(false)).thresholds).toEqual([{ value: 1, label: 'limit (şu an)', color: 'var(--warn)' }]);
    const nolim = base(false, () => ok({ cpuLimit: undefined, memLimit: undefined }));
    const c = buildChartItems('cpu', nolim);
    expect(c.thresholds).toEqual([]);
    expect(c.noLimit).toBe(true);
    const mm = buildChartItems('mem', nolim);
    expect(mm.thresholds).toEqual([]);
    expect(mm.noLimit).toBe(false);
    expect(chartCaption({ durNs: 3.42e9, stepSec: 15, service: SVC, siblingsDrawn: 0, siblingsHidden: 0, cpuNoLimit: true }))
      .toContain('CPU limiti tanımsız: limit çizgisi yok.');
  });
  it('seri: zaman = start + i·step (ns), boşluk NaN (sıfır çizilmez)', () => {
    const b = buildChartItems('cpu', base(false));
    const pts = b.items[0].series[0].points;
    expect(pts[0]).toEqual({ time: (T0 / 1e9 - 300) * 1e9, value: 0.4 });
    expect(pts[1].time).toBe((T0 / 1e9 - 285) * 1e9);
    expect(Number.isNaN(pts[1].value)).toBe(true);
  });
  it('huni cümlesi: tek adım / çok adım / adım yok', () => {
    expect(funnelCaption(3.42e9, 15)).toBe("Metrik grafiklerinde bu 3,42 sn tek bir 15 sn'lik adıma düşer.");
    expect(funnelCaption(40e9, 15)).toBe('Metrik grafiklerinde bu 40,00 sn yaklaşık 3 adıma (15 sn) yayılır.');
    expect(funnelCaption(3.42e9, null)).toBeNull();
  });
  it('başlık gerçek adımı söyler', () => {
    expect(chartCaption({ durNs: 3.42e9, stepSec: 60, service: SVC, siblingsDrawn: 0, siblingsHidden: 0, cpuNoLimit: false }))
      .toMatch(/^Mavi bant: trace \(3,42 sn; en az bir 60 sn adım genişliğinde çizilir\)\./);
  });
});

// ── 4. timelineLanes / timelineTicks ────────────────────────────────────────
describe('v0.10.968 — span zaman çizelgesi', () => {
  const S = (id: string, a: number, b: number): LaneSpan => ({ id, startNs: a, endNs: b });
  const overlaps = (items: LaneSpan[], laneOf: Map<string, number>) => {
    for (const a of items) for (const b of items) {
      if (a.id >= b.id || laneOf.get(a.id) !== laneOf.get(b.id)) continue;
      if (a.startNs < b.endNs && b.startNs < a.endNs) return true;
    }
    return false;
  };

  it('çakışma yok, en az şerit (en çok eşzamanlılık), bitişe dokunan aynı şeride', () => {
    const items = [S('a', 0, 10), S('b', 5, 15), S('c', 10, 20), S('d', 12, 14), S('e', 20, 30)];
    const r = timelineLanes(items);
    expect(overlaps(items, r.laneOf)).toBe(false);
    expect(r.lanes).toBe(3);          // 12–14 anında a? hayır: b, c, d → 3
    expect(r.laneOf.get('c')).toBe(0); // a bitti (10) → şerit 0
    expect(r.overflow).toBe(0);
  });
  it('deterministik: girdi sırası sonucu değiştirmez', () => {
    const items = [S('a', 0, 10), S('b', 0, 10), S('c', 2, 4), S('d', 3, 9), S('e', 9, 12)];
    const want = [...timelineLanes(items).laneOf.entries()].sort();
    for (const perm of [[...items].reverse(), [items[2], items[0], items[4], items[1], items[3]]]) {
      expect([...timelineLanes(perm).laneOf.entries()].sort()).toEqual(want);
    }
  });
  it('rastgele 200 span: çakışma yok ve şerit sayısı = en yüksek eşzamanlılık', () => {
    let seed = 42;
    const rnd = () => ((seed = (seed * 1103515245 + 12345) % 2 ** 31) / 2 ** 31);
    const items = Array.from({ length: 200 }, (_, i) => { const a = Math.floor(rnd() * 1000); return S(`s${i}`, a, a + 1 + Math.floor(rnd() * 80)); });
    const r = timelineLanes(items);
    expect(overlaps(items, r.laneOf)).toBe(false);
    let peak = 0;
    for (let t = 0; t < 1100; t++) peak = Math.max(peak, items.filter(s => s.startNs <= t && t < s.endNs).length);
    expect(r.lanes).toBe(peak);
  });
  it('maxLanes aşılınca en erken boşalan şeride biner ve sayılır', () => {
    const r = timelineLanes([S('a', 0, 10), S('b', 0, 20), S('c', 0, 5)], 2);
    expect(r.lanes).toBe(2);
    expect(r.overflow).toBe(1);
  });
  it.each([
    [3.42e9, ['0', '0,5 sn', '1 sn', '1,5 sn', '2 sn', '2,5 sn', '3 sn', '3,42 sn']],
    [440e6, ['0', '100 ms', '200 ms', '300 ms', '440 ms']],
    [1e9, ['0', '0,2 sn', '0,4 sn', '0,6 sn', '0,8 sn', '1,00 sn']],
  ])('timelineTicks(%s) düzgün adımlar + trace süresi', (dur, want) => {
    expect(timelineTicks(dur).map(t => t.label)).toEqual(want);
    const t = timelineTicks(dur);
    expect(t[t.length - 1]).toMatchObject({ ns: dur, end: true });
  });

  it('timelineModel: hatalı / kritik span, bu pod\'un hatası ayrı, işaret başlığı', () => {
    const p = pod(`${SVC}-6b7d9f8c5-m3t9w`, { spans: 2, errors: 1, critNs: 1.3e9, activeFromNs: T0 + 0.294e9, activeToNs: T0 + 2.81e9 });
    const other = pod('audit-log-prod-5f9c7d6b8-q2w4e', { service: 'audit-log-prod' });
    const sp = (id: string, name: string, start: number, ms: number, status = 'ok') =>
      ({ spanId: id, name, startTime: start, durationMs: ms, statusCode: status } as unknown as SpanRow);
    const marks: TraceErrorMark[] = [
      { spanId: 'e1', pod: p.pod, service: SVC, name: 'POST /v1/score', status: 'DEADLINE_EXCEEDED', timeNs: T0 + 1.802e9 },
      { spanId: 'x1', pod: other.pod, service: 'audit-log-prod', name: 'PUT /v1/audit', status: '409', timeNs: T0 + 0.758e9 },
    ];
    const m = model([p, other], {
      podSpans: new Map([[p.pod, [sp('e1', 'POST /v1/score', T0 + 0.294e9, 1508, 'error'), sp('k1', 'score.model.evaluate', T0 + 0.338e9, 1100)]]]),
      criticalIds: new Set(['k1']), errorMarks: marks,
    });
    const tl = timelineModel(m, p);
    expect(tl.bars.find(b => b.id === 'e1')).toMatchObject({ error: true, critical: false, status: 'DEADLINE_EXCEEDED' });
    expect(tl.bars.find(b => b.id === 'k1')).toMatchObject({ error: false, critical: true, status: 'ok' });
    expect(tl.lanes).toBe(2);
    expect(tl.marks.map(x => x.own)).toEqual([true, false]);
    expect(tl.marks[0].title).toBe(`${SVC} …m3t9w · POST /v1/score · DEADLINE_EXCEEDED · +1,802 sn`);
    expect(tl.summary).toBe('2 span · 1 hata · kritik yolda 1,30 sn');
    expect(tl.active.x0).toBeCloseTo(0.294 / 3.42);
  });
});

// ── 5. nowSection ───────────────────────────────────────────────────────────
describe('v0.10.968 — "Şu an" satırları', () => {
  const row = (d: PodMetricSeries, k: string) => nowSection(d, T0).rows.find(r => r.k === k)!;
  it('restart: 0 nötr, >0 uyarı, bilinmiyor soluk "—"', () => {
    expect(row(series({ now: { restarts: 0 } }), 'Restart')).toEqual({ k: 'Restart', v: '0', tone: undefined });
    expect(row(series({ now: { restarts: 3 } }), 'Restart')).toMatchObject({ v: '3', tone: 'warn' });
    expect(row(series({ now: {} }), 'Restart')).toMatchObject({ v: '—', tone: 'faint' });
  });
  it('son sonlanma: OOMKilled tehlike rozeti + zaman; diğer neden nötr; zaman yok; hiç yok', () => {
    const at = T0 / 1e9 + 12 * 60;
    const oom = row(series({ now: { lastTermReason: 'OOMKilled', lastTermAtSec: at } }), 'Son sonlanma');
    expect(oom.badge).toEqual({ t: 'OOMKilled', tone: 'danger' });
    expect(oom.v).toMatch(/^ \d\d:\d\d:\d\d · trace'ten 12 dk sonra$/);
    const err = row(series({ now: { lastTermReason: 'Error' } }), 'Son sonlanma');
    expect(err).toMatchObject({ v: ' · zaman bilinmiyor', badge: { t: 'Error', tone: 'neutral' } });
    expect(row(series({ now: {} }), 'Son sonlanma')).toMatchObject({ v: '—', tone: 'faint' });
  });
  it('limit / istek: "1 / 0,5 çekirdek", "tanımsız / 0,5 çekirdek", bellek birimli', () => {
    expect(row(series(), 'CPU limit / istek').v).toBe('1 / 0,5 çekirdek');
    expect(row(series({ cpuLimit: undefined }), 'CPU limit / istek').v).toBe('tanımsız / 0,5 çekirdek');
    expect(row(series(), 'Bellek limit / istek').v).toBe('2 GiB / 1,5 GiB');
    expect(row(series({ memLimit: undefined, memRequest: undefined }), 'Bellek limit / istek').v).toBe('tanımsız / tanımsız');
  });
  it('faz; envanter yok / okunamadı notu', () => {
    expect(row(series(), 'Faz').v).toBe('Running');
    expect(nowSection(series(), T0).note).toBeNull();
    expect(nowSection(series({ inventory: 'absent' }), T0).note).toMatch(/envanterde yok/);
    expect(nowSection(series({ inventory: 'unknown' }), T0).note).toBe('kube-state-metrics okunamadı: faz, restart ve limitler bilinmiyor.');
  });
  it('relToTrace / agoText ölçek taşması', () => {
    const s0 = T0 / 1e9;
    expect(relToTrace(s0 + 30, T0)).toBe("trace'le aynı dakikada");
    expect(relToTrace(s0 - 5 * 60, T0)).toBe("trace'ten 5 dk önce");
    expect(relToTrace(s0 + 3 * 3600, T0)).toBe("trace'ten 3 sa sonra");
    expect(agoText(18 * 60_000, 0)).toBe('18 dk önce');
    expect(agoText(10_000, 0)).toBe('az önce');
    expect(agoText(3 * 86_400_000, 0)).toBe('3 gün önce');
  });
  it('metriği ok olmayan pod: eşlenmemiş notu birebir; hata hata tonunda', () => {
    expect(nowNote({ kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' })!.t)
      .toBe('Cluster eşlenmemiş: faz, restart ve limitler okunamıyor.');
    expect(nowNote({ kind: 'error', message: 'x', timeout: false })).toMatchObject({ err: true });
    expect(nowNote({ kind: 'loading' })).toBeNull();
  });
});

// ── 6. stateMessage ─────────────────────────────────────────────────────────
describe('v0.10.968 — metrik durum mesajı', () => {
  const kinds: PodMetricState[] = [
    { kind: 'loading' }, { kind: 'idle' }, { kind: 'off' },
    { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' },
    { kind: 'unmapped', clusterValue: '', reason: 'no_cluster_value' },
    { kind: 'no_samples', cluster: { id: 'c', name: 'dc-east-1' } },
    { kind: 'ambiguous', cluster: { id: 'c', name: 'dc-east-1' } },
    // v0.10.968 — sunucunun GERÇEK biçimi (chunkError errUpstream önekini atar).
    { kind: 'error', message: 'dc-east-1: Thanos 10 sn içinde yanıt vermedi (zaman aşımı)', timeout: true },
    { kind: 'error', message: 'dc-east-1: CPU sorgusu: HTTP 500 internal: boom.', timeout: false },
  ];
  it('ok → null; diğer her tür bir mesaj', () => {
    expect(stateMessage(ok())).toBeNull();
    for (const k of kinds) expect(stateMessage(k)).not.toBeNull();
  });
  it('birebir metinler', () => {
    expect(stateMessage(kinds[1])).toMatchObject({ title: 'Metrik henüz yüklenmedi.', action: 'load' });
    expect(stateMessage(kinds[2])!.title).toBe('Thanos Remote Cluster tanımlı değil.');
    expect(stateMessage(kinds[3])).toMatchObject({
      title: "Bu pod'un cluster'ı (dc-east-2) bir Remote Cluster kaydına eşlenmemiş — metrik gösterilemiyor.",
      sub: "Bu trace'teki bilgiler (span, hata, kritik yol) tam; yalnız Thanos grafikleri yok.",
      settings: true,
    });
    expect(stateMessage(kinds[5])).toMatchObject({
      title: 'Bu pencerede Thanos örneği yok.',
      sub: 'Cluster eşli ve sorgu başarılı, ama bu pod için CPU / bellek serisi dönmedi.',
      tone: 'none',
    });
    expect(stateMessage(kinds[6])!.title).toBe("Pod adı birden çok namespace'te — span'de k8s.namespace.name yok.");
    expect(stateMessage(kinds[0])).toMatchObject({ loading: true });
  });
  it('HATA asla boş sonuç gibi yazılmaz: hata tonu + "okunamadı" + yeniden dene', () => {
    for (const e of kinds.slice(7)) {
      const m = stateMessage(e)!;
      expect(m.tone).toBe('err');
      expect(m.action).toBe('retry');
      expect(m.title).toBe('Veri okunamadı — bu bir hata, boş sonuç değil.');
      expect(`${m.title} ${m.sub}`).not.toMatch(/örnek yok|metrik yok|veri yok/);
    }
    expect(stateMessage(kinds[7])!.sub).toBe("10 sn zaman aşımı. Diğer cluster'ların metrikleri etkilenmedi.");
    expect(stateMessage(kinds[8])!.sub).toBe("dc-east-1: CPU sorgusu: HTTP 500 internal: boom. Diğer cluster'ların metrikleri etkilenmedi.");
    // Hata olmayan hiçbir tür hata tonu taşımaz.
    for (const k of kinds.slice(0, 7)) expect(stateMessage(k)!.tone).toBe('none');
  });
  it('errorReason: zaman aşımı eşlemesi ve ≤80 kırpma', () => {
    expect(errorReason({ message: 'x zaman aşımı', timeout: false })).toBe('10 sn zaman aşımı');
    expect(errorReason({ message: 'a'.repeat(200), timeout: false })).toHaveLength(80);
    expect(errorReason({ message: '  ', timeout: false })).toBe('bilinmeyen hata');
  });
});

// ── Trace anında satırı, başlık, rozet, odak gezinmesi ─────────────────────
describe('v0.10.968 — trace anı satırı ve başlık', () => {
  it('limitli CPU: "0,97 çekirdek · limitin %97\'si" + kısıtlama notu ≥%90', () => {
    const r = momentRow('cpu', series({ cpuAt: 0.97, cpuLevel: 'err' }));
    expect(r).toEqual({ k: 'CPU', main: "0,97 çekirdek · limitin %97'si", level: 'err', rest: ' limit 1 çekirdek (şu an)', cap: THROTTLE_NOTE });
    expect(momentRow('cpu', series({ cpuAt: 0.5 })).cap).toBeUndefined();
    expect(momentRow('mem', series({ memAt: 1.42 * 1024 ** 3, memLevel: 'warn' })))
      .toMatchObject({ main: "1,42 GiB · limitin %71'i", level: 'warn', rest: ' limit 2 GiB (şu an)' });
  });
  it('limitsiz: renk yok, isteğe oran; envanter yok / okunamadı; değer yok', () => {
    const r = momentRow('cpu', series({ cpuAt: 0.62, cpuLimit: undefined }));
    expect(r).toMatchObject({ main: '0,62 çekirdek · limit tanımsız', level: 'none', rest: " isteğin %124'ü" });
    expect(r.title).toBe("İsteğin %124'ü (istek 0,5 çekirdek, şu an)");
    expect(momentRow('cpu', series({ cpuLimit: undefined, inventory: 'absent' })).rest).toContain('Pod şu anki envanterde yok — limit bilinmiyor');
    expect(momentRow('cpu', series({ cpuLimit: undefined, inventory: 'unknown' })).rest).toContain('limit okunamadı (kube-state-metrics sorgusu başarısız)');
    expect(momentRow('cpu', series({ cpuAt: null })).main).toBe('—');
  });
  it('son ek + benzersiz etiket; alt satır; cluster adı', () => {
    const a = pod(`${SVC}-6b7d9f8c5-m3t9w`);
    const b = pod(`${SVC}-7c8d9e0f1-m3t9w`);
    const c = pod('ledger-writer-prod-1', { service: 'ledger-writer-prod' });
    expect(podSuffix(a)).toBe('m3t9w');
    expect(podSuffix(c)).toBe('prod-1');
    const l = suffixLabels([a, b, c]);
    expect(l.get(a.pod)).toBe(a.pod);          // çakışma → tam ad
    expect(l.get(c.pod)).toBe('prod-1');
    expect(subLine(a, ok())).toEqual([SVC, 'ns payments', 'dc-east-1', 'java']);
    expect(subLine({ ...a, runtime: '' }, { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' }))
      .toEqual([SVC, 'ns payments', 'dc-east-1']);
    expect(clusterNameOf({ kind: 'error', message: '', timeout: false })).toBe('');
    expect(clusterNameOf({ kind: 'no_samples', cluster: { id: 'c', name: 'dc-east-2' } })).toBe('dc-east-2');
  });
  it('rozetler: kök hata · giriş · N hata · kritik yol · en büyük öz süre', () => {
    const p = pod(`${SVC}-6b7d9f8c5-m3t9w`, { rootCause: true, entry: true, errors: 4, critShare: 0.38, critNs: 1.3e9, topSelf: true });
    expect(podBadges(p, 0.03)).toEqual([
      { t: 'kök hata', tone: 'danger' }, { t: 'giriş', tone: 'neutral' }, { t: '4 hata', tone: 'danger' },
      { t: 'kritik yol %38 · 1,30 sn', tone: 'neutral' }, { t: 'en büyük öz süre', tone: 'neutral' },
    ]);
    expect(podBadges(pod('x-y'), 0.03)).toEqual([]);
  });
  it('flaggedNav: model sırasında önceki / sonraki işaretli; uçlarda null', () => {
    const ps = ['a', 'b', 'c', 'd', 'e'].map((s, i) => pod(`${SVC}-6b7d9f8c5-${s}0000`, { flagged: i !== 2 }));
    const m = model(ps);
    expect(flaggedNav(m, ps[0].pod)).toMatchObject({ prev: null, next: ps[1] });
    expect(flaggedNav(m, ps[2].pod)).toMatchObject({ prev: ps[1], next: ps[3] });   // işaretsiz seçili
    expect(flaggedNav(m, ps[4].pod)).toMatchObject({ prev: ps[3], next: null });
    expect(flaggedNav(m, ps[0].pod).list).toHaveLength(4);
  });
});

// ── v0.10.968 — inceleme turu ───────────────────────────────────────────────
describe('v0.10.968 — okunamayan kardeş boş sayılmaz (F6)', () => {
  const ps = ['a', 'b', 'c'].map(s => pod(`${SVC}-6b7d9f8c5-${s}0000`));
  const m = model(ps);
  const err: PodMetricState = { kind: 'error', message: 'dc-east-2: CPU sorgusu: HTTP 500 internal: boom', timeout: false };
  it('bütün kardeşler okunamadı → "okunamadı" hata tonunda, "metriği yok" DEĞİL', () => {
    const st = siblingStats(m, ps[0], p => (p === ps[0].pod ? ok() : err))!;
    expect(st).toMatchObject({ total: 2, withMetrics: 0, failed: 2 });
    const segs = siblingCopy(st);
    expect(segs).toEqual([{ t: "Bu trace'teki diğer 2 pod'un metrikleri okunamadı — kıyaslanacak kardeş yok", tone: 'err', strong: true }]);
    expect(segs[0].t).not.toContain('metriği yok');
  });
  it('okunamayan + eşlenmemiş karışık → yine "okunamadı"', () => {
    const st = siblingStats(m, ps[0], p => (p === ps[0].pod ? ok() : p === ps[1].pod ? err
      : { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' }))!;
    expect(st.failed).toBe(1);
    expect(siblingCopy(st)[0]).toMatchObject({ tone: 'err' });
  });
  it('taşma menüsü notu: okunamayan çip "okunamadı", eşlenmemiş "metrik yok" kalır', () => {
    const st = (p: string): PodMetricState => (p === ps[1].pod ? err
      : p === ps[2].pod ? { kind: 'unmapped', clusterValue: 'dc-east-2', reason: 'no_remote_cluster' } : ok());
    const { visible } = compareChipLayout(ps, [ps[0].pod], st);
    expect(visible.find(c => c.pod === ps[1].pod)!.note).toBe('okunamadı');
    expect(visible.find(c => c.pod === ps[2].pod)!.note).toBe('metrik yok');
  });
});

describe('v0.10.968 — kube-state-metrics kısmen düştü: "tanımsız" değil "bilinmiyor" (MF-3)', () => {
  const part = (o: Partial<PodMetricSeries> = {}) => series({ instantPartial: true, cpuLimit: undefined, memLimit: undefined, ...o });
  it('momentRow: istek biliniyorsa oranı da söyler; hiçbir yerde "tanımsız" yok', () => {
    const r = momentRow('cpu', part({ cpuAt: 0.62 }));
    expect(r).toMatchObject({ main: '0,62 çekirdek · limit bilinmiyor', level: 'none' });
    expect(r.rest).toBe(" · kube-state-metrics kısmen okunamadı · isteğin %124'ü");
    const n = momentRow('mem', part({ memRequest: undefined }));
    expect(`${n.main}${n.rest}`).not.toMatch(/tanımsız/);
    expect(n.rest).toBe(' · kube-state-metrics kısmen okunamadı');
  });
  it('nowSection: eksik alan "bilinmiyor" + not; tam okumada davranış aynı', () => {
    const { rows, note } = nowSection(part({ cpuRequest: 0.5, memRequest: undefined }), T0);
    expect(rows.find(r => r.k === 'CPU limit / istek')!.v).toBe('bilinmiyor / 0,5 çekirdek');
    expect(rows.find(r => r.k === 'Bellek limit / istek')!.v).toBe('bilinmiyor / bilinmiyor');
    expect(note).toBe('kube-state-metrics kısmen okunamadı: eksik alanlar bilinmiyor.');
    expect(nowSection(series({ cpuLimit: undefined }), T0).rows.find(r => r.k === 'CPU limit / istek')!.v).toBe('tanımsız / 0,5 çekirdek');
  });
});

describe('v0.10.968 — çakışmasız karşılaştırma renkleri (MF-1)', () => {
  const a = pod(`${SVC}-6b7d9f8c5-p5r8d`);
  const b = pod(`${SVC}-6b7d9f8c5-b2q6f`);
  const m = model([a, b]);
  const labels = suffixLabels([a, b]);
  it('önkoşul: iki etiket de hash yuvası 0 (hash-yalnız renk ÇAKIŞIR)', () => {
    expect(preferredSlot('p5r8d')).toBe(0);
    expect(preferredSlot('b2q6f')).toBe(0);
    expect(seriesColor('p5r8d')).toBe(seriesColor('b2q6f'));
  });
  it('compareColors farklı renk verir; ilk gelen tercih ettiği yuvayı korur', () => {
    const c = compareColors([a.pod, b.pod], labels, 'dark');
    expect(c.get('p5r8d')).not.toBe(c.get('b2q6f'));
    expect(c.get('p5r8d')).toBe(seriesColor('p5r8d'));
  });
  it('buildChartItems: çizgi rengi = lejant rengi = harita; kardeş (muted) renksiz', () => {
    const c = compareColors([a.pod, b.pod], labels, 'dark');
    const cp = chartPods(m, a, [a.pod, b.pod], () => ok());
    const bld = buildChartItems('mem', { ...cp, siblingLines: true, traceStartNs: T0, traceEndNs: T0 + 1e9, stepSec: 15, colors: c });
    const data = bld.items.filter(i => i.role === 'data');
    expect(data.map(i => i.color)).toEqual([c.get('p5r8d'), c.get('b2q6f')]);
    expect(new Set(data.map(i => i.color)).size).toBe(2);
    expect(bld.legend.map(l => l.color)).toEqual(data.map(i => i.color));
    const three = pod(`${SVC}-6b7d9f8c5-z9z9z`);
    const m3 = model([a, b, three]);
    const cp3 = chartPods(m3, a, [a.pod], () => ok());
    const b3 = buildChartItems('cpu', { ...cp3, siblingLines: true, traceStartNs: T0, traceEndNs: T0 + 1e9, stepSec: 15, colors: compareColors([a.pod], suffixLabels([a, b, three]), 'dark') });
    expect(b3.items.filter(i => i.role === 'muted').every(i => i.color === undefined)).toBe(true);
  });
});

describe('v0.10.968 — "+N" menüsü açıkken üyelik donar (F4 ui)', () => {
  const ps = Array.from({ length: 8 }, (_, i) => pod(`${SVC}-6b7d9f8c5-q${i}aaa`));
  it('pinned: işaretlenen taşma çipi menüde kalır; bırakılınca görünür akar', () => {
    const pinned = new Set([ps[6].pod, ps[7].pod]);
    const open = compareChipLayout(ps, [ps[0].pod, ps[6].pod], () => ok(), suffixLabels(ps), pinned);
    expect(open.overflow.map(c => [c.pod, c.on])).toEqual([[ps[6].pod, true], [ps[7].pod, false]]);
    const closed = compareChipLayout(ps, [ps[0].pod, ps[6].pod], () => ok());
    expect(closed.visible.map(c => c.pod)).toContain(ps[6].pod);
    expect(closed.overflow.map(c => c.pod)).toEqual([ps[7].pod]);
  });
  it('visibleOnlyByOn yalnız ilk 6 dışı + işaretsiz + karşılaştırmadaki çipte', () => {
    const { visible } = compareChipLayout(ps, [ps[0].pod, ps[6].pod], () => ok());
    expect(visible.filter(c => c.visibleOnlyByOn).map(c => c.pod)).toEqual([ps[6].pod]);
  });
  it('son çipin ipucu reddi söyler (eylemi vaat etmez)', () => {
    const { visible } = compareChipLayout(ps, [ps[0].pod], () => ok());
    expect(visible[0].title).toBe(`${ps[0].pod} · son pod karşılaştırmadan çıkarılamaz`);
    expect(compareChipLayout(ps, [ps[0].pod, ps[1].pod], () => ok()).visible[0].title).toBe(`${ps[0].pod} · karşılaştırmadan çıkar`);
  });
});
