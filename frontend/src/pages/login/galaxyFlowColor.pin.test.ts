// Login galaksisinin akan noktalarının rengi.
// v0.10.1929–1930: ayrı `--galaxy-gold` token'ı (önce altın, sonra OTel #f5a800).
// v0.10.1932 (operatör-raporlu: "Traces sayfasındaki süre grafiğinin sarı tonunda
// değil akan ışıklar") — ayrı token KALKTI; canvas Traces response-time
// çizgisinin token'ını (--orange) okur, iki yüzey her temada aynı tonda kalır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('login galaksisi akan nokta rengi', () => {
  it('Traces süre çizgisi --orange kullanır', () => {
    expect(read('../../components/traces/volumeSeries.ts')).toMatch(/key: 'rt'[^\n]*color: 'var\(--orange\)'/);
  });
  it('canvas akan noktaları aynı token\'ı okur (--galaxy-gold / --warn değil)', () => {
    const src = read('TopologyBackdrop.tsx');
    expect(src).toContain("flow: resolveVar('var(--orange)')");
    expect(src).not.toContain('--galaxy-gold');
    expect(src).not.toContain("resolveVar('var(--warn)')");
  });
  it('ölü --galaxy-gold token\'ı CSS\'te kalmadı', () => {
    expect(read('../../styles/globals.css')).not.toContain('--galaxy-gold');
  });
});
