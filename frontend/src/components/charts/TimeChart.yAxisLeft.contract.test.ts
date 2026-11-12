// TimeChart.yAxisLeft.contract — v0.10.2036 (operatör ekranı: anomali
// detayının dar grafiğinde y ekseni değerleri ÜSTTE yatay diziliyordu; uPlot
// side 0 = üst şerit). Sözleşme: (1) `yAxisLeft` sol ekseni side 3'e (dikey,
// oluk genişliği ölçülür) koyar, varsayılan side 0 aynen; (2) dikey eksende
// oluk = en geniş etiket (measuredAxisSize), yatayda punto; (3) anomali
// detayının kayıtlı görüntüsü (compact) sol ekseni kullanır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type uPlot from 'uplot';
import { measuredAxisSize, AXIS_MIN_SIZE_PX } from '@/lib/chart/axisSize';
import { stripTsComments } from '@/styles/zLayers.test';

describe('TimeChart yAxisLeft (source contract)', () => {
  const src = stripTsComments(readFileSync(resolve(__dirname, 'TimeChart.tsx'), 'utf8'));
  it('varsayılan üst şerit, yAxisLeft ile sol şerit', () => {
    expect(src).toMatch(/yAxisLeft = false,/);
    expect(src).toMatch(/yAxis\('y', yAxisLeft \? 3 : 0, fmtLeftRef/);
  });
  it('anomali detayının kayıtlı görüntüsü sol ekseni kullanır', () => {
    const player = stripTsComments(readFileSync(resolve(__dirname, '../coremetry/anomalies/MLEventPlayer.tsx'), 'utf8'));
    expect(player).toMatch(/yAxisLeft=\{compact\}/);
  });
});

describe('measuredAxisSize: dikey eksende oluk etiket genişliği', () => {
  const size = measuredAxisSize('12px monospace');
  const plot = (side: number) => ({ axes: [{ side, ticks: { size: 3 }, gap: 5 }] }) as unknown as uPlot;
  it('uzun etiket dikey oluğu genişletir; yatay oluk etiketten bağımsız', () => {
    const labels = ['0', '1.34 bin', '2.80 bin'];
    const left = size(plot(3), labels, 0, 0);
    const top = size(plot(0), labels, 0, 0);
    expect(left).toBeGreaterThan(top);
    expect(top).toBeGreaterThanOrEqual(AXIS_MIN_SIZE_PX);
    expect(size(plot(0), ['0', '123456789012'], 0, 0)).toBe(top);
  });
});
