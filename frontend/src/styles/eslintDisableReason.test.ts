import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';

// eslintDisableReason — v0.10.919 (buton bütünlüğü, Seçenek B).
//
// `ui/no-raw-button` (eslint.config.js) ham `<button>`/`role="button"`ı
// ui/ dışında yasaklıyor; mevcutlar eslint-suppressions.json'da sayılı.
// Satır istisnası mümkün ama GEREKÇEYLE — ESLint 9'un `-- açıklama`
// sözdizimi:
//   {/* eslint-disable-next-line ui/no-raw-button -- <neden, ≥8 karakter> */}
// eslint-plugin-eslint-comments EKLENMEDİ (yeni bağımlılık); kapı burada.
//
// İki kural:
//   1. `ui/no-raw-button`u susturan her yönerge gerekçe taşır.
//   2. Kural listesiz ("blanket") yönerge YOK — `// eslint-disable-line`
//      tek başına bu kuralı da sessizce susturur ve 1. kuralı by-pass eder.
//      Taban: 0 (ölçüldü; depodaki 74 yönergenin hepsi kural adlı).
//
// Arama dizesi çalışma anında kurulur: bu dosyanın kendi yorumları
// kendini yakalamasın.
const SRC = resolve(__dirname, '..');
const DIRECTIVE = new RegExp('eslint-' + 'disable(?:-next-line|-line)?\\b([^\\n]*)', 'g');
const RULE = 'ui/' + 'no-raw-button';
const SELF = 'eslintDisableReason' + '.test.ts';

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(ts|tsx)$/.test(p) && !p.endsWith(SELF)) out.push(p);
  }
  return out;
}

const directives: { at: string; rest: string }[] = [];
for (const p of walk(SRC)) {
  readFileSync(p, 'utf8').split('\n').forEach((line, i) => {
    for (const m of line.matchAll(DIRECTIVE)) {
      directives.push({ at: `${p.slice(SRC.length + 1)}:${i + 1}`, rest: m[1].replace(/\*\/.*$|\}.*$/, '').trim() });
    }
  });
}

describe('eslint-disable gerekçe kapısı', () => {
  it('tarama boş dönmüyor (desen kaymadı)', () => {
    expect(directives.length).toBeGreaterThan(10);
  });

  it(`${RULE} istisnaları gerekçeli ("-- <neden>", ≥8 karakter)`, () => {
    const bad = directives
      .filter(d => d.rest.includes(RULE))
      .filter(d => !/\s--\s+\S.{7,}/.test(d.rest))
      .map(d => d.at);
    expect(bad, `Gerekçesiz ${RULE} istisnası:\n${bad.join('\n')}`).toEqual([]);
  });

  it('kural listesiz (blanket) eslint-disable yok', () => {
    const bad = directives.filter(d => d.rest === '' || d.rest.startsWith('--')).map(d => d.at);
    expect(bad, `Kural adı taşımayan yönerge (her kuralı susturur):\n${bad.join('\n')}`).toEqual([]);
  });
});
