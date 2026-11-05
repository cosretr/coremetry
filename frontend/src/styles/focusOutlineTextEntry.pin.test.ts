// v0.10.1782 (operatör) — CoSRE sohbet girişi ve Search sayfasında yazarken
// beliren çerçeve: genel `:focus-visible` dış çizgi kuralı metin girişlerine
// de uygulanıyordu; kendi odak göstergesi olan kutularda (kompozer kartının
// focus-within halkası, .sf-wrap) ikinci çerçeve çiziyordu. Metin girişleri
// kenar rengiyle odak gösterir; kenarsız girişler (checkbox/radio/range/file)
// dış çizgiyi korur.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const G = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');

describe('v0.10.1782: metin girişleri :focus-visible dış çizgisi almaz', () => {
  it('genel dış çizgi kuralı yalnız kenarsız input türlerini alır, textarea\'yı hiç almaz', () => {
    const block = G.slice(G.indexOf('button:focus-visible,'), G.indexOf('outline-offset: 2px;'));
    for (const t of ['checkbox', 'radio', 'range', 'file', 'color']) expect(block).toContain(`input[type="${t}"]:focus-visible,`);
    expect(block).not.toMatch(/\ninput:focus-visible,/);
    expect(block).not.toContain('textarea:focus-visible');
  });
  it('metin girişlerinin odağı kenar rengi: input:focus + textarea:focus → --focus', () => {
    expect(G).toContain('input:where(:not([data-slot])):focus, select:where(:not([data-slot])):focus { border-color: var(--focus); }');
    expect(G).toContain('textarea:where(:not([data-slot])):focus { border-color: var(--focus); outline: none; }');
  });
});
