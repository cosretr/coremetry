// criticalPath.test.ts — v0.10.944 (CoSRE Faz A): kritik yol ÖZETİ iç içe
// süreleri TOPLAMAZ. Eski `totalNs` zincirdeki span sürelerinin toplamıydı;
// çocuk ebeveyninin süresinin içinde olduğu için aynı duvar saati iki (üç…)
// kez sayılıyordu — 1 s'lik bir trace "2.4 s kritik yol" gösteriyordu.
// Pinlenen sözleşme:
//   - seçim kuralı aynı (Go ikizi chstore/tracetree.go ile): ebeveynin
//     bitişini aşan (fire-and-forget) çocuk zincire girmez;
//   - rootWallNs = kök span'in süresi, spanCount = zincir uzunluğu;
//   - yol-üstü öz süre halka başına; iç içe zincirde TOPLAMI rootWallNs'e
//     eşit (hiçbir an iki kez sayılmaz), asla negatif değil;
//   - sonuçta "toplam" alanı YOK; sayfalar toplam basmıyor (kaynak pini).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { computeCriticalPath, type CriticalPathSpan } from './criticalPath';

const MS = 1e6;
const sp = (spanId: string, parentId: string, startMs: number, durMs: number): CriticalPathSpan =>
  ({ spanId, parentId, startTime: startMs * MS, duration: durMs * MS });

// checkout(1000) → payments(800) → db(600); inventory(100) kardeş;
// audit fire-and-forget (checkout bittikten sonra biter).
const TRACE: CriticalPathSpan[] = [
  sp('checkout', '', 0, 1000),
  sp('payments', 'checkout', 100, 800),
  sp('db', 'payments', 150, 600),
  sp('inventory', 'checkout', 10, 100),
  sp('audit', 'checkout', 900, 500),
];

describe('computeCriticalPath', () => {
  it('zincir kök → yaprak; fire-and-forget çocuk zincire girmez', () => {
    const cp = computeCriticalPath(TRACE);
    expect(cp.order).toEqual(['checkout', 'payments', 'db']);
    expect(cp.ids.has('audit')).toBe(false);
    expect(cp.rootId).toBe('checkout');
    expect(cp.leafId).toBe('db');
    expect(cp.spanCount).toBe(3);
  });

  it('kapsam = kök duvar süresi; iç içe süreler TOPLANMAZ', () => {
    const cp = computeCriticalPath(TRACE);
    expect(cp.rootWallNs).toBe(1000 * MS);
    // Eski davranış 1000+800+600 = 2400 ms basıyordu.
    expect(cp).not.toHaveProperty('totalNs');
  });

  it('yol-üstü öz süre halka başına; iç içe zincirde toplamı kök süresi', () => {
    const cp = computeCriticalPath(TRACE);
    expect(cp.onPathSelfNs.get('checkout')).toBe(200 * MS);
    expect(cp.onPathSelfNs.get('payments')).toBe(200 * MS);
    expect(cp.onPathSelfNs.get('db')).toBe(600 * MS);
    const sum = [...cp.onPathSelfNs.values()].reduce((a, b) => a + b, 0);
    expect(sum).toBe(cp.rootWallNs);
  });

  it('saat kayması: ebeveynden önce başlayan çocukta yalnız ÖRTÜŞEN pay düşer, negatif yok', () => {
    const cp = computeCriticalPath([sp('root', '', 100, 300), sp('child', 'root', 50, 320)]);
    expect(cp.order).toEqual(['root', 'child']);
    // örtüşme = min(400, 370) − max(100, 50) = 270 → kök 300 − 270 = 30 ms
    expect(cp.onPathSelfNs.get('root')).toBe(30 * MS);
    for (const v of cp.onPathSelfNs.values()) expect(v).toBeGreaterThanOrEqual(0);
  });

  it('çok kök: en uzun kök seçilir; boş girdi boş sonuç', () => {
    const cp = computeCriticalPath([sp('kisa', '', 0, 10), sp('uzun', '', 0, 50)]);
    expect(cp.rootId).toBe('uzun');
    expect(cp.rootWallNs).toBe(50 * MS);
    const empty = computeCriticalPath([]);
    expect(empty).toMatchObject({ spanCount: 0, rootWallNs: 0, rootId: null, leafId: null });
    expect(empty.ids.size).toBe(0);
  });
});

describe('sayfalar toplam basmıyor (kaynak pini)', () => {
  const read = (p: string) => readFileSync(resolve(__dirname, '..', 'pages', p), 'utf8');
  for (const file of ['Trace.tsx', 'TraceCompare.tsx']) {
    it(`${file}: span sayısı + kök duvar süresi, toplam alanı yok`, () => {
      const src = read(file);
      expect(src).not.toMatch(/critical(Path)?\??\.totalNs/);
      expect(src).toMatch(/\.spanCount/);
      expect(src).toMatch(/\.rootWallNs/);
      expect(src).not.toContain('summing to');
    });
  }
});
