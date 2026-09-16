// excPodsLayout.test.ts — v0.10.173 → v0.10.734.
// 173 (operatör): «Pods · nodes» en alta (12 pod'luk tablo sayfayı itiyordu),
// tablo taşmasız (fixed + colgroup; ellipsis + title).
// 734 (operatör onaylı mockup a42a0b31, 2026-09-16): panel SAĞ KOLONA, Sample
// traces'in ÜSTÜNE (stack trace adası); 173'ün kaygısı ilk 8 pod + "tümü (N) ▸"
// ile korunur; oluşum çubuğu; uzun stack katlı; first/last seen çipleri kalktı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('exception detayı Pods · nodes (v0.10.173 → v0.10.734)', () => {
  it('panel sağ kolonda: Stack trace kartından SONRA, Sample traces kartından ÖNCE, aynı ızgarada', () => {
    const src = read('./ProblemDetail.tsx');
    const pods = src.indexOf('<ExceptionPodsPanel');
    const samples = src.indexOf('<h3>Sample traces</h3>');
    const stack = src.indexOf('<h3>Stack trace</h3>');
    const grid = src.indexOf('<div className="pd-cols pd-cols-14">');
    expect(grid).toBeGreaterThan(-1);
    expect(pods).toBeGreaterThan(stack);
    expect(pods).toBeLessThan(samples);
    expect(pods).toBeGreaterThan(grid);
    // Tek mount (altta ikinci kopya yok).
    expect(src.indexOf('<ExceptionPodsPanel', pods + 1)).toBe(-1);
  });
  it('ilk 8 pod + tümü; oluşum çubuğu; kısa alt yazı (ayrıntı title\'da)', () => {
    const src = read('./ExceptionPodsPanel.tsx');
    expect(src).toContain('const PODS_SHOW = 8;');
    expect(src).toContain('rows.slice(0, PODS_SHOW)');
    expect(src).toContain('tümü (${rows.length}) ▸');
    expect(src).toContain('<div className="exc-share">');
    expect(src).toContain('<span className="ov-sub" title={scanNote}>');
    expect(src).not.toContain('scanned occurrences with pod context');
    const css = read('../../styles/globals.css');
    expect(css).toMatch(/\.exc-share \{ position: relative;/);
  });
  it('tablo sabit yerleşimli, colgroup\'lu; pod/node/tarih hücreleri ellipsis sınıfı + title taşır; namespace/cluster ayrı kolon değil', () => {
    const src = read('./ExceptionPodsPanel.tsx');
    expect(src).toContain('<table className="exc-pods-t">');
    expect(src).toContain('<colgroup>');
    expect((src.match(/className="mono exc-pods-cell" title=/g) ?? []).length).toBe(3);
    expect(src).not.toContain('<th>namespace</th>');
    expect(src).toContain('exc-pods-sub');
    const css = read('../../styles/globals.css');
    expect(css).toMatch(/\.exc-pods-t \{ table-layout: fixed;/);
    expect(css).toMatch(/\.exc-pods-t th, \.exc-pods-t td \{[^}]*text-overflow: ellipsis/);
    // yüzdeler toplam 100 → taşma yok (v0.10.174); 734: beş sütun (Traces düğmesi kalır — operatör)
    const pct = [...css.matchAll(/\.exc-pods-c-[a-z]+ \{ width: (\d+)%; \}/g)].map(m => Number(m[1]));
    expect(pct.length).toBe(5);
    expect(src).toContain('<td><Link to={tracesHref} className="sec" title="Bu pod\'un hatalı trace\'leri">Traces</Link></td>');
    expect(pct.reduce((a, b) => a + b, 0)).toBe(100);
  });
  it('uzun stack katlı: eşik 20, ilk 12; Copy tamamını kopyalar; first/last seen çipleri yok', () => {
    const src = read('./ProblemDetail.tsx');
    expect(src).toContain('const STACK_FOLD_AT = 20;');
    expect(src).toContain('const STACK_FOLD_SHOW = 12;');
    expect(src).toContain('shownStackLines.map((l, i) =>');
    expect(src).toContain('onClick={copyStack} disabled={!stack}');
    expect(src).toContain('<div className="ex-fold">');
    expect(src).not.toContain('<span className="k">first seen</span>');
    expect(src).not.toContain('<span className="k">last seen</span>');
    const css = read('../../styles/globals.css');
    expect(css).toMatch(/\.ex-fold \{ display: flex;/);
  });
});

// v0.10.734 (operatör: "başladığı bitti net gözükebilir, sağdan soldan margin")
describe('Occurrences over time — kenar payı', () => {
  it('x ekseni iki yanda payla mıhlı (%5, en az 2 bucket); TimeChart xRange alır', () => {
    const src = read('./ProblemDetail.tsx');
    expect(src).toContain('const OCC_EDGE_PAD = 0.05;');
    expect(src).toContain('Math.max((to - from) * OCC_EDGE_PAD, (occTimes[1] - occTimes[0]) * 2)');
    expect(src).toContain('xRange={occXRange}');
  });
  it('zaman ekseni: 140 px + her tik tarih+saat (fmtOccTick)', () => {
    const src = read('./ProblemDetail.tsx');
    expect(src).toContain('height={140} regions={probRegions}');
    expect(src).toContain('fmtX={fmtOccTick}');
  });
  it('fmtOccTick saf: gün.ay saat:dakika', async () => {
    const { fmtOccTick } = await import('./occTick');
    const t = new Date(2026, 8, 13, 11, 35, 0).getTime() / 1000; // yerel saat
    expect(fmtOccTick(t)).toBe('13.09 11:35');
  });
});
