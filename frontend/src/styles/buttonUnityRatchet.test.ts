import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { stripTsComments } from './zLayers.test';

// buttonUnityRatchet — v0.10.914 (buton bütünlüğü, dilim 1).
//
// Operatör: "bazı butonlar traces mesela farklılaşıyor". Kök sebep: sayfa
// kendi `<button>`unu kurup kendi sınıfını (tl-go, ov-facet, elle
// `.segmented`) yazıyordu. Ortak atomlar (Button / IconButton / Chip /
// SegmentedControl) dururken yeni ham düğme eklemek sapmayı geri getirir.
//
// KAPI: iki sayı yalnız AŞAĞI iner. Bir dilim sayıyı düşürdüğünde tavan da
// aynı commit'te düşürülür; artış = atom kullan. Tabanlar v0.10.914:
// 125 ham `<button` (ui atomları hariç), 13 dosyada elle `className="segmented"`.
// v0.10.915 (dilim 2): 97 ham `<button`, elle `.segmented` 0 — tüm tek
// seçimli gruplar SegmentedControl.
// v0.10.919 (Seçenek B temeli): sayım YORUMLARI ATIYOR. 97'nin 12'si
// yorum metnindeki `<button` sözcüğüydü (TopbarSearch, Sidebar, Traces …);
// gerçek taban 85 (TypeScript AST sayımıyla birebir). `stripTsComments`
// deponun standart ayıklayıcısı: basit regex ayıklayıcı `//` içindeki bir
// `/*`i blok yorumu sanıp AdminClickhouse'un yarısını gizliyordu (ölçüldü).
// Aynı commit'te `role="button"` da sayılıyor (span/div/tr üstünde düğme
// taklidi) ve ESLint `ui/no-raw-button` kuralı ikisini de satırında
// gösteriyor (eslint-suppressions.json mevcutları sayar). Koşullu rol
// (`role={x ? 'button' : …}`) de sayılınca role tabanı 18.
// v0.10.924 (Faz 2): 85 → 0 ham `<button`, 18 → 0 `role="button"`. Atoma
// dönüşemeyen 17 yer (liste seçeneği, graf düğümü, <td>/<tr>, düğme içeren
// başlık) gerekçeli satır istisnası taşıyor; o sayı da yalnız AŞAĞI iner —
// istisna, tavanı sıfırlanmış kapının arka kapısı olmasın.
const SRC = resolve(__dirname, '..');
const MAX_RAW_BUTTONS = 0;
const MAX_ROLE_BUTTON = 0;
const MAX_REASONED_EXEMPTIONS = 17;
const MAX_SEGMENTED_FILES = 0;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter(p => !p.includes(join('components', 'ui') + '/'));
// Gerekçeli satır istisnası (eslint.config.js'te belgeli
//   `eslint-disable-next-line ui/no-raw-button -- <neden>`) ESLint'ten
// geçer; mandal da onu SAYMAZ — aksi hâlde belgelenen kaçış yolu sıfır
// paylı tavanda yine kırmızı olurdu. Yorum ayıklanmadan ÖNCE, direktifin
// hemen altındaki satırın belirteçleri etkisizleştirilir (satır silinmez:
// `*/}` kapanışı düşerse stripTsComments devamı yutardı).
// Gerekçe biçimini eslintDisableReason.test.ts ayrıca denetler.
const REASONED = new RegExp('eslint-' + 'disable-next-line\\b[^\\n]*\\bui/no-raw-button\\b[^\\n]*\\s--\\s+\\S.{7,}');
function exemptReasoned(src: string): string {
  const ls = src.split('\n');
  for (let i = 0; i < ls.length - 1; i++) {
    if (REASONED.test(ls[i])) ls[i + 1] = ls[i + 1].replace(/<button\b/g, '<button_').replace(/(?<=\s)role=/g, 'role_=');
  }
  return ls.join('\n');
}
const code = new Map(files.map(p => [p, stripTsComments(exemptReasoned(readFileSync(p, 'utf8')))]));
const count = (re: RegExp) => [...code.values()].reduce((a, s) => a + (s.match(re)?.length ?? 0), 0);

describe('buton bütünlüğü mandalı', () => {
  it('ham <button sayısı tavanı aşmaz', () => {
    const n = count(/<button\b/g);
    expect(n, 'yeni düğme için Button/IconButton/Chip/SegmentedControl kullan').toBeLessThanOrEqual(MAX_RAW_BUTTONS);
  });
  it('role="button" taklidi tavanı aşmaz', () => {
    // Öncesinde boşluk: JSX özniteliği. `'[role="button"]'` gibi seçici
    // dizeleri (MetricPanel querySelector) sayılmaz.
    // Koşullu rol de sayılır (`role={x ? 'button' : undefined}`) — ESLint
    // kuralıyla aynı küme.
    const n = count(/(?<=\s)role=(?:"button"|\{[^}]*['"`]button['"`][^}]*\})/g);
    expect(n, 'tıklanabilir span/div yerine Button/IconButton ya da btn-bare kullan').toBeLessThanOrEqual(MAX_ROLE_BUTTON);
  });
  it('gerekçeli istisna sayısı tavanı aşmaz', () => {
    const n = files.reduce((a, p) => a + readFileSync(p, 'utf8').split('\n').filter(l => REASONED.test(l)).length, 0);
    expect(n, 'istisna yerine atom kullan; gerçekten gerekiyorsa tavanı gerekçeyle artır').toBeLessThanOrEqual(MAX_REASONED_EXEMPTIONS);
  });
  it('elle .segmented kuran dosya sayısı tavanı aşmaz', () => {
    const n = files.filter(p => /className="segmented/.test(code.get(p)!)).length;
    expect(n, 'tek seçimli grup için SegmentedControl kullan').toBeLessThanOrEqual(MAX_SEGMENTED_FILES);
  });
  it('Traces toggle\'ları SegmentedControl üzerinden', () => {
    const s = stripTsComments(readFileSync(resolve(SRC, 'pages', 'Traces.tsx'), 'utf8'));
    expect(s).toMatch(/<SegmentedControl[^>]*aria-label="Grafik türü"/);
    for (const v of ['list', 'aggregate', 'shapes', 'volume', 'latency']) expect(s).toContain(`value: '${v}'`);
    expect(s).not.toMatch(/tl-go|tl-clear/);
  });
  it('gerekçeli istisna sayılmaz, gerekçesiz olan sayılır', () => {
    const D = 'eslint-' + 'disable-next-line ui/no-raw-button';
    const B = '<' + 'button';
    const reasoned = `{/* ${D} -- üçüncü parti widget kökü */}\n      ${B} type="button">x</button>`;
    const bare = `{/* ${D} */}\n      ${B} type="button">x</button>`;
    const n = (src: string) => stripTsComments(exemptReasoned(src)).match(/<button\b/g)?.length ?? 0;
    expect(n(reasoned)).toBe(0);
    expect(n(bare)).toBe(1);
  });
});
