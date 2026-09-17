// VolumeChart.units.test.ts — v0.10.759 (operatör, prod şerit ipucu:
// "response time (median) 1.3k"). Süre serisi ipucu/lejantta "1.3s",
// sayım serileri tam sayı; eksen biçimleyicisi (fmtLeft) aynen.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fmtSmart } from '@/lib/chartFmt';
import { sortedTooltipRows } from '@/lib/chart/tooltipModel';

describe('VolumeChart birimleri', () => {
  it('kaynak: sol eksen ms, sağ eksen count; eksen biçimleyicisi duruyor', () => {
    const src = readFileSync(resolve(__dirname, 'VolumeChart.tsx'), 'utf8');
    expect(src).toContain('leftUnit="ms"');
    expect(src).toContain('rightUnit="count"');
    expect(src).toContain('fmtLeft={fmtVolumeDuration}');
  });
  it('ipucu satırı: 1300 ms → 1.3s, 830 ms → 830ms; sayım 39 → 39, 1234 → 1.23k', () => {
    const rows = sortedTooltipRows([
      { label: 'response time (median)', color: '#000', value: 1300, unit: 'ms' },
      { label: 'traces', color: '#000', value: 39, unit: 'count' },
      { label: 'error traces', color: '#000', value: 0, unit: 'count' },
    ]);
    const text = Object.fromEntries(rows.map(r => [r.label, r.text]));
    expect(text['response time (median)']).toBe('1.3s');
    expect(text['traces']).toBe('39');
    expect(text['error traces']).toBe('0');
    expect(fmtSmart(830, 'ms')).toBe('830ms');
    expect(fmtSmart(1234, 'count')).toBe('1.23k');
    expect(fmtSmart(39.4, 'count')).toBe('39');
  });
});
