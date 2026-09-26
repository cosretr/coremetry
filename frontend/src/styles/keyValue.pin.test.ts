// keyValue.pin — v0.10.939 (tablo standardı T1, dilim 2: KeyValue atomu).
//
// Operatör onayı: docs/DECISIONS.md "Tablo standardı"; mockup "Anahtar/değer"
// bölümü. Atomun DOM sözleşmesi `components/ui/KeyValue.contract.test.tsx`te;
// burada GÖRÜNÜM kararları çivileniyor — hiçbiri tsc/eslint/colorLeaks'e
// takılmaz: `.keyval__val`e `word-break: break-all` yazmak geçerli CSS'tir,
// ekranda yalnız kimlikler harf ortasından kırılır; açığa çıkarma kuralından
// `:focus-within`i silmek de klavyeyle gezen operatöre görünmez düğme bırakır.
//   • tek etiket genişliği token'ı (`--kv-label-w`) + genişletme basamağı,
//   • etiket --text2 ve büyük harf yok; değer --text; mono yalnız işaretli,
//   • sarma `overflow-wrap: anywhere`, ASLA `break-all`,
//   • satır ayracı --divider, zebra/hover zemini/dış çerçeve yok (T2, T10),
//   • eylem yuvası hover + :focus-within'de, dokunmatikte hep görünür,
//   • atomun bastığı her sınıf tanımlı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
// Yorumları BOŞALT (paletteStep2 / tableStandard deseni): gerekçe yorumları
// kural adlarını düz metin olarak anıyor.
const CLEAN = CSS.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string }
const RULES: Rule[] = [...CLEAN.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
  selectors: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')),
  body: m[2],
}));
const rulesFor = (sel: string) => RULES.filter(r => r.selectors.includes(sel));
const bodyOf = (sel: string): string => {
  const r = rulesFor(sel);
  if (!r.length) throw new Error(`kural yok: ${sel} (keyvalue.css globals.css'e birleştirildi mi?)`);
  return r.map(x => x.body).join(';');
};
/** `.keyval` ailesinin (ve yoğunluk kapsamlarının) bütün kuralları. */
const familyBodies = () => RULES.filter(r => r.selectors.some(s => /\.keyval(?![\w-])|\.keyval(__|--)/.test(s)));

describe('T1 — KeyValue görünümü', () => {
  it('tek etiket genişliği token\'ı: varsayılan + wide basamağı, ızgara onu okur', () => {
    expect(bodyOf('.keyval')).toMatch(/--kv-label-w:\s*\d+px/);
    expect(bodyOf('.keyval--wide')).toMatch(/--kv-label-w:\s*\d+px/);
    const def = Number(/--kv-label-w:\s*(\d+)px/.exec(bodyOf('.keyval'))![1]);
    const wide = Number(/--kv-label-w:\s*(\d+)px/.exec(bodyOf('.keyval--wide'))![1]);
    expect(wide, 'wide varsayılandan geniş değil').toBeGreaterThan(def);
    expect(bodyOf('.keyval__row')).toMatch(/grid-template-columns:[^;]*var\(--kv-label-w\)[^;]*minmax\(0,\s*1fr\)/);
  });

  it('etiket --text2, büyük harf / harf aralığı yok; değer --text', () => {
    const k = bodyOf('.keyval__k');
    expect(k).toMatch(/color:\s*var\(--text2\)/);
    for (const r of familyBodies()) {
      expect(r.body, r.selectors.join(', ')).not.toMatch(/text-transform/);
      expect(r.body, r.selectors.join(', ')).not.toMatch(/letter-spacing/);
    }
    expect(bodyOf('.keyval__v')).toMatch(/color:\s*var\(--text\)/);
  });

  it('mono yalnız işaretli değerde ve TEK yığından (--font-mono)', () => {
    expect(bodyOf('.keyval__val--mono')).toMatch(/font-family:\s*var\(--font-mono\)/);
    const monoRules = familyBodies().filter(r => /font-family/.test(r.body)).map(r => r.selectors.join(', '));
    expect(monoRules).toEqual(['.keyval__val--mono']);
  });

  it('uzun değer ve etiket `overflow-wrap: anywhere` ile sarar; ailede `break-all` YOK', () => {
    expect(bodyOf('.keyval__val')).toMatch(/overflow-wrap:\s*anywhere/);
    expect(bodyOf('.keyval__k')).toMatch(/overflow-wrap:\s*anywhere/);
    for (const r of familyBodies()) expect(r.body, r.selectors.join(', ')).not.toMatch(/break-all/);
  });

  it('boş değer soluk (--text3, devre dışı tonu --text-faint DEĞİL)', () => {
    expect(bodyOf('.keyval__val--empty')).toMatch(/color:\s*var\(--text3\)/);
  });

  it('satır ayracı --divider; son satır çizgisiz (:last-of-type — Tooltip kardeşi :last-child\'ı bozar)', () => {
    expect(bodyOf('.keyval__row')).toMatch(/border-bottom:\s*1px solid var\(--divider\)/);
    expect(bodyOf('.keyval__row:last-of-type')).toMatch(/border-bottom:\s*0/);
  });

  it('dış çerçeve, zebra, satır hover zemini ve el imleci YOK (T2, T10)', () => {
    expect(bodyOf('.keyval')).not.toMatch(/border|background|border-radius/);
    const bad = familyBodies()
      .filter(r => r.selectors.some(s => /:nth-(child|of-type)|\.keyval__row:hover(?!\s)/.test(s))
        || /background|cursor/.test(r.body))
      .map(r => r.selectors.join(', '));
    expect(bad).toEqual([]);
  });

  it('eylem yuvası gizli; satır hover\'ı VE :focus-within açar', () => {
    expect(bodyOf('.keyval__acts')).toMatch(/opacity:\s*0/);
    expect(bodyOf('.keyval__row:hover .keyval__acts')).toMatch(/opacity:\s*1/);
    expect(bodyOf('.keyval__row:focus-within .keyval__acts'), 'klavye odağı gizli düğmeye iniyor').toMatch(/opacity:\s*1/);
  });

  it('dokunmatikte (hover: none) eylem yuvası hep görünür', () => {
    const blocks = [...CLEAN.matchAll(/@media \(hover: none\) \{([\s\S]*?)\n\}/g)].map(m => m[1]);
    expect(blocks.some(b => /\.keyval__acts\s*\{[^}]*opacity:\s*1/.test(b)), '(hover: none) .keyval__acts kuralı yok').toBe(true);
  });

  it('yoğunluk ailenin dolgusuna ve yazı boyuna ulaşır', () => {
    for (const d of ['compact', 'dense']) {
      const b = bodyOf(`[data-density="${d}"] .keyval`);
      expect(b, d).toMatch(/--kv-pad-y:/);
      expect(b, d).toMatch(/font-size:/);
    }
    const cell = bodyOf('.keyval__k');
    expect(cell).toMatch(/padding:\s*var\(--kv-pad-y\)\s+var\(--kv-pad-x\)/);
  });

  it('atomun bastığı her sınıf globals.css\'te tanımlı', () => {
    const src = readFileSync(resolve(__dirname, '../components/ui/KeyValue.tsx'), 'utf8')
      .replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
    const emitted = new Set([...src.matchAll(/'(keyval[\w-]*)'|"(keyval[\w-]*)"/g)].map(m => m[1] ?? m[2]));
    expect(emitted.size, 'atomun sınıfları bulunamadı — desen kaymış').toBeGreaterThanOrEqual(8);
    const defined = new Set([...CLEAN.matchAll(/\.(keyval[\w-]*)/g)].map(m => m[1]));
    expect([...emitted].filter(c => !defined.has(c))).toEqual([]);
  });
});
