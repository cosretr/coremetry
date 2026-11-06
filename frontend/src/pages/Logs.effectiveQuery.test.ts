// v0.10.418 — log arama denetimi B7: uygulanan arama cümlesi görünür ve
// kopyalanır (compileSearch 12 kullanım, 0 render idi). Kaynak pini.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

describe('effective search clause chip (B7)', () => {
  // v0.10.1498 (operatör onaylı Discover taslağı) — "Uygulanan arama" bandı
  // tek süzgeç satırına (pages/logs/LogsFilterRow.tsx) "KQL ⓘ" popover'ı
  // olarak indi; içerik ve kopyalama aynı. Sayfa derlenmiş cümleyi satıra verir.
  const page = readFileSync(resolve(__dirname, './Logs.tsx'), 'utf8');
  const src = readFileSync(resolve(__dirname, './logs/LogsFilterRow.tsx'), 'utf8');
  it('compiledSearch render + CopyButton', () => {
    expect(page).toContain('compiledSearch={compiledSearch}');
    expect(src).toContain('{compiledSearch && (');
    expect(src).toContain('<CopyButton value={compiledSearch}');
    expect(src).toContain('<code title={compiledSearch}>{compiledSearch}</code>');
  });
  it('pill bloğunun dışında (serbest metinle de görünür)', () => {
    // KQL ⓘ, pill'lerin "Clear all" bloğunun KAPANIŞINDAN sonra ve kendi
    // kapısıyla — `filters.length > 0` içine düşerse yalnız pill varken görünür.
    const chip = src.indexOf('<CopyButton value={compiledSearch}');
    // v0.10.1854 — "Clear all" → "Tümünü temizle" (FilterChipRow trailing yuvası).
    const barClose = src.indexOf('Tümünü temizle\n            </Button>\n          )}');
    expect(barClose).toBeGreaterThan(0);
    expect(chip).toBeGreaterThan(barClose);
    expect(src.indexOf('{compiledSearch && (')).toBeGreaterThan(barClose);
    // satırın görünürlük kapısı da derlenmiş cümleyi sayar
    expect(src).toContain('|| !!compiledSearch;');
  });
});
