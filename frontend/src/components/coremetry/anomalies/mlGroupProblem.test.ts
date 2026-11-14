// v0.10.2053 — anomali tekilleştirme D4: ML grubu Problem açar. Grup
// detayında "Problem açtı" çipi + bağlantı, gölgede "açardı"; Problem
// detayından gruba geri bağlantı; ML sekmesinin tik özeti. Servisler sentetik.
import { describe, it, expect } from 'vitest';
import type { MLGroupDecision, MLSvcShadow, MLTickStatus } from '@/lib/types';
import { groupGateLine, groupProblemChip, latestGroupDecision, svcGateLine } from './mlGroupDetail';
import { groupTickText } from './MLTab';
import { mlGroupProblemHref } from '@/lib/anomalyHref';

const dec = (o: Partial<MLGroupDecision>): MLGroupDecision => ({
  group: 'mlg-1', wouldOpen: true, rule: 'anomaly-auto:group:ev1', priority: 'P2', reason: 'açardı: 2 farklı sinyal teyit etti (log, trace)', detectors: [], at: 10, ...o,
});

describe('grup kararı çipi', () => {
  it('canlı → "Problem açtı · P2" + Problem bağlantısı', () => {
    expect(groupProblemChip(dec({ live: true, problemId: 'anomaly-auto:group:ev1:orders-api' })))
      .toEqual({ label: 'Problem açtı · P2', href: '/inbox?problem=anomaly-auto%3Agroup%3Aev1%3Aorders-api' });
  });
  it('gölge (anahtar kapalı) → bağlantısız "açardı"; açmaz → çip yok', () => {
    expect(groupProblemChip(dec({}))).toEqual({ label: 'Problem açardı (canlı kip kapalı)' });
    expect(groupProblemChip(dec({ wouldOpen: false, priority: undefined }))).toBeNull();
    expect(groupProblemChip(undefined)).toBeNull();
  });
  it('v0.10.2092 tek sinyal canlı: "Problem açtı" çipi, gölge çipi yok', () => {
    const d = dec({ wouldOpen: true, priority: 'P2', singleWouldOpen: true, live: true, problemId: 'p1', reason: 'açardı: tek güçlü sinyal (trace) — op_latency 30 dk sürdü, normalin 5.0 katı (kötüleşme)' });
    expect(groupProblemChip(d)?.label).toBe('Problem açtı · P2');
    expect(groupGateLine(d).startsWith('Problem açtı (tek sinyal) · P2 — ')).toBe(true);
    expect(groupProblemChip({ ...d, live: false, problemId: undefined })).toEqual({ label: 'Problem açardı (canlı kip kapalı)' });
  });
  it('teknik satır ve en son karar', () => {
    expect(groupGateLine(dec({ live: true, problemId: 'p' }))).toBe('Problem açtı · P2 — açardı: 2 farklı sinyal teyit etti (log, trace)');
    expect(groupGateLine(dec({ wouldOpen: false, priority: undefined, reason: 'açmazdı: tek sinyal (trace)' }))).toBe('açmazdı — açmazdı: tek sinyal (trace)');
    expect(groupGateLine(undefined)).toBe('karar yok');
    const latest = latestGroupDecision([{ groupShadow: dec({ at: 1 }) }, {}, { groupShadow: dec({ at: 5, live: true }) }]);
    expect(latest?.at).toBe(5);
  });
});

describe('Problem → grup bağlantısı', () => {
  it('yalnız anomaly-auto:group: kuralı', () => {
    expect(mlGroupProblemHref('anomaly-auto:group:ev1')).toBe('/anomalies/ml/ev1');
    expect(mlGroupProblemHref('anomaly-auto:abcdef0123456789')).toBeNull();
    expect(mlGroupProblemHref('anomaly-auto:group:')).toBeNull();
    expect(mlGroupProblemHref(undefined)).toBeNull();
  });
});

describe('ML tik özeti', () => {
  const base: MLTickStatus = { at: 1, durationMs: 1, events: 0, backoff: [] };
  it('canlı kip sayıları; kapalıyken gölge', () => {
    expect(groupTickText({ ...base, groupLive: true, groupOpened: 1, groupRefreshed: 2, shadowGroups: 4 }))
      .toBe("grup Problem'i (canlı): 1 açıldı, 2 sürüyor, 0 kapandı · 4 grup değerlendirildi");
    expect(groupTickText({ ...base, shadowGroups: 4, shadowWouldOpen: 1 })).toBe('gölge terfi: 4 grup, 1 Problem açardı (açılmadı)');
    expect(groupTickText(base)).toBe('');
  });
  it('v0.10.2092 tek sinyal sayacı: canlıda açılan, kapalıyken açardı', () => {
    expect(groupTickText({ ...base, shadowGroups: 4, shadowWouldOpen: 1, shadowSingleWouldOpen: 2 }))
      .toBe('gölge terfi: 4 grup, 1 Problem açardı (açılmadı) · tek sinyal: 2 Problem açardı');
    expect(groupTickText({ ...base, groupLive: true, groupOpened: 1, shadowGroups: 3, shadowSingleWouldOpen: 1, groupSingleOpened: 1 }))
      .toBe("grup Problem'i (canlı): 1 açıldı, 0 sürüyor, 0 kapandı · 3 grup değerlendirildi · tek sinyal: 1 Problem açtı");
  });
});

// v0.10.2130 — servis düzeyi ML Problem'i canlı (spec-ml-service-level dilim 4).
describe('servis düzeyi kapı canlı', () => {
  const base: MLTickStatus = { at: 1, durationMs: 1, events: 0, backoff: [] };
  const svc = (o: Partial<MLSvcShadow>): MLSvcShadow => ({
    wouldOpen: true, owner: true, rule: 'anomaly-auto:svc:orders-api', priority: 'P1', value: 0.03, threshold: 0.01, reason: 'r', at: 1, ...o,
  });
  it('kapı satırı: canlı açtı / canlı üye / gölge', () => {
    expect(svcGateLine(svc({ live: true, problemId: 'anomaly-auto:svc:orders-api' }))).toBe('Problem açtı · P1 — r');
    expect(svcGateLine(svc({ live: true, problemId: 'anomaly-auto:group:ev1:orders-api' }))).toBe("servisin ML Problem'ine üye · P1 — r");
    expect(svcGateLine(svc({}))).toBe('Problem açardı (gölge) · P1 — r');
  });
  it('tik özeti: canlıda açılan sayısı, gölge sayacı yalnız kapalıyken', () => {
    expect(groupTickText({ ...base, groupLive: true, groupOpened: 1, svcOpened: 1, shadowSvcWouldOpen: 1 }))
      .toBe("grup Problem'i (canlı): 1 açıldı, 0 sürüyor, 0 kapandı · servis düzeyi: 1 Problem açtı");
    expect(groupTickText({ ...base, shadowGroups: 1, shadowSvcWouldOpen: 2, shadowSvcP1: 1 }))
      .toBe('gölge terfi: 1 grup, 0 Problem açardı (açılmadı) · servis düzeyi: 2 Problem açardı (gölge, 1 P1)');
  });
});
