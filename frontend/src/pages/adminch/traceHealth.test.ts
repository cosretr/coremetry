// traceHealth.test.ts — v0.10.757 panel saf yardımcıları + kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { bucketBars, fleetVerdict, lossVerdict, nameTone, pctOf, podRSHash, stalePods, staleVerdict } from './traceHealth';

describe('traceHealth — saf', () => {
  it('lossVerdict: kalıcı kayıp err, kalite işareti warn, temiz ok', () => {
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0 })).toEqual({ tone: 'b-ok', text: 'kayıp yok' });
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0, rejects: { span_empty_id: 3 } }).tone).toBe('b-warn');
    expect(lossVerdict({ accepted: 10, dropped: 2, writeFailed: 0 }).tone).toBe('b-err');
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0, rejects: { http_decode_failed: 1, span_empty_id: 9 } }).text).toBe('1 kayıp');
    // v0.10.760 — spool tıkalıysa sayaçlar sıfır olsa da kırmızı; api-rolü pod "kayıp yok" demez.
    expect(lossVerdict({ accepted: 0, dropped: 0, writeFailed: 0 }, true)).toEqual({ tone: 'b-err', text: 'spool tıkalı' });
    expect(lossVerdict({ accepted: 0, dropped: 0, writeFailed: 0 }, false, false)).toEqual({ tone: 'b-gray', text: 'bu pod ingest değil' });
    expect(lossVerdict({ accepted: 0, dropped: 3, writeFailed: 0 }, false, false).tone).toBe('b-gray');
  });
  it('pctOf / nameTone / bucketBars', () => {
    expect(pctOf(1, 0)).toBeNull();
    expect(pctOf(25, 100)).toBe(25);
    expect(nameTone(null)).toBe('b-ok');
    expect(nameTone(4.9)).toBe('b-ok');
    expect(nameTone(5)).toBe('b-warn');
    expect(nameTone(20)).toBe('b-err');
    expect(bucketBars([])).toEqual([]);
    expect(bucketBars([{ t: 1, spans: 0 }, { t: 2, spans: 50 }, { t: 3, spans: 100 }]).map(b => b.h)).toEqual([2, 50, 100]);
    expect(bucketBars([{ t: 1, spans: 0 }])[0].h).toBe(0);
  });
});

describe('traceHealth — kablolama', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  it('panel kök kapsamasının yanında, istek isteğe bağlı, istemci ucu var', () => {
    expect(page).toContain('<TraceHealthPanel />');
    expect(page.indexOf('<RootCoveragePanel />')).toBeLessThan(page.indexOf('<TraceHealthPanel />'));
    expect(page).toContain("queryKey: ['ch-trace-health'");
    expect(page).toContain('lossVerdict(data.pod, data.spoolDegraded, data.pod.ingestRole)');
    expect(api).toContain('/api/admin/clickhouse/trace-health?range_s=');
  });
});

// v0.10.767 (Faz B) — filo mutabakatı.
describe('traceHealth — filo', () => {
  const base = { accepted: 1000, storedSettled: 998, storedKnown: true, empty: false, settledFrom: 0, settledTo: 1 };
  it('fleetVerdict: eşikler, %100 üstü olduğu gibi, gri durumlar', () => {
    expect(fleetVerdict(base)).toEqual({ tone: 'b-ok', text: '%99.8 saklandı', pct: 99.8 });
    expect(fleetVerdict({ ...base, storedSettled: 980 }).tone).toBe('b-warn');
    expect(fleetVerdict({ ...base, storedSettled: 900 }).tone).toBe('b-err');
    expect(fleetVerdict({ ...base, storedSettled: 1012 })).toEqual({ tone: 'b-ok', text: '%101 saklandı', pct: 101.2 });
    // v0.10.770 — prod: defter 15 dk'lık, pencere 6 saat → %7495 yeşil çizilmişti.
    expect(fleetVerdict({ ...base, storedSettled: 74950 })).toEqual({ tone: 'b-warn', text: '%7495 saklandı (kapsam?)', pct: 7495 });
    expect(fleetVerdict({ ...base, coveredFrom: 1, settledTo: 1 }).text).toBe('defter henüz yerleşmedi');
    expect(fleetVerdict({ ...base, settledTo: 10, coveredFrom: 5, accepted: 1, acceptedSettled: 1000 }).tone).toBe('b-ok');
    expect(fleetVerdict({ ...base, empty: true })).toEqual({ tone: 'b-gray', text: 'defter boş', pct: null });
    expect(fleetVerdict({ ...base, settledTo: 0 }).text).toBe('pencere kısa');
    expect(fleetVerdict({ ...base, storedKnown: false }).text).toBe('saklanan okunamadı');
    expect(fleetVerdict({ ...base, accepted: 0 }).text).toBe('kabul yok');
  });
  it('stalePods: 3 dk eşiği', () => {
    const now = 1_000_000 * 1e9;
    expect(stalePods([{ lastSampleAt: now - 60e9 }, { lastSampleAt: now - 200e9 }, { lastSampleAt: now - 181e9 }], now)).toBe(2);
    expect(stalePods([], now)).toBe(0);
  });
  it('panel filo kartını çizer', () => {
    const src = readFileSync(resolve(__dirname, '..', 'AdminClickhouse.tsx'), 'utf8');
    expect(src).toContain('Filo mutabakatı');
    expect(src).toContain('fleetVerdict(data.fleet)');
    expect(src).toContain('staleVerdict(data.fleet.pods, data.generatedAt)');
  });
});

// v0.10.772 — rollout artığı ile gerçek bayat pod ayrımı.
describe('traceHealth — bayat pod hükmü', () => {
  const now = 1_000_000 * 1e9;
  const fresh = (pod: string) => ({ pod, lastSampleAt: now - 60e9 });
  const old = (pod: string) => ({ pod, lastSampleAt: now - 400e9 });
  it('podRSHash: <deploy>-<rs>-<ek>', () => {
    expect(podRSHash('coremetry-ingest-5f88b4fd-4xsnh')).toBe('5f88b4fd');
    expect(podRSHash('ingest-abc12-x')).toBe('abc12');
    expect(podRSHash('tek')).toBe('tek');
  });
  it('eski ReplicaSet\'in kapanan podları gri "rollout", aynı setten bayat pod kırmızı', () => {
    expect(staleVerdict([fresh('coremetry-ingest-5dbb596498-a'), fresh('coremetry-ingest-5dbb596498-b'), old('coremetry-ingest-5f88b4fd-x'), old('coremetry-ingest-5f88b4fd-y')], now))
      .toEqual({ count: 2, tone: 'b-gray', text: '2 eski pod (rollout)' });
    expect(staleVerdict([fresh('coremetry-ingest-5dbb596498-a'), old('coremetry-ingest-5dbb596498-b')], now))
      .toEqual({ count: 1, tone: 'b-err', text: '1 pod bayat' });
    // Taze pod hiç yoksa rollout iddiası yok: hepsi bayat, kırmızı.
    expect(staleVerdict([old('coremetry-ingest-5f88b4fd-x')], now)?.tone).toBe('b-err');
    expect(staleVerdict([fresh('coremetry-ingest-5f88b4fd-x')], now)).toBeNull();
    expect(stalePods([old('a-b-c')], now)).toBe(1);
  });
});
