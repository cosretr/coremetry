// restartToneClass — v0.10.973 (tablo standardı T5/T9).
//
// NE ÇİVİLİYOR: ServicePodsTable'ın ↻ hücresi satır içi
// `color: restartColor(n)` yerine sınıf alır. Renk DEĞİŞMEMELİ: sınıf,
// restartColor'ın döndürdüğü token'ın aynısını boyar (her n için), sınıflar
// globals.css'te yalnız o token'la tanımlı ve hücre bilinmeyen restart'ı
// eskisi gibi soluk basar.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { restartColor, restartToneClass } from './thresholds';

const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');
const TOKEN_OF: Record<ReturnType<typeof restartToneClass>, string> = {
  'cell-err': 'var(--err)',
  'cell-warn': 'var(--warn)',
  'cell-faint': 'var(--text3)',
};

describe('restartToneClass — restartColor ile aynı eşik, aynı token (v0.10.973)', () => {
  it('her restart sayısında sınıfın token\'ı restartColor\'ın rengi', () => {
    for (const n of [0, 1, 2, 3, 5, 8, 9, 50, 1000]) {
      expect(TOKEN_OF[restartToneClass(n)], `n=${n}`).toBe(restartColor(n));
    }
  });

  it('eşik sınırları: 2 soluk, 3 warn, 8 warn, 9 err', () => {
    expect(restartToneClass(2)).toBe('cell-faint');
    expect(restartToneClass(3)).toBe('cell-warn');
    expect(restartToneClass(8)).toBe('cell-warn');
    expect(restartToneClass(9)).toBe('cell-err');
  });

  it('sınıflar globals.css\'te yalnız metin rengi, aynı token\'la', () => {
    expect(css).toMatch(/\n\.cell-err\s*\{\s*color:\s*var\(--err\);\s*\}/);
    expect(css).toMatch(/\n\.cell-warn\s*\{\s*color:\s*var\(--warn\);\s*\}/);
    expect(css).toMatch(/\n\.cell-faint\s*\{\s*color:\s*var\(--text3\);\s*\}/);
  });

  it('ServicePodsTable ↻ hücresi: sınıfla, satır içi renk yok; bilinmeyen = soluk', () => {
    const tbl = readFileSync(resolve(__dirname, '../service/ServicePodsTable.tsx'), 'utf8');
    expect(tbl).toContain("className={`num ${r.restartsUnknown ? 'cell-faint' : restartToneClass(r.restarts ?? 0)}`}");
    expect(tbl).not.toContain("style={{ color: r.restartsUnknown ? 'var(--text3)' : restartColor(r.restarts ?? 0) }}");
  });
});
