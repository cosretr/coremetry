// traceHealth.test.ts — v0.10.757 panel saf yardımcıları + kablolama pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { bucketBars, fleetVerdict, lossVerdict, nameTone, pctOf, podRSHash, rawHostLabel, sortRawHosts, stalePods, staleVerdict } from './traceHealth';

describe('traceHealth — saf', () => {
  // v0.10.929 (K5) — temiz hâl sağlıklı, geçiş değil: b-ok → b-gray.
  it('lossVerdict: kalıcı kayıp err, kalite işareti warn, temiz nötr', () => {
    expect(lossVerdict({ accepted: 10, dropped: 0, writeFailed: 0 })).toEqual({ tone: 'b-gray', text: 'kayıp yok' });
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
    expect(nameTone(null)).toBe('b-gray'); // v0.10.929 (K5)
    expect(nameTone(4.9)).toBe('b-gray');
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
    // v0.10.929 (K5) — ≥%99.5 saklandı sağlıklı hâl: nötr.
    expect(fleetVerdict(base)).toEqual({ tone: 'b-gray', text: '%99.8 saklandı', pct: 99.8 });
    expect(fleetVerdict({ ...base, storedSettled: 980 }).tone).toBe('b-warn');
    expect(fleetVerdict({ ...base, storedSettled: 900 }).tone).toBe('b-err');
    expect(fleetVerdict({ ...base, storedSettled: 1012 })).toEqual({ tone: 'b-gray', text: '%101 saklandı', pct: 101.2 });
    // v0.10.770 — prod: defter 15 dk'lık, pencere 6 saat → %7495 yeşil çizilmişti.
    expect(fleetVerdict({ ...base, storedSettled: 74950 })).toEqual({ tone: 'b-warn', text: '%7495 saklandı (kapsam?)', pct: 7495 });
    expect(fleetVerdict({ ...base, coveredFrom: 1, settledTo: 1 }).text).toBe('defter henüz yerleşmedi');
    expect(fleetVerdict({ ...base, settledTo: 10, coveredFrom: 5, accepted: 1, acceptedSettled: 1000 }).tone).toBe('b-gray');
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

// v0.10.823 — isteğe bağlı HAM sayım (spans, host başına). MV sayısı
// varsayılan kalır; kutucuk işaretlenince aynı pencere raw=1 ile çekilir.
describe('traceHealth — ham sayım kablolaması', () => {
  const page = readFileSync(resolve(__dirname, '../AdminClickhouse.tsx'), 'utf8');
  const api = readFileSync(resolve(__dirname, '../../lib/api.ts'), 'utf8');
  const types = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
  it('kutucuk + raw sorgu anahtarı + istemci ucu + tip', () => {
    expect(page).toContain('Ham sayım (spans, host başına)');
    expect(page).toContain("queryKey: ['ch-trace-health', armed, raw]");
    expect(page).toContain('api.chTraceHealth(armed ?? 3600, raw, signal)');
    expect(page).toContain('Ham spans (Distributed)');
    expect(api).toContain("${raw ? '&raw=1' : ''}");
    expect(types).toContain('byHost: { host: string; shard: number; count: number }[]');
    expect(types).toContain('byShard: { shard: number; hosts: number; min: number; max: number; spreadPct: number }[]');
  });
  // İnceleme #2 — şard bilinci: kıyas AYNI shard içinde; MV/ham cümlesi
  // replikalar uyumsuzken KURULMAZ (iki bağımsız rastgele-replika okuması).
  it('şard bilincli metin + koşullu MV/ham cümlesi + kısmi hata satırı', () => {
    expect(page).toContain("AYNI shard'ın host'ları birbirinden farklıysa o shard'ın replikaları ayrışmış");
    expect(page).toContain("farklı olması normaldir (shard anahtarı)");
    expect(page).toContain("Replikalar uyumsuzken MV/ham kıyası anlamsız — önce replikaları onar.");
    expect(page).toContain("'MV sayımı ham sayımdan küçükse MV/kaskad kaybı.'");
    expect(page).toContain('sortRawHosts(data.raw.byHost).map(h => kv(rawHostLabel(h)');
    expect(page).toContain('host başına sayım okunamadı');
  });
  it('rawHostLabel / sortRawHosts: shard etiketi, eşlenemeyen sona', () => {
    expect(rawHostLabel({ host: 'ch-01', shard: 1, count: 9 })).toBe('ham · shard 1 · ch-01');
    expect(rawHostLabel({ host: 'ch-05', shard: -1, count: 9 })).toBe('ham · shard ? · ch-05');
    const hs = [
      { host: 'ch-05', shard: -1, count: 1 }, { host: 'ch-03', shard: 2, count: 2 },
      { host: 'ch-02', shard: 1, count: 3 }, { host: 'ch-01', shard: 1, count: 4 },
    ];
    expect(sortRawHosts(hs).map(h => h.host)).toEqual(['ch-01', 'ch-02', 'ch-03', 'ch-05']);
    expect(hs[0].host).toBe('ch-05'); // girdi kopyalanır, yerinde sıralanmaz
  });
  it('varsayılan (işaretsiz) istek eski URL ile birebir aynı', () => {
    const build = (rangeS: number, raw?: boolean) => `/api/admin/clickhouse/trace-health?range_s=${rangeS}${raw ? '&raw=1' : ''}`;
    expect(build(3600)).toBe('/api/admin/clickhouse/trace-health?range_s=3600');
    expect(build(3600, false)).toBe('/api/admin/clickhouse/trace-health?range_s=3600');
    expect(build(900, true)).toBe('/api/admin/clickhouse/trace-health?range_s=900&raw=1');
  });
});
