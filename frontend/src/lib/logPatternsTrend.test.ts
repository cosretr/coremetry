import { describe, it, expect } from 'vitest';
import { trendCell, trendLabel, trendSortValue, firstSeenBadge, insightsNotes } from './logPatternsTrend';
import type { LogPatternGroup, LogPatternsBaseline } from './types';

const g = (o: Partial<LogPatternGroup>): LogPatternGroup => ({
  hash: 'h', template: 't', count: 10, sample: '', severity: 9, firstSeen: 0, lastSeen: 0, services: [], serviceCount: 0, query: '', ...o,
});
const base: LogPatternsBaseline = { fromNs: 0, toNs: 1, sampled: 500, distinct: 20 };

// v0.10.508 (C6) — "Önceki pencere" hücresi: yeni / oran / yok; taban yoksa ya da bozuksa "—".
// v0.10.1896 — oran ifadesi kuralı + pay normalizasyonu + ayrı "ilk kez görüldü".
describe('logPatternsTrend', () => {
  it('taban yok ya da degraded → none', () => {
    expect(trendCell(g({ new: true }), undefined)).toEqual({ kind: 'none' });
    expect(trendCell(g({ ratio: 3 }), { ...base, degraded: true })).toEqual({ kind: 'none' });
    expect(trendLabel({ kind: 'none' })).toBe('—');
  });
  it('yeni ve oran — oran ifadesi, "×" yok', () => {
    expect(trendCell(g({ new: true }), base)).toEqual({ kind: 'new' });
    expect(trendLabel(trendCell(g({ new: true }), base))).toBe('önceki pencerede yok');
    expect(trendLabel(trendCell(g({ ratio: 3 }), base))).toBe('yaklaşık 3 kat');
    expect(trendLabel(trendCell(g({ ratio: 12.4 }), base))).toBe('yaklaşık 12 kat');
    expect(trendLabel(trendCell(g({ ratio: 0.5 }), base))).toBe('yaklaşık 2 kat az');
    expect(trendCell(g({ ratio: 0.5 }), base)).toMatchObject({ kind: 'ratio', up: false, flat: false });
    expect(trendCell(g({ ratio: 1.05 }), base)).toMatchObject({ kind: 'ratio', flat: true });
  });
  it('pay normalizasyonu: 2000 satırlık pencere vs 500 satırlık taban', () => {
    // ham oran 40/10 = 4 kat; pay: (40/2000) / (10/500) = 1 → normal aralıkta
    const row = g({ count: 40, prevCount: 10, ratio: 4 });
    expect(trendLabel(trendCell(row, base, 2000))).toBe('normal aralıkta');
    expect(trendLabel(trendCell(row, base))).toBe('yaklaşık 4 kat'); // örnek boyu bilinmiyorsa ham
  });
  it('sıralama: yeni > oran > yok', () => {
    expect(trendSortValue(g({ new: true }), base)).toBeGreaterThan(trendSortValue(g({ ratio: 99 }), base));
    expect(trendSortValue(g({}), base)).toBe(0);
  });
  it('ilk kez görüldü ayrı etiket; ölçülemeyen "ilk kez" demez', () => {
    expect(firstSeenBadge(g({ new: true, firstSeen7d: 'first' }))?.text).toBe('ilk kez görüldü');
    expect(firstSeenBadge(g({ new: true, firstSeen7d: 'seen' }))).toBeNull();
    expect(firstSeenBadge(g({ new: true, firstSeen7d: 'unknown' }))?.text).not.toContain('ilk kez görüldü');
    expect(firstSeenBadge(g({ new: true }))).toBeNull();
  });
});

describe('insightsNotes', () => {
  const m = { bucketSec: 300, fromNs: 0, toNs: 1, lookbackDays: 7 } as const;
  it('sorun yoksa not yok; kısmi / hata uyarı; servissiz düz not', () => {
    expect(insightsNotes(undefined)).toEqual([]);
    expect(insightsNotes({ ...m, trend: 'ok', firstSeen: 'ok' })).toEqual([]);
    expect(insightsNotes({ ...m, trend: 'partial', firstSeen: 'no-service' })).toEqual([
      { text: 'eğilim kısmi (zaman aşımı ya da shard hatası)', warn: true },
      { text: 'ilk görülme ölçülmedi: tek servis seçin', warn: false },
    ]);
    expect(insightsNotes({ ...m, trend: 'unsupported', firstSeen: 'unsupported' })).toEqual([{ text: 'eğilim bu log arka ucunda yok', warn: false }]);
  });
});
