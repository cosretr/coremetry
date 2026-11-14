// v0.10.2153 — Operations trend grafiği: p99 çizgisi OpenTelemetry mavisi.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { SERIES_C } from './TrendSpark';

const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
const rule = (sel: string) => new RegExp(sel.replace(/[.]/g, '\\.') + '\\s*\\{([^}]*)\\}').exec(css)?.[1] ?? '';

describe('TrendSpark renkleri (v0.10.2153)', () => {
  it('okuma (ipucu) renkleri: çağrı orange, hata err, p99 otel-blue', () => {
    expect(SERIES_C).toEqual({ calls: 'var(--orange)', errors: 'var(--err)', p99: 'var(--otel-blue)' });
  });
  it('çizim renkleri CSS\'te aynı token\'lar; token tanımlı', () => {
    expect(rule('.trend-spark .ts-bar')).toContain('var(--orange)');
    expect(rule('.trend-spark .ts-err')).toContain('var(--err)');
    expect(rule('.trend-spark .ts-p99')).toContain('var(--otel-blue)');
    expect(rule('.trend-spark .ts-cur')).toContain('var(--otel-blue)');
    expect(css).toMatch(/--otel-blue:\s*#425cc7/);
  });
});
