// v0.10.2053 — anomali tekilleştirme D4: ML grubu Problem açar. Grup
// detayında "Problem açtı" çipi + bağlantı, gölgede "açardı"; Problem
// detayından gruba geri bağlantı; ML sekmesinin tik özeti. Servisler sentetik.
import { describe, it, expect } from 'vitest';
import type { MLGroupDecision, MLTickStatus } from '@/lib/types';
import { groupGateLine, groupProblemChip, latestGroupDecision } from './mlGroupDetail';
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
});
