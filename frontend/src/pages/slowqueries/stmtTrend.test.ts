import { describe, it, expect } from 'vitest';
import {
  stmtTrendGrid, stmtTrendSeries, stmtBucketBounds, stmtBucketIndex,
  stmtBucketCoveredSec, stmtBucketLabel,
} from './stmtTrend';

// stmtTrend.test.ts — v0.10.1058. Operatör: "Statement detail grafikleri
// ... zaman yok vs., hiç olmamış." Eski densifyTrend kovaların ZAMANINI
// atıp yalnız indeks tutuyordu; grafik "bucket 17/37" diyordu. Burada
// pinlenen: her kova sunucunun başlangıç damgasını taşır, kova sınırları
// YARI-AÇIK, oran böleni kovanın MV'de kapsanan süresi, ölçülmemiş P95
// boşluk (NaN) — sıfır değil.

const T0 = Date.UTC(2026, 9, 2, 8, 0, 0) / 1000; // 5 dk hizalı, unix sn
const P = (tsSec: number, calls: number, extra: Partial<{ errors: number; p95Ms: number }> = {}) => ({
  tsNs: tsSec * 1e9, calls, errors: extra.errors ?? 0, avgMs: 3, p95Ms: extra.p95Ms ?? 20,
});

describe('stmtBucketBounds — yarı-açık [başlangıç, bitiş)', () => {
  it.each([
    [0, [T0, T0 + 300]],
    [1, [T0 + 300, T0 + 600]],
    [36, [T0 + 36 * 300, T0 + 37 * 300]],
  ] as const)('kova %i', (i, want) => {
    expect(stmtBucketBounds(T0, 300, i)).toEqual(want);
  });
});

describe('stmtBucketIndex — sınır noktası hangi kovaya düşer', () => {
  const n = 12;
  it.each([
    ['ızgara başı', T0 * 1e9, 0],
    ['ilk kova sonundan 1 ms önce', (T0 + 300) * 1e9 - 1e6, 0],
    ['ilk kova sonu = ikinci kovanın başı', (T0 + 300) * 1e9, 1],
    ['kova ortası', (T0 + 450) * 1e9, 1],
    ['son kova sonundan 1 ms önce', (T0 + n * 300) * 1e9 - 1e6, n - 1],
    ['son kova sonu = ızgara dışı', (T0 + n * 300) * 1e9, -1],
    ['ızgara başından 1 ms önce', T0 * 1e9 - 1e6, -1],
  ] as const)('%s', (_name, tsNs, want) => {
    expect(stmtBucketIndex(tsNs, T0, 300, n)).toBe(want);
  });

  it('kaba kova (900 s): 5 dk taneli noktalar doğru kovada toplanır', () => {
    expect(stmtBucketIndex((T0 + 600) * 1e9, T0, 900, 4)).toBe(0);
    expect(stmtBucketIndex((T0 + 900) * 1e9, T0, 900, 4)).toBe(1);
  });

  it('bozuk genişlik → -1', () => {
    expect(stmtBucketIndex(T0 * 1e9, T0, 0, 4)).toBe(-1);
  });
});

describe('stmtBucketCoveredSec — oran böleni', () => {
  it.each([
    ['tam kova', T0, 300, T0 + 3600, 300],
    ['kaba kova pencere içinde', T0, 900, T0 + 3600, 900],
    // to kovanın 2. MV dilimine düşüyor → 2 MV kovası (time_bucket < to).
    ['kaba kova pencere sonunu aşıyor', T0, 900, T0 + 450, 600],
    // to tam MV sınırında: o sınırda başlayan MV kovası DAHİL DEĞİL.
    ['to tam MV sınırında', T0, 900, T0 + 600, 600],
    ['5 dk kova, to kovanın ortasında (MV kovası bütün)', T0, 300, T0 + 150, 300],
    ['kova to’dan sonra başlıyor', T0 + 900, 300, T0 + 600, 0],
  ] as const)('%s', (_n, s, b, to, want) => {
    expect(stmtBucketCoveredSec(s, b, to)).toBe(want);
  });
});

describe('stmtTrendGrid', () => {
  it('seyrek noktaları zaman damgalı yoğun ızgaraya açar', () => {
    // 08:03:27 → 09:00: başlangıç 08:00'a iner, 12 kova.
    const fromNs = (T0 + 207) * 1e9;
    const toNs = (T0 + 3600) * 1e9;
    const g = stmtTrendGrid([P(T0, 5, { errors: 1 }), P(T0 + 600, 3, { p95Ms: 25 })], fromNs, toNs, 300)!;
    expect(g.startSec).toBe(T0);
    expect(g.tsNs).toHaveLength(12);
    // x değerleri ZAMAN, indeks değil.
    expect(g.tsNs[0]).toBe(T0 * 1e9);
    expect(g.tsNs[11]).toBe((T0 + 11 * 300) * 1e9);
    expect(g.calls.slice(0, 3)).toEqual([5, 0, 3]);
    expect(g.errors[0]).toBe(1);
    expect(g.p95Ms[2]).toBe(25);
    // Çağrısız kova: P95 ölçülmedi → NaN (grafikte boşluk), çağrı 0.
    expect(Number.isNaN(g.p95Ms[1])).toBe(true);
  });

  it('pencere dışı noktalar yazılmaz', () => {
    const g = stmtTrendGrid([P(T0 - 300, 9), P(T0 + 9000, 9)], T0 * 1e9, (T0 + 600) * 1e9, 300)!;
    expect(g.calls.every(v => v === 0)).toBe(true);
  });

  it('bozuk girdiler → null', () => {
    expect(stmtTrendGrid([], 0, 0, 300)).toBeNull();
    expect(stmtTrendGrid([P(0, 1)], 5e9, 4e9, 300)).toBeNull();
    expect(stmtTrendGrid([P(0, 1)], 0, 5e9, 0)).toBeNull();
  });

  it('ızgara 400 kovada kesilir (sunucu LIMIT aynası)', () => {
    expect(stmtTrendGrid([P(0, 1)], 0, 90 * 24 * 3600 * 1e9, 300)!.tsNs).toHaveLength(400);
  });
});

describe('stmtTrendSeries — grafik serileri', () => {
  it('çağrı/sn, kova başına hata, ms P95; x = kova başlangıcı (ns)', () => {
    const toNs = (T0 + 900) * 1e9;
    const g = stmtTrendGrid([P(T0, 600, { errors: 2, p95Ms: 12 }), P(T0 + 600, 300)], T0 * 1e9, toNs, 300)!;
    const s = stmtTrendSeries(g, toNs);
    expect(s.callsPerSec[0].points.map(p => p.time)).toEqual([T0, T0 + 300, T0 + 600].map(t => t * 1e9));
    expect(s.callsPerSec[0].points.map(p => p.value)).toEqual([2, 0, 1]);
    expect(s.errors[0].points.map(p => p.value)).toEqual([2, 0, 0]);
    expect(s.p95Ms[0].points[0].value).toBe(12);
    expect(Number.isNaN(s.p95Ms[0].points[1].value)).toBe(true);
    expect(s.xRange).toEqual({ from: T0, to: T0 + 900 });
    expect(s.hasCalls).toBe(true);
  });

  it('son kaba kova kısmi: oran kapsanan süreye bölünür', () => {
    // 900 s kovalar, pencere 08:00 → 08:20: ikinci kova [08:15, 08:30)
    // yalnız 08:15 MV kovasını kapsar → bölen 300, 900 değil.
    const toNs = (T0 + 1200) * 1e9;
    const g = stmtTrendGrid([P(T0, 900), P(T0 + 900, 300)], T0 * 1e9, toNs, 900)!;
    expect(stmtTrendSeries(g, toNs).callsPerSec[0].points.map(p => p.value)).toEqual([1, 1]);
  });

  it('çağrısız pencere → hasCalls false', () => {
    const g = stmtTrendGrid([], T0 * 1e9, (T0 + 600) * 1e9, 300)!;
    expect(stmtTrendSeries(g, (T0 + 600) * 1e9).hasCalls).toBe(false);
  });
});

describe('stmtBucketLabel', () => {
  it.each([[300, '5m'], [2100, '35m'], [7200, '2h'], [90, '90s'], [0, '']] as const)('%i → %s', (s, want) => {
    expect(stmtBucketLabel(s)).toBe(want);
  });
});
