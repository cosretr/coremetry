// mlPlay.test.ts — v0.10.1846: oynatma zamanlamasının SAF sözleşmesi
// (adım hizalama, ilk sapmadan 6 kova önce başlama, sonda durma, hareket
// azaltmada yalnız adımlar, ?play=1, liste ▶ linki).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { activeStep, frameAt, indexAt, nextIndex, nextReduced, playStart, reducedSequence, stepIndices, wantsAutoPlay, PLAY_LEAD_BUCKETS } from './mlPlay';
import { mlEventHref, mlEventPlayHref } from './anomalyHref';

const times = Array.from({ length: 20 }, (_, i) => 1000 + i * 300);
const steps = [
  { key: 'first', label: 'İlk sapma · 14:25', t: 1000 + 10 * 300 },
  { key: 'detect', label: 'Tespit · 14:30', t: 1000 + 11 * 300 + 10 },
  { key: 'peak', label: 'Tepe · 14:35', t: 1000 + 14 * 300 },
  { key: 'open', label: 'Sürüyor · 14:40', t: 1000 + 19 * 300 },
];

describe('mlPlay', () => {
  it('steps align to series points (last point ≤ t)', () => {
    expect(stepIndices(times, steps)).toEqual([10, 11, 14, 19]);
    expect(indexAt(times, 0)).toBe(0);
  });
  it('starts 6 buckets before the first deviation, never below 0', () => {
    expect(PLAY_LEAD_BUCKETS).toBe(6);
    expect(playStart(times, steps)).toBe(4);
    expect(playStart(times, [{ key: 'first', label: '', t: 1000 + 2 * 300 }])).toBe(0);
    expect(playStart(times, [])).toBe(0);
  });
  it('advances one bucket and stops at the end (no loop)', () => {
    expect(nextIndex(4, 20)).toBe(5);
    expect(nextIndex(19, 20)).toBeNull();
  });
  it('active step and reduced-motion sequence', () => {
    const idx = stepIndices(times, steps);
    expect(activeStep(idx, 3)).toBe(-1);
    expect(activeStep(idx, 12)).toBe(1);
    expect(activeStep(idx, 19)).toBe(3);
    expect(reducedSequence(idx)).toEqual([10, 11, 14, 19]);
    expect(reducedSequence([3, 3, 1])).toEqual([1, 3]);
    // hareket azaltma: kova kova değil, adımdan adıma atlar; sonda durur
    expect(nextReduced([10, 11, 14, 19], 10)).toBe(11);
    expect(nextReduced([10, 11, 14, 19], 12)).toBe(14);
    expect(nextReduced([10, 11, 14, 19], 19)).toBeNull();
  });
  it('frame lookup by time', () => {
    const frames = [{ t: times[5], clock: '14:00', text: 'x' }];
    expect(frameAt(times, frames, 5)?.text).toBe('x');
    expect(frameAt(times, frames, 6)).toBeNull();
  });
  it('?play=1 and the list ▶ link', () => {
    expect(wantsAutoPlay(new URLSearchParams('play=1'))).toBe(true);
    expect(wantsAutoPlay(new URLSearchParams('play=0'))).toBe(false);
    expect(mlEventPlayHref('a b')).toBe(`${mlEventHref('a b')}?play=1`);
  });
  // v0.10.2021 — liste satırı = grup (mockup anomaly-unified.html): "Olayı izle"
  // sütunu kalktı (fazla sütun yok); oynatma detay sayfasından. Satır tıkı
  // grubun en güçlü üyesinin detayını OYNATMADAN açar.
  // v0.10.2027 — liste UnifiedAnomalies'e taşındı; satır href'i satırın kendi
  // hedefi (ML grubunda en güçlü üye, mlEventHref — oynatmasız).
  // v0.10.2051 — satır sunucudan; href lider üyeden rowHref (unifiedRows.ts).
  it('row click (getRowHref) opens the row target without play', () => {
    const src = readFileSync(resolve(__dirname, '../components/coremetry/anomalies/UnifiedAnomalies.tsx'), 'utf8');
    expect(src).not.toMatch(/mlEventPlayHref/);
    expect(src).toMatch(/getRowHref=\{rowHref\}/);
  });
});
