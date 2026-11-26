// TimeChart.strictGaps.contract — v0.10.2480 (operator-reported, prod: Traces
// şeridinin süre çizgisi boş kovaları üçgenle birleştiriyor, sayım ekseninin
// "0"ı süre ekseninin tepesinde başıboş duruyordu). Sözleşme: (1) strictGaps
// line serisinde köprü yok (stepGapsRefiner bağlanmaz → uPlot null'da kırar),
// yalıtık örnek nokta basılır; (2) strictGaps yapı imzasında (rebuild);
// (3) VolumeChart sayımı SOL dikey eksende çizer (yAxisLeft), çizgi strictGaps.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { stripTsComments } from '@/styles/zLayers.test';
import { timeChartBuildSignature } from '@/lib/chartBuildSig';

describe('TimeChart strictGaps (source contract)', () => {
  const src = stripTsComments(readFileSync(resolve(__dirname, 'TimeChart.tsx'), 'utf8'));
  it('strictGaps → gaps köprüsü yok, yalıtık nokta filtresi var', () => {
    expect(src).toContain('...(s.strictGaps ? {} : { gaps: stepGapsRefiner })');
    expect(src).toMatch(/s\.strictGaps \? \{ show: false, size: 5, filter: isolatedPointsFilter \}/);
    expect(src).not.toMatch(/spanGaps:\s*true/);
  });

  it('VolumeChart: sayım sol dikey eksende', () => {
    const vc = stripTsComments(readFileSync(resolve(__dirname, '../traces/VolumeChart.tsx'), 'utf8'));
    expect(vc).toMatch(/\n\s*yAxisLeft\n/);
  });

  it('strictGaps yapı imzasına girer; yokken imza aynen', () => {
    const base = { height: 140, hasBrush: false, hasFmtLeft: false, hasFmtRight: false, hasFmtX: false, renderable: true };
    const line = { key: 'rt', label: 'rt', color: 'var(--orange)', type: 'line', axis: 'right', width: 2 };
    const plain = timeChartBuildSignature({ ...base, series: [line] });
    const strict = timeChartBuildSignature({ ...base, series: [{ ...line, strictGaps: true }] });
    expect(strict).not.toBe(plain);
    expect(timeChartBuildSignature({ ...base, series: [{ ...line, strictGaps: false }] })).toBe(plain);
  });
});
