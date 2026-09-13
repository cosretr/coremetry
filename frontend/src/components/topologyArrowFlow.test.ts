// topologyArrowFlow.test.ts — v0.10.729 (operatör, prod service-map:
// "çizgiler kayıyor ama oklar sabit belki onlar da kaysa iyi olur. Ayrıca
// aynı sütun hizasındaki oklarda da karışıklık olabiliyor").
//
// Ne çiviliyor: (a) ok kendi kenarının yolu boyunca akar (offset-path),
// (b) hedef ucunda DAR bir aralıkta kalır (tam yol boyu akan ok komşu
// kolonlara girip "hangi kenarın oku" karışıklığını büyütürdü), (c) faz
// kenar indeksinden türer — yakınsayan kenarların okları aynı hizada
// yığılmasın, (d) offset-path desteklenmeyen tarayıcıda ve hareket-azaltma
// kipinde ok SABİT ve DOĞRU yerde durur (animation:none tek başına oku
// yolun başına düşürürdü).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
const src = readFileSync(resolve(__dirname, 'TopologyFlowGraph.tsx'), 'utf8');
const rule = (sel: string) => {
  const i = css.indexOf(sel + ' {');
  return i < 0 ? '' : css.slice(i + sel.length, css.indexOf('}', i));
};

describe('topoloji ok akışı (v0.10.729)', () => {
  it('yedek yol: offset-path yoksa ok bileşenin verdiği noktada sabit', () => {
    expect(rule('.topo-edge-arrow')).toMatch(/transform:\s*translate\(calc\(var\(--ax, 0\)/);
    expect(css).toContain('@supports (offset-path: path("M 0 0"))');
  });
  it('destekleyen tarayıcıda yol + dönüş + animasyon kenardan gelir', () => {
    const sup = css.slice(css.indexOf('@supports (offset-path: path("M 0 0"))'));
    expect(sup).toMatch(/offset-rotate:\s*auto;/);
    expect(sup).toMatch(/animation:\s*topo-arrow-flow var\(--arw-dur/);
    // Ters (çift yönlü) ok geriye bakar ve kendi keyframe'ini kullanır.
    expect(sup).toMatch(/\.topo-edge-arrow\.rev \{[^}]*offset-rotate:\s*auto 180deg/);
    expect(sup).toMatch(/\.topo-edge-arrow\.rev \{[^}]*animation-name:\s*topo-arrow-flow-rev/);
  });
  it('aralık DAR ve hedef ucunda (58→88 ileri, 42→12 geri)', () => {
    expect(css).toContain('@keyframes topo-arrow-flow     { from { offset-distance: 58%; } to { offset-distance: 88%; } }');
    expect(css).toContain('@keyframes topo-arrow-flow-rev { from { offset-distance: 42%; } to { offset-distance: 12%; } }');
  });
  it('hareket-azaltma: animasyon kapalı AMA ok hedef ucunda kalır', () => {
    const block = css.slice(css.indexOf('@media (prefers-reduced-motion: reduce) {\n  /* Hareket kapalıyken'));
    expect(block).toMatch(/\.topo-edge-arrow \{ animation: none; offset-distance: 78%; \}/);
    expect(block).toMatch(/\.topo-edge-arrow\.rev \{ offset-distance: 22%; \}/);
  });
  it('bileşen: sınıf + yol + süre + indeksten türeyen gecikme; statik transform ÖZNİTELİĞİ yok', () => {
    expect(src).toContain("className={j === 0 ? 'topo-edge-arrow' : 'topo-edge-arrow rev'}");
    // Yol satır içinde: kenarın d'si ile AYNI eğri (ok çizgiden ayrılmasın).
    expect(src).toContain("offsetPath: `path(\"M ${a.x} ${a.y} C ${mx} ${a.y}, ${mx} ${b.y}, ${b.x} ${b.y}\")`");
    expect(src).toContain("d={`M ${a.x} ${a.y} C ${mx} ${a.y}, ${mx} ${b.y}, ${b.x} ${b.y}`}");
    expect(src).toContain("['--arw-dur' as string]: `${(2.8 - 1.8 * t).toFixed(2)}s`");
    expect(src).toMatch(/--arw-delay[^\n]*i \* 37/);
    // Ok artık transform ÖZNİTELİĞİ taşımıyor (offset-path ile çakışırdı).
    expect(src).not.toContain('transform={`translate(${ar.x} ${ar.y}) rotate(${ar.angle})`}');
    // Kenar çizgisinin akışı ve ok akışı AYNI süreyi okur (tempo ayrışmasın).
    expect(src).toContain("animationDuration: `${(2.8 - 1.8 * t).toFixed(2)}s`");
  });
});
