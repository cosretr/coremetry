// anomalyDetail.test.ts — v0.10.1032 (operatör: "Anomali ve alert rule'lara
// girdiğimde drawer çıkıyor. Exception gibi detay gözükmüyor.").
//
// Anomali olayı artık iki yüzeyde anlatılıyor: /anomalies çekmecesi ve
// Problems kuyruğundaki tam sayfa. İkisinin ortak saf çekirdeği bu modül —
// pencere, davranış kanıtının (bozuk JSON dahil) ayrıştırılması, pivotlar,
// türün tek grafiği. Biri değişirse iki ekran birlikte değişir.
import { describe, it, expect } from 'vitest';
import type { AnomalyEvent, BehaviorChangeDetails } from '@/lib/types';
import type { CosreChartSpec } from '@/components/cosreChartSpec';
import {
  ANOMALY_CHART_EMPTY, ANOMALY_KIND_LABEL, ANOMALY_KIND_PLAIN, anomalyChart, anomalyChartWindow, anomalyDurationNs,
  anomalySignalHrefs, behaviorDetailsOf, findAnomalyEventInCache, hourOfWeekLabel, isLogAnomalyKind,
  isTraceAnomalyKind, parseBehaviorDetails, sampleTraceHref,
} from './anomalyDetail';

const MIN = 60 * 1e9;
// Dakikaya hizalı bir an (1_700_000_040 s / 60 tam sayı) — pencere uçları
// dakikaya yuvarlandığı için beklenen değerler okunur kalsın.
const T0 = 1_700_000_040e9;

const ev = (over: Partial<AnomalyEvent> = {}): AnomalyEvent => ({
  id: 'a1', kind: 'log_pattern', pattern: 'ORA-00060', service: 'checkout',
  startedAt: T0, lastSeen: T0 + 20 * MIN, peakRatio: 4.2, currentRatio: 3, currentCount: 12,
  sample: 'deadlock detected', status: 'active', ...over,
});

const details = (over: Partial<BehaviorChangeDetails> = {}): BehaviorChangeDetails => ({
  metric: 'p99_ms', signal: 'regime', direction: 'up', ratio: 2.4, z: 6.1, baseline: 120, current: 290,
  unit: 'ms', hourOfWeek: 33, dwell: 4, onsetNs: T0, ...over,
});

describe('anomalyChartWindow — v0.8.267 çekmece formülü + v0.10.1032 sınırları', () => {
  it('kısa spike: en az 30 dk giriş, 10 dk kuyruk', () => {
    expect(anomalyChartWindow(ev({ lastSeen: T0 + 5 * MIN }))).toEqual({
      fromNs: T0 - 30 * MIN, toNs: T0 + 15 * MIN,
    });
  });
  it('uzun spike: süresinin 3 katı giriş', () => {
    expect(anomalyChartWindow(ev({ lastSeen: T0 + 60 * MIN }))).toEqual({
      fromNs: T0 - 180 * MIN, toNs: T0 + 70 * MIN,
    });
  });
  it('giriş en çok 6 sa (günlerce süren olay haftalarca geri sorgu atmaz)', () => {
    const w = anomalyChartWindow(ev({ lastSeen: T0 + 3 * 24 * 60 * MIN }));
    expect(w.fromNs).toBe(T0 - 6 * 60 * MIN);
    expect(w.toNs).toBe(T0 + 3 * 24 * 60 * MIN + 10 * MIN);
  });
  it('uçlar dakikaya yuvarlanır, pencere asla DARALMAZ (başlangıç aşağı, bitiş yukarı)', () => {
    const w = anomalyChartWindow(ev({ startedAt: T0 + 17e9, lastSeen: T0 + 5 * MIN + 1e9 }));
    expect(w.fromNs).toBe(T0 - 30 * MIN); // T0+17s−30dk → aşağı
    expect(w.toNs).toBe(T0 + 16 * MIN);   // T0+15dk+1s → yukarı
    expect(w.fromNs % MIN).toBe(0);
    expect(w.toNs % MIN).toBe(0);
  });
  it('açık olayda lastSeen dakika içinde kayarsa pencere (sorgu anahtarı) DEĞİŞMEZ', () => {
    const a = anomalyChartWindow(ev({ lastSeen: T0 + 5 * MIN + 5e9 }));
    const b = anomalyChartWindow(ev({ lastSeen: T0 + 5 * MIN + 40e9 }));
    expect(a).toEqual(b);
  });
  it('bozuk sıra (lastSeen < startedAt) pencereyi ters çevirmez', () => {
    const w = anomalyChartWindow(ev({ lastSeen: T0 - 5 * MIN }));
    expect(w).toEqual({ fromNs: T0 - 30 * MIN, toNs: T0 + 10 * MIN });
    expect(w.toNs).toBeGreaterThan(w.fromNs);
    expect(anomalyDurationNs(ev({ lastSeen: T0 - 5 * MIN }))).toBe(0);
  });
});

describe('parseBehaviorDetails / behaviorDetailsOf — korumalı ayrıştırma', () => {
  it('geçerli kanıt', () => {
    const d = details();
    expect(parseBehaviorDetails(JSON.stringify(d))).toEqual(d);
    expect(behaviorDetailsOf({ kind: 'behavior_change', sample: JSON.stringify(d) })).toEqual(d);
  });
  it.each([
    ['bozuk JSON', '{"metric": "p99_ms", "ratio": '],
    ['düz metin', 'deadlock detected'],
    ['null', 'null'],
    ['metrik yok', JSON.stringify({ ratio: 2 })],
    ['oran sayı değil', JSON.stringify({ metric: 'p99_ms', ratio: '2' })],
    ['boş', ''],
  ])('%s → null (çağıran ham örneğe düşer, sayfa patlamaz)', (_n, s) => {
    expect(parseBehaviorDetails(s)).toBeNull();
  });
  it('davranış dışı tür ya da boş örnek ayrıştırılmaz', () => {
    expect(behaviorDetailsOf({ kind: 'log_pattern', sample: JSON.stringify(details()) })).toBeNull();
    expect(behaviorDetailsOf({ kind: 'behavior_change', sample: '' })).toBeNull();
  });
});

describe('hourOfWeekLabel — UTC kova etiketi', () => {
  it.each([[0, 'Pzt 00:00 UTC'], [33, 'Sal 09:00 UTC'], [167, 'Paz 23:00 UTC'], [-1, '—'], [168, '—'], [Number.NaN, '—']])(
    '%s → %s', (how, want) => { expect(hourOfWeekLabel(how)).toBe(want); });
});

describe('tür sınıfları ve etiketler', () => {
  it('log / trace türleri', () => {
    expect(['log_pattern', 'log_template_new', 'elastic_ml'].every(isLogAnomalyKind)).toBe(true);
    expect(['trace_op', 'trace_op_latency', 'behavior_change'].some(isLogAnomalyKind)).toBe(false);
    expect(['trace_op', 'trace_op_latency'].every(isTraceAnomalyKind)).toBe(true);
    expect(isTraceAnomalyKind('behavior_change')).toBe(false);
  });
  it('her türün etiketi var', () => {
    for (const k of ['log_pattern', 'trace_op', 'trace_op_latency', 'elastic_ml', 'log_template_new', 'behavior_change'] as const) {
      expect(ANOMALY_KIND_LABEL[k]).toBeTruthy();
    }
  });
  it('tam sayfa rozeti düz Türkçe (v0.10.1032 inceleme)', () => {
    expect(ANOMALY_KIND_PLAIN).toEqual({
      log_pattern: 'Log deseni', log_template_new: 'Yeni log biçimi', trace_op: 'Operasyon hatası',
      trace_op_latency: 'Operasyon gecikmesi', behavior_change: 'Davranış değişimi', elastic_ml: 'Elastic ML',
    });
  });
});

describe('anomalySignalHrefs — üreticilerle, olay penceresiyle', () => {
  const win = { fromNs: T0 - 30 * MIN, toNs: T0 + 30 * MIN };
  it('servis yok → null (boş açılan pivot "olay yok" diye okunur)', () => {
    expect(anomalySignalHrefs(ev({ service: '' }), win)).toBeNull();
  });
  it('log / trace / servis linkleri pencereyi ve servisi taşır', () => {
    const h = anomalySignalHrefs(ev(), win)!;
    expect(h.logs.startsWith('/logs?')).toBe(true);
    expect(h.logs).toContain('service=checkout');
    expect(h.logs).toContain('range=custom');
    expect(h.errorTraces.startsWith('/traces?')).toBe(true);
    expect(h.errorTraces).toContain('hasError=true');
    expect(h.errorTraces).toContain('rootOnly=false');
    expect(h.servicePage.startsWith('/service?name=checkout')).toBe(true);
    expect(h.servicePage).toContain('range=custom');
    expect(h.operationTraces).toBeNull();
  });
  it('trace türlerinde operasyonun trace linki (desen = span adı)', () => {
    const h = anomalySignalHrefs(ev({ kind: 'trace_op', pattern: 'POST /pay' }), win)!;
    expect(h.operationTraces).not.toBeNull();
    expect(h.operationTraces!.startsWith('/traces?')).toBe(true);
    expect(anomalySignalHrefs(ev({ kind: 'trace_op_latency', pattern: '' }), win)!.operationTraces).toBeNull();
  });
  it('trace_op (hata anomalisi) operasyon linki hasError taşır; gecikme türü taşımaz', () => {
    const err = new URLSearchParams(anomalySignalHrefs(ev({ kind: 'trace_op', pattern: 'POST /pay' }), win)!
      .operationTraces!.split('?')[1]);
    expect(err.get('hasError')).toBe('true');
    const lat = new URLSearchParams(anomalySignalHrefs(ev({ kind: 'trace_op_latency', pattern: 'GET /x' }), win)!
      .operationTraces!.split('?')[1]);
    expect(lat.has('hasError')).toBe(false);
  });
});

describe('sampleTraceHref — trace türünde örnek bir trace kimliği', () => {
  it.each<[string, Partial<AnomalyEvent>, string | null]>([
    ['32 hex', { kind: 'trace_op', sample: '4bf92f3577b34da6a3ce929d0e0e4736' }, '/trace?id=4bf92f3577b34da6a3ce929d0e0e4736'],
    ['16 hex', { kind: 'trace_op_latency', sample: 'a3ce929d0e0e4736' }, '/trace?id=a3ce929d0e0e4736'],
    ['boşluklu', { kind: 'trace_op', sample: ' a3ce929d0e0e4736 \n' }, '/trace?id=a3ce929d0e0e4736'],
    ['kimlik değil', { kind: 'trace_op', sample: 'timeout after 30s' }, null],
    ['boş', { kind: 'trace_op', sample: '' }, null],
    ['log türü', { kind: 'log_pattern', sample: 'a3ce929d0e0e4736' }, null],
  ])('%s', (_n, over, want) => {
    const got = sampleTraceHref(ev(over));
    if (want === null) expect(got).toBeNull();
    else expect(got?.startsWith(want)).toBe(true);
  });
});

describe('anomalyChart — türün TEK grafiği, dedektörün KENDİ metriği, düz başlık', () => {
  const win = { fromNs: 1, toNs: 2 };
  it('trace_op → operasyonun hata SAYISI (oran değil: dedektör sayıyı kıyaslar)', () => {
    expect(anomalyChart(ev({ kind: 'trace_op', pattern: 'POST /pay' }), win, null)).toEqual({
      spec: { service: 'checkout', operation: 'POST /pay', agg: 'errors', fromNs: 1, toNs: 2 },
      title: 'POST /pay · hata sayısı', seriesName: 'hata sayısı',
    });
  });
  it('trace_op_latency → operasyonun p99\'u', () => {
    expect(anomalyChart(ev({ kind: 'trace_op_latency', pattern: 'GET /x' }), win, null)).toEqual({
      spec: { service: 'checkout', operation: 'GET /x', agg: 'p99', fromNs: 1, toNs: 2 },
      title: 'GET /x · p99 gecikme', seriesName: 'p99 gecikme',
    });
  });
  it.each<[string, CosreChartSpec['agg'] | null, string]>([
    ['error_rate', 'error_rate', 'hata oranı'], ['p99_ms', 'p99', 'p99 gecikme'], ['request_rate', 'rate', 'istek hızı'],
    ['ext:oracle_sessions', null, ''], ['', null, ''],
  ])('behavior_change %s → %s (servis geneli)', (metric, agg, label) => {
    const got = anomalyChart(ev({ kind: 'behavior_change' }), win, details({ metric }));
    expect(got).toEqual(agg
      ? { spec: { service: 'checkout', agg, fromNs: 1, toNs: 2 }, title: `checkout · ${label}`, seriesName: label }
      : null);
  });
  it('başlıkta ham metrik kimliği yok', () => {
    for (const e of [ev({ kind: 'trace_op', pattern: 'POST /pay' }), ev({ kind: 'trace_op_latency', pattern: 'GET /x' })]) {
      expect(anomalyChart(e, win, null)!.title).not.toMatch(/errors|error_rate|p99_ms|request_rate|\brate\b/);
    }
  });
  it('kanıtı ayrıştırılamayan davranış / log türleri / servissiz / desensiz trace → grafik yok', () => {
    expect(anomalyChart(ev({ kind: 'behavior_change' }), win, null)).toBeNull();
    for (const kind of ['log_pattern', 'log_template_new', 'elastic_ml'] as const) {
      expect(anomalyChart(ev({ kind }), win, null)).toBeNull();
    }
    expect(anomalyChart(ev({ kind: 'trace_op', service: '' }), win, null)).toBeNull();
    expect(anomalyChart(ev({ kind: 'trace_op', pattern: '' }), win, null)).toBeNull();
  });
  it('boş grafik notu bu sayfa için yazılmış (sohbetin çit dili değil)', () => {
    expect(ANOMALY_CHART_EMPTY).toBe('Bu pencere için seri yok — olay saklama süresinin dışında olabilir.');
    expect(ANOMALY_CHART_EMPTY).not.toMatch(/çit|doğrula/);
  });
});

describe('findAnomalyEventInCache — host\'un bedava ilk basamağı', () => {
  it('liste önbelleğinde id ile bulur; yoksa / boşsa undefined', () => {
    const a = ev({ id: 'a1' }), b = ev({ id: 'b2' });
    expect(findAnomalyEventInCache({ items: [a, b] }, 'b2')).toBe(b);
    expect(findAnomalyEventInCache({ items: [a] }, 'zz')).toBeUndefined();
    expect(findAnomalyEventInCache(undefined, 'a1')).toBeUndefined();
    expect(findAnomalyEventInCache({ items: null }, 'a1')).toBeUndefined();
    expect(findAnomalyEventInCache({ items: [a] }, '')).toBeUndefined();
  });
});
