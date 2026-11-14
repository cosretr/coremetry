// ProblemInsightStrip.pin.test.ts — v0.10.562: şerit ilk ekranın (.pd-cols) üstünde;
// hata sayfayı bozmaz (null döner).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const detail = readFileSync(resolve(__dirname, 'ProblemDetail.tsx'), 'utf8');
const strip = readFileSync(resolve(__dirname, 'ProblemInsightStrip.tsx'), 'utf8');

describe('ProblemInsightStrip', () => {
  // v0.10.2127 (Problems sadeleştirme S7; operatör kararı 2026-11-13: teknik
  // kanıt altta KAPALI) — şerit ilk ekrandan "Teknik kanıt" bölümüne indi.
  it('yerleşim: kapalı "Teknik kanıt" içinde', () => {
    const s = detail.indexOf('<ProblemInsightStrip problemId={problem.id} />');
    const tech = detail.indexOf('<Sect title="Teknik kanıt" collapsible>');
    expect(s).toBeGreaterThan(0);
    expect(tech).toBeGreaterThan(0);
    expect(s).toBeGreaterThan(tech);
  });
  it('hata → null, yükleniyor → soluk satır', () => {
    expect(strip).toContain('if (q.isError || !q.data) return null;');
    expect(strip).toContain('ins-strip--pending');
  });
});
