// v0.10.940 (operatör onayı K2) — /ai Kaynak seçici: üretim / Değerlendirme.
//
// KAYNAK PİNİ: üç okuma (stats / series / calls) AYNI kaynağı taşımalı ve
// kaynak efekt bağımlılıklarında olmalı. Biri unutulursa ekran sessizce
// karışır: KPI karoları üretimi, tablo değerlendirme çağrılarını sayar —
// hiçbir hata görünmez. Bütçe parametresiz kalır (bütçe üretim harcamasının
// tavanı; evalset onu şişirmemeli).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');

describe('v0.10.940 — /ai ?source=evalset', () => {
  it('kaynak URL\'den (paylaşılan link aynı görünümü açar)', () => {
    expect(src).toContain("searchParams.get('source') === 'evalset' ? 'evalset' : undefined");
    expect(src).toContain('aria-label="Kaynak"');
    expect(src).toContain('<option value="evalset">Değerlendirme</option>');
  });

  it('üç okuma da kaynağı taşır ve kaynak değişince yeniden okur', () => {
    expect(src).toContain('api.aiStats({ from, to, source })');
    expect(src).toContain('api.aiSeries({ from, to, source })');
    const calls = src.slice(src.indexOf('api.aiCalls({'), src.indexOf('api.aiCalls({') + 400);
    expect(calls).toMatch(/\bsource,/);
    expect(src).toContain('}, [range, source]);');
    expect(src).toContain('}, [range, surface, provider, status, source]);');
  });

  it('kaynak değişince önceki KPI / grafik sıfırlanır (yoklama efektinden ÖNCE)', () => {
    // v0.10.940 — aksi hâlde Üretim sayıları yeni okuma gelene dek
    // "Kaynak: Değerlendirme" etiketinin altında durur (ya da tersi).
    const reset = src.indexOf('useEffect(() => { setStats(undefined); setSeries(undefined); }, [source]);');
    expect(reset).toBeGreaterThan(-1);
    expect(reset).toBeLessThan(src.indexOf('}, [range, source]);'));
  });

  it('kaynak değişince öbür kaynağın yüzey süzgeci temizlenir (tek önek sabiti)', () => {
    // v0.10.940 — sunucu kaynak koşulunu süzgeçle AND'ler: `evalset-…`
    // süzgeci Üretim'de hep 0 satır. Önek literali sayfada değil, types.ts'te.
    expect(src).toContain("if (surface && surface.startsWith(EVALSET_SURFACE_PREFIX) !== (v === 'evalset')) setSurface('');");
    expect(src).not.toContain("'evalset-'");
  });

  it('bütçe her zaman üretim (parametresiz)', () => {
    expect(src).toContain('api.aiBudget()');
    expect(src).not.toMatch(/api\.aiBudget\(\s*\{/);
  });
});
