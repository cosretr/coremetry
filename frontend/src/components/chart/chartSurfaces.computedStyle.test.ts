// @vitest-environment jsdom
//
// chartSurfaces.computedStyle — v0.10.1613 (shadcn Faz 1 R5; operatör).
// R5 Tailwind'e ilk kaynakları (@source: shadcn / ai-elements / coremetry)
// veriyor; üç grafik yüzeyinin stili kaskattan GEÇMEYE devam etmeli:
// MultiLineChart lejantı, gecikme ısı haritası (LatencyHeatmap) ve bir
// histogram (LogsHistogram → TimeChart uPlot lejantı). Grafik kodu
// DEĞİŞMİYOR; burada yalnız onların ürettiği DOM biçimi + gerçek CSS var.
//
// İki birleştirme, corePanelContracts.computedStyle.test.ts'in yöntemiyle:
//  (A) TARAYICI: index.css import listesi `layer(x)` → `@layer x { … }`.
//      jsdom @layer içini kaskada katmıyor (ölçüldü, R4) → görülen değer
//      YALNIZ katmansız chart.css'ten gelebilir. chart.css'in kuralları
//      emotion'ı ve tüm katmanları (utilities dahil) yener — sınanan bu.
//  (B) LEGACY YALNIZ: uPlot.min.css + globals.css katmansız, bugünkü sırada.
//      legacy katmanının KENDİ içindeki kaskad budur (katman içinde özgüllük
//      + sıra geçerli); eski kuralların grafik öğelerine ulaştığını gösterir.
// Tailwind yardımcıları ile eski sınıf adlarının çakışması bu testin işi
// değil: build çıktısında scripts/bundle-budget.mjs ölçüyor (izin listesi).
import { describe, it, expect, beforeAll, afterAll } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const STYLES = resolve(__dirname, '../../styles');
const FE = resolve(__dirname, '../../..');
const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '');
const imports = () =>
  [...strip(readFileSync(resolve(STYLES, 'index.css'), 'utf8')).matchAll(/@import\s+'([^']+)'\s*([^;]*);/g)]
    .filter(([, spec]) => !spec.startsWith('tailwindcss/') && spec !== './tokens.css')
    .map(([, spec, rest]) => ({ text: readFileSync(resolve(STYLES, spec), 'utf8'), layer: /layer\((\w+)\)/.exec(rest)?.[1] }));

function sheet(css: string): HTMLStyleElement {
  const s = document.createElement('style');
  s.textContent = css;
  document.head.appendChild(s);
  return s;
}
function el(html: string, sel: string): CSSStyleDeclaration {
  const host = document.createElement('div');
  host.innerHTML = html;
  document.body.appendChild(host);
  return getComputedStyle(host.querySelector(sel)!);
}

// uPlot'un lejant tablosu (MultiLineChart / TimeChart aynı biçimi üretir).
const LEGEND = '<table class="u-legend"><tbody>'
  + '<tr class="u-series"><th>Time</th><td class="u-value">t</td></tr>'
  + '<tr class="u-series"><th><div class="u-marker"></div>svc-a</th><td class="u-value">12</td></tr>'
  + '<tr class="u-series u-off"><th>svc-b</th><td class="u-value">3</td></tr>'
  + '</tbody></table>';

describe('(A) tarayıcı kaskadı: grafik lejant kuralları katmansız ve ulaşıyor', () => {
  let s: HTMLStyleElement;
  beforeAll(() => { s = sheet(imports().map(p => (p.layer ? `@layer ${p.layer} {\n${p.text}\n}` : p.text)).join('\n')); });
  afterAll(() => { s.remove(); document.body.innerHTML = ''; });

  it('MultiLineChart tablo lejantı: tam genişlik, ad sola, değer sağa + 52px', () => {
    const host = `<div class="mlc-chart">${LEGEND}</div>`;
    expect(el(host, '.u-legend').width).toBe('100%');
    expect(el(host, 'tr:nth-child(2) th').textAlign).toBe('left');
    const v = el(host, 'tr:nth-child(2) td.u-value');
    expect(v.textAlign).toBe('right');
    expect(v.minWidth).toBe('52px');
  });

  it('MultiLineChart LİSTE lejantı: satırlar sarılan flex, sayı sütunu gizli, satır tavanı 240px', () => {
    const host = `<div class="mlc-chart mlc-legend-list">${LEGEND}</div>`;
    const tbody = el(host, 'tbody');
    expect(tbody.display).toBe('flex');
    expect(tbody.flexWrap).toBe('wrap');
    expect(el(host, 'tr:nth-child(2) td.u-value').display).toBe('none');
    const tr = el(host, 'tr:nth-child(2)');
    expect(tr.display).toBe('inline-flex');
    expect(tr.maxWidth).toBe('240px');
  });

  it('histogram (LogsHistogram → TimeChart uPlot lejantı, .mlc-chart DIŞI): genel kurallar var, mlc kapsamı sızmıyor', () => {
    const host = `<div class="lh">${LEGEND}</div>`;
    expect(el(host, '.u-legend').fontSize).toBe('10px');
    expect(el(host, 'tr:first-child').display).toBe('none'); // x-ekseni satırı gizli
    expect(el(host, 'tr.u-off').opacity).toBe('0.45');
    expect(el(host, '.u-marker').width).toBe('8px');
    const v = el(host, 'tr:nth-child(2) td.u-value');
    expect(v.textAlign).not.toBe('right');
    expect(v.minWidth).not.toBe('52px');
  });

  it('ısı haritası (LatencyHeatmap canvas): hiçbir katmansız kural canvas boyunu ezmiyor', () => {
    // Isı haritası kendi canvas'ını inline stil + CSS boyutuyla çizer; katmansız
    // tek kaynak chart.css ve orada canvas kuralı yok → jsdom'da max-width ilk değer (none).
    expect(el('<div><canvas style="width: 640px; height: 180px"></canvas></div>', 'canvas').maxWidth).toBe('none');
    expect(el('<div><canvas style="width: 640px; height: 180px"></canvas></div>', 'canvas').width).toBe('640px');
  });

  it('kontrol: legacy katmanındaki kural jsdom kaskadına girmiyor (ayırt edicilik)', () => {
    expect(el('<div class="lh-left"></div>', '.lh-left').display).not.toBe('flex');
  });
});

describe('(B) legacy katmanı kendi içinde: eski grafik kuralları öğeye ulaşıyor', () => {
  let s: HTMLStyleElement;
  beforeAll(() => {
    const legacy = imports().filter(p => p.layer === 'legacy');
    expect(legacy.length).toBe(2); // uPlot.min.css + globals.css, bu sırayla
    expect(legacy[0].text).toBe(readFileSync(resolve(FE, 'node_modules/uplot/dist/uPlot.min.css'), 'utf8'));
    s = sheet(legacy.map(p => p.text).join('\n'));
  });
  afterAll(() => { s.remove(); document.body.innerHTML = ''; });

  it('histogram başlığı .lh-left: sarılan flex', () => {
    const s2 = el('<div class="lh-left"></div>', '.lh-left');
    expect(s2.display).toBe('flex');
    expect(s2.flexWrap).toBe('wrap');
    expect(s2.alignItems).toBe('center');
  });

  it('ısı haritası canvas\'ı: globals `canvas { max-width: 100% }` ulaşıyor', () => {
    expect(el('<div><canvas></canvas></div>', 'canvas').maxWidth).toBe('100%');
  });

  it('uPlot taban kuralı: .u-over mutlak konumlu (kardeş grafik imleç katmanı)', () => {
    expect(el('<div class="uplot"><div class="u-over"></div></div>', '.u-over').position).toBe('absolute');
  });
});
