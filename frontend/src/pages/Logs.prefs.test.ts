// Logs.prefs.test.ts — v0.10.318 (DataTable audit dilim 8, Logs) kaynak pini:
// sunucu sütun tercihi Traces sözleşmesiyle — bir kez benimseme, yalnız
// operatör değişikliği kaydeder (deep-link cols= sunucuyu ezmez), prefs
// bekliyorken writeUrl cols yazmaz.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'Logs.tsx'), 'utf8');

describe('Logs sunucu sütun tercihi', () => {
  it("useTablePrefs('logs') + bir kez benimseme + URL öncelik kapısı", () => {
    expect(src).toContain("useTablePrefs('logs')");
    expect(src).toContain('if (prefs.model === undefined || prefsAdopted.current) return;');
    expect(src).toContain('if (urlHadCols.current || !prefs.model) return;');
  });
  it('prefs.save yalnız changeCols içinde — URL içe aktarımı kaydetmez', () => {
    expect(src.split('prefs.save(').length - 1).toBe(1);
    const i = src.indexOf('prefs.save(');
    expect(i).toBeGreaterThan(src.indexOf('const changeCols = '));
    expect(i).toBeLessThan(src.indexOf('const removeColumn = '));
    const imp = src.slice(src.indexOf('const urlSig = logsUrlSig;'), src.indexOf('const writeUrl = '));
    expect(imp).not.toContain('prefs.save(');
  });
  it('kendi-kendine-yazım yarışı: prefs bekliyorken cols yazılmaz', () => {
    expect(src).toContain("(prefs.model === undefined && !urlHadCols.current) ? '' : colsParam(cols ?? logCols)");
  });
  it('v0.10.1502 — Columns ▾ seçicisi tek yoldan (changeCols) yazar; kendisi sunucuya yazmaz', () => {
    expect(src).toContain('columns={viewCols} fields={fields} onColumns={changeCols}'); // v0.10.1856 — görünen küme (akıllı kolonlar dahil)
    for (const rel of ['logs/LogsColumnPicker.tsx', 'logs/LogsTableToolbar.tsx', 'logs/logColumns.ts']) {
      const f = readFileSync(resolve(__dirname, rel), 'utf8');
      expect(f, rel).not.toContain('prefs.save(');
      expect(f, rel).not.toContain('setRaw(');
      expect(f, rel).not.toContain('setSearchParams');
    }
  });
});
