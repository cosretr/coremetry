// @vitest-environment jsdom
//
// corePanelContracts.computedStyle — v0.10.1611 (shadcn Faz 1 R4, §12 Q4).
// CorePanel sözleşme testlerinin hesaplanmış-stil ayağı. index.css artık
// globals.css'i `layer(legacy)`e koyuyor; katmanlı her kural, @grafana/ui'nin
// çalışma anında enjekte ettiği KATMANSIZ emotion stillerine özgüllükten
// bağımsız yenilir. Bu yüzden uPlot lejantı (.u-*) ve CorePanel lejant
// kabı kuralları katmansız chart.css'e taşındı. Test, index.css'in import
// listesini tarayıcının kuracağı biçimde birleştirir (layer(x) → @layer x
// { … }) ve birkaç özelliği getComputedStyle ile okur: kural katmansız
// yerinde değilse — ya da biri onu yeniden legacy'ye taşırsa — değer düşer.
// jsdom @layer bloklarının içini kaskada katmıyor (ölçüldü), yani burada
// görünen değer YALNIZ katmansız bir kuraldan gelebilir.
import { describe, it, expect, beforeAll } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const STYLES = resolve(__dirname, '../../styles');

function assembleIndexCss(): string {
  const index = readFileSync(resolve(STYLES, 'index.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
  const parts: string[] = [];
  for (const m of index.matchAll(/@import\s+'([^']+)'\s*([^;]*);/g)) {
    const [, spec, rest] = m;
    if (spec.startsWith('tailwindcss/') || spec === './tokens.css') continue; // Tailwind yönergeleri; jsdom'un işi değil
    const text = readFileSync(resolve(STYLES, spec), 'utf8');
    const layer = /layer\((\w+)\)/.exec(rest)?.[1];
    parts.push(layer ? `@layer ${layer} {\n${text}\n}` : text);
  }
  return parts.join('\n');
}

function el(html: string, sel: string): CSSStyleDeclaration {
  const host = document.createElement('div');
  host.innerHTML = html;
  document.body.appendChild(host);
  return getComputedStyle(host.querySelector(sel)!);
}

describe('chart.css katmansız: .u-* ve CorePanel lejant kuralları hesaplanan stile ulaşıyor', () => {
  beforeAll(() => {
    const style = document.createElement('style');
    style.textContent = assembleIndexCss();
    document.head.appendChild(style);
  });

  it('.u-legend: yazı boyu 10px (uPlot 14px\'ini !important ile ezer)', () => {
    expect(el('<table class="u-legend"><tbody></tbody></table>', '.u-legend').fontSize).toBe('10px');
  });

  it('.u-legend tr.u-series.u-off: opaklık 0.45, satır imleci pointer', () => {
    const s = el('<table class="u-legend"><tbody><tr class="u-series"><td>a</td></tr><tr class="u-series u-off"><td>b</td></tr></tbody></table>', 'tr.u-off');
    expect(s.opacity).toBe('0.45');
    expect(s.cursor).toBe('pointer');
  });

  it('.mlc-chart .u-legend td.u-value: sağa yaslı, 52px taban', () => {
    const s = el('<div class="mlc-chart"><table class="u-legend"><tbody><tr class="u-series"><td class="u-value">1</td></tr></tbody></table></div>', 'td.u-value');
    expect(s.textAlign).toBe('right');
    expect(s.minWidth).toBe('52px');
  });

  it('.cp-legend-scroll (PanelLegend kabı): kaydırma + 184px tavan', () => {
    const s = el('<div class="cp-legend-scroll"></div>', '.cp-legend-scroll');
    expect(s.overflow).toBe('auto');
    expect(s.maxHeight).toBe('184px');
  });

  it('kontrol: legacy katmanındaki bir globals.css kuralı jsdom kaskadına girmiyor (testin ayırt ediciliği)', () => {
    // `.dash-row-header { display: flex }` globals.css'te (legacy). Görünür
    // olsaydı yukarıdaki değerlerin katmansız kaynaktan geldiği kanıtlanmazdı.
    expect(el('<div class="dash-row-header"></div>', '.dash-row-header').display).not.toBe('flex');
  });
});
