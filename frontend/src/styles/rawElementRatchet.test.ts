import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import ts from 'typescript';

// rawElementRatchet — v0.10.1614 (shadcn Faz 1 R6, plan §3.3).
//
// ESLint `ui/no-raw-element`in vitest ikizi (buttonUnityRatchet'in
// gerekçesiyle: yalnız vitest koşan yol da kapıdan geçmesin). Üç sayı yalnız
// AŞAĞI iner; bir sayfa dilimi kendi dosyalarını sıfırladığında tavan aynı
// commit'te düşer ve `npx eslint src --prune-suppressions` koşulur.
// Kapsam ESLint kuralıyla aynı: .tsx, testler hariç, components/ui
// (dondurulmuş atomlar), components/shadcn (üretilen) ve
// components/ai-elements (karar 8) hariç; components/coremetry DAHİL.
// <input> yalnız literal `type` ALLOWED_INPUT_TYPES içindeyse serbest
// (§12 Q3: file kalıcı; checkbox/radio shadcn karşılıkları kurulana dek;
// hidden/range/color kurulum kümesinde karşılıksız) — eslint.config.js ile
// aynı küme, aşağıdaki iddia ikisini eşitliyor.
// Taban (R6 commit'i, ölçüldü): input 350 · select 161 · textarea 33;
// eslint-suppressions.json toplamı 544 = üçünün toplamı (124 dosya).
// Serbest (bilgi): checkbox 129 + radio 7 = 136, file 7, range 1, color 1.
// R2 (Faz 2 dilim 1): checkbox serbest kümeden çıktı → MAX_RAW_CHECKBOX 129,
// eslint-suppressions.json toplamı 544 → 673.
const MAX_RAW_INPUT = 341; // Faz 2 dilim 1 R7: 350 → 344 (Remote Cluster); etkileşim P2: 344 → 341 (⌘K cmdk)
const MAX_RAW_SELECT = 159; // R7: 161 → 159
const MAX_RAW_TEXTAREA = 33;
// Faz 2 dilim 1 R2 (G10, operatör Q-IA7): shadcn checkbox kuruldu, `checkbox`
// serbest kümeden çıktı. Ham kutuların AYRI tavanı (MAX_RAW_INPUT hiç
// yükselmez); ölçüldü: R2 commit'i. Yalnız aşağı iner.
const MAX_RAW_CHECKBOX = 126; // R7: 129 → 126

const SRC = resolve(__dirname, '..');
const FE = resolve(SRC, '..');
const ALLOWED_INPUT_TYPES = ['file', 'radio', 'hidden', 'range', 'color'];

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const EXEMPT_DIRS = ['ui', 'shadcn', 'ai-elements'].map(d => join('components', d) + '/');
const files = walk(SRC).filter(p => !EXEMPT_DIRS.some(d => p.includes(d)));

// Sayım TypeScript AST'si üzerinden (ESLint'in gördüğü JSX öğeleriyle
// birebir): metin taraması yorum içindeki `<select>` sözcüğünü sayıyordu
// (ChannelModal, ters tırnaklı JSX yorumu — stripTsComments onu kaçırıyor).
// Gerekçeli satır istisnası (eslint.config.js'te belgeli) sayılmaz: öğenin
// başladığı satırın bir üstünde `disable-next-line ui/no-raw-element -- …`.
const REASONED = new RegExp('eslint-' + 'disable-next-line\\b[^\\n]*\\bui/no-raw-element\\b[^\\n]*\\s--\\s+\\S.{7,}');

type Hit = { el: 'input' | 'select' | 'textarea'; type: string | null };

/** Bir kaynaktaki ham form öğeleri (gerekçeli istisnalar hariç). */
export function rawFormElements(src: string, file = 'x.tsx'): Hit[] {
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const lines = src.split('\n');
  const out: Hit[] = [];
  const visit = (n: ts.Node) => {
    if (ts.isJsxOpeningElement(n) || ts.isJsxSelfClosingElement(n)) {
      const name = n.tagName.getText(sf);
      if (name === 'input' || name === 'select' || name === 'textarea') {
        const line = sf.getLineAndCharacterOfPosition(n.getStart(sf)).line;
        if (!(line > 0 && REASONED.test(lines[line - 1]))) {
          let type: string | null = null;
          for (const a of n.attributes.properties) {
            if (!ts.isJsxAttribute(a) || a.name.getText(sf) !== 'type' || !a.initializer) continue;
            const i = a.initializer;
            if (ts.isStringLiteral(i)) type = i.text;
            else if (ts.isJsxExpression(i) && i.expression && (ts.isStringLiteral(i.expression) || ts.isNoSubstitutionTemplateLiteral(i.expression))) type = i.expression.text;
          }
          out.push({ el: name, type });
        }
      }
    }
    ts.forEachChild(n, visit);
  };
  visit(sf);
  return out;
}

function counts() {
  let input = 0, checkbox = 0, radio = 0, otherAllowed = 0, select = 0, textarea = 0;
  for (const p of files) {
    for (const h of rawFormElements(readFileSync(p, 'utf8'), p)) {
      if (h.el === 'select') select++;
      else if (h.el === 'textarea') textarea++;
      else if (h.type === 'checkbox') checkbox++;
      else if (h.type === null || !ALLOWED_INPUT_TYPES.includes(h.type)) input++;
      else if (h.type === 'radio') radio++;
      else otherAllowed++;
    }
  }
  return { input, checkbox, radio, otherAllowed, select, textarea };
}

describe('ham form öğesi mandalı (ui/no-raw-element ikizi)', () => {
  const c = counts();

  it('tarama kaynak buluyor', () => {
    expect(files.length).toBeGreaterThan(300);
    expect(c.radio, 'radio sayımı (bilgi; kapı değil, Q3)').toBeGreaterThan(0);
  });
  it('ham <input> (serbest literal tipler hariç) tavanı aşmaz', () => {
    expect(c.input, 'Field bileşiği / shadcn input kullan').toBeLessThanOrEqual(MAX_RAW_INPUT);
  });
  it('ham <input type="checkbox"> tavanı aşmaz (R2, ayrı tavan)', () => {
    expect(c.checkbox, 'coremetry CheckboxField / shadcn Checkbox kullan').toBeLessThanOrEqual(MAX_RAW_CHECKBOX);
  });
  it('ham <select> tavanı aşmaz', () => {
    expect(c.select, 'shadcn native-select / select kullan').toBeLessThanOrEqual(MAX_RAW_SELECT);
  });
  it('ham <textarea> tavanı aşmaz', () => {
    expect(c.textarea, 'shadcn textarea kullan').toBeLessThanOrEqual(MAX_RAW_TEXTAREA);
  });

  it('ikiz eşitliği: eslint-suppressions.json toplamı = ölçülen dört sayının toplamı (R2: + checkbox)', () => {
    const sup = JSON.parse(readFileSync(resolve(FE, 'eslint-suppressions.json'), 'utf8')) as Record<string, Record<string, { count: number }>>;
    let total = 0;
    for (const rules of Object.values(sup)) total += rules['ui/no-raw-element']?.count ?? 0;
    expect(total).toBe(c.input + c.select + c.textarea + c.checkbox);
  });

  it('serbest tip kümesi eslint.config.js ile aynı', () => {
    const e = readFileSync(resolve(FE, 'eslint.config.js'), 'utf8');
    expect(e).toContain(`const ALLOWED_INPUT_TYPES = new Set([${ALLOWED_INPUT_TYPES.map(t => `'${t}'`).join(', ')}]);`);
  });

  it('ayrıştırıcı: literal tipler okunur, ifade null, yorum içi metin sayılmaz', () => {
    const t = (src: string) => rawFormElements(src).map(h => `${h.el}:${h.type}`);
    expect(t('const a = <input type="checkbox" />;')).toEqual(['input:checkbox']);
    expect(t('const a = <input type={"radio"} />;')).toEqual(['input:radio']);
    expect(t('const a = <input type={`range`} />;')).toEqual(['input:range']);
    expect(t('const a = <input type={x ? "checkbox" : "text"} />;')).toEqual(['input:null']);
    expect(t('const a = <div>{/* `x` native <select> kardeşi */}<textarea /></div>;')).toEqual(['textarea:null']);
  });

  it('gerekçeli istisna sayılmaz, gerekçesiz olan sayılır', () => {
    const D = 'eslint-' + 'disable-next-line ui/no-raw-element';
    expect(rawFormElements(`const a = (<div>\n{/* ${D} -- üçüncü parti widget kökü */}\n<input value="x" /></div>);`)).toEqual([]);
    expect(rawFormElements(`const a = (<div>\n{/* ${D} */}\n<input value="x" /></div>);`).length).toBe(1);
  });
});
