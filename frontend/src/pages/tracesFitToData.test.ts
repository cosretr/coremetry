// tracesFitToData.test.ts — v0.10.738 (operatör: Problems pivotu sonrası
// 6 sa / 24 sa pencerede "histogram tek bir bar"). Kova genişliği piksel
// bütçesinden (v0.9.715), inceltilmez; veri pencerenin küçük bir kısmına
// sıkışmışsa şerit "veriye sığdır" der ve tık sürükle-seçim yolundan
// (applyBrush → zoom yığını) pencereyi daraltır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const traces = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');

describe('/traces veriye sığdır (v0.10.738)', () => {
  it('sığdırma aralığı saf çekirdekten, şerit başlığında düğme, tık zoom yoluna', () => {
    expect(traces).toContain('dataExtent(volSeries?.count ?? null, listRangeNs.from / 1e9, listRangeNs.to / 1e9)');
    expect(traces).toContain('⤢ veriye sığdır');
    expect(traces).toContain('applyBrush(fitExtent.fromSec * 1000, fitExtent.toSec * 1000)');
  });
});
