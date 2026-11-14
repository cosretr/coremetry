// Pod.annotationLane.test.ts — v0.10.312 (chart-layer audit Dilim 2.1):
// annotation şeridi /pod'da RED yığınının ALTINDA, ns pencere + sayfa zoom
// yığını sözleşmesiyle monte. Kaynak pini: şerit kaldırılır ya da Overview
// linkinin üstüne kayarsa kırmızı. v0.10.2154 — şeridin Service.tsx'teki
// montajı Details'in Performance bölümüyle birlikte kalktı (operatör); /pod
// tek montaj.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Pod.tsx'), 'utf8');

describe('Pod annotation şeridi', () => {
  it('RED yığınının altında, ns pencere + handleZoom ile monte', () => {
    const mount = '<ServiceAnnotationLane service={service} fromNs={from} toNs={to} onZoomTo={handleZoom} />';
    const i = src.indexOf(mount);
    expect(i).toBeGreaterThan(-1);
    expect(i).toBeGreaterThan(src.indexOf('storageKey="pod-response-time"'));
    expect(i).toBeLessThan(src.indexOf('Bu pod bir Coremetry servisine eşlenmedi'));
    expect(src.split('<ServiceAnnotationLane ').length - 1).toBe(1);
  });
  it('onZoomTo sayfa zoom yığınına gider', () => {
    expect(src).toContain('usePageZoomRange(DEFAULT_RANGE_PRESET)'); // v0.10.787 — genel varsayılan sabitten
  });
});
