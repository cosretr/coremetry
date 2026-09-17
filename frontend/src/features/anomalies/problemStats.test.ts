// problemStats.test.ts — v0.10.774: saf yardımcılar + kablolama pinleri.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { categoryChips, fmtMttr, priorityChips, statsChart, statsWindowFromParam } from './problemStats';

describe('problemStats — saf', () => {
  it('pencere parametresi: bilinen → aynı, bilinmeyen/boş → 24h', () => {
    expect(statsWindowFromParam('1h')).toBe('1h');
    expect(statsWindowFromParam('7d')).toBe('7d');
    expect(statsWindowFromParam('3h')).toBe('24h');
    expect(statsWindowFromParam(null)).toBe('24h');
  });
  it('fmtMttr: örnek yoksa —; sn / dk / sa / g', () => {
    expect(fmtMttr(120, 0)).toBe('—');
    expect(fmtMttr(45, 3)).toBe('45 sn');
    expect(fmtMttr(1800, 3)).toBe('30 dk');
    expect(fmtMttr(5400, 3)).toBe('1.5 sa');
    expect(fmtMttr(172800, 3)).toBe('2.0 g');
  });
  it('statsChart: iki çubuk serisi, x unix sn', () => {
    const c = statsChart({ buckets: [{ t: 100, opened: 2, resolved: 1 }, { t: 400, opened: 0, resolved: 3 }] });
    expect(c.times).toEqual([100, 400]);
    expect(c.series.map(s => s.key)).toEqual(['opened', 'resolved']);
    expect(c.series[0].data).toEqual([2, 0]);
    expect(c.series[1].data).toEqual([1, 3]);
    expect(c.series.every(s => s.type === 'bar')).toBe(true);
  });
  it('çipler: öncelik sabit sıra, kategori sayıya göre ve sıfırsız', () => {
    expect(priorityChips({ P3: 4, P1: 1 })).toEqual([{ key: 'P1', count: 1 }, { key: 'P2', count: 0 }, { key: 'P3', count: 4 }]);
    expect(categoryChips({ ERROR: 3, SLOWDOWN: 5, CUSTOM: 0, AVAILABILITY: 3 })).toEqual([
      { key: 'SLOWDOWN', count: 5 }, { key: 'AVAILABILITY', count: 3 }, { key: 'ERROR', count: 3 },
    ]);
  });
  it('şerit Inbox başlığında, kancası ve pencere parametresi kablolu', () => {
    const inbox = readFileSync(resolve(__dirname, '..', '..', 'pages', 'Inbox.tsx'), 'utf8');
    expect(inbox).toContain('<ProblemStatsStrip env={env} />');
    const strip = readFileSync(resolve(__dirname, 'ProblemStatsStrip.tsx'), 'utf8');
    expect(strip).toContain('useProblemStats(win, env)');
    expect(strip).toContain("{ replace: true }");
    expect(strip).toContain('leftUnit="count"');
  });
});
