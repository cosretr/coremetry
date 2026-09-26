// slosFanout.pin.test.ts — v0.10.854 (scale-audit 2026-09-23 🔴): /slos tablosu
// TÜM listeyi çizerken satır başına iki HAM useEffect isteği açıyordu
// (autocreate ≤200 SLO → 400 istek/yükleme). Pin: çipler useQuery (dedup +
// staleTime), LazyMount içinde (yalnız görünür satır fetch eder), >100 satırda
// content-visibility.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, '..', 'Slos.tsx'), 'utf8');
const body = (name: string) => {
  const i = src.indexOf(`function ${name}(`);
  const j = src.indexOf('\nfunction ', i + 1);
  return src.slice(i, j < 0 ? undefined : j);
};

describe('/slos satır fan-out', () => {
  it('ForecastChip ve BurnSparkline useQuery ile çeker, ham useEffect yok', () => {
    for (const name of ['ForecastChip', 'BurnSparkline']) {
      const b = body(name);
      expect(b, name).toContain('useQuery({');
      expect(b, name).toContain('staleTime: 60_000');
      expect(b, name).not.toContain('useEffect(');
    }
  });
  it('iki çip LazyMount içinde, satırlar >100\'de content-visibility taşır', () => {
    expect(src.split('<LazyMount compact').length - 1).toBe(2);
    // v0.10.945 (tablo standardı T6) — content-visibility tek sınıftan (`.cv-row`).
    expect(src).toContain("dt.sortedRows.length > 100 ? 'cv-row' : undefined");
  });
});
