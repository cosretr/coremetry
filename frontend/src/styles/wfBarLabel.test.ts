import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.356 — Operator-reported: şelalede öz-süre gölgesi (.wf-bar-self,
// position:absolute) statik .wf-bar-label'ın üstüne boyanınca süre yazısı
// sönük kalıyordu. Etiket gölgenin üstünde (relative + z-index) ve beyaz.
describe('.wf-bar-label öz-süre gölgesinin üstünde ve beyaz (v0.10.356)', () => {
  const css = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
  const rule = css.match(/^\.wf-bar-label \{([^}]*)\}/m);
  it('kural var ve katmanı gölgeden üstte', () => {
    expect(rule).not.toBeNull();
    expect(rule![1]).toMatch(/position:\s*relative/);
    expect(rule![1]).toMatch(/z-index:\s*[1-9]/);
    expect(rule![1]).toMatch(/color:\s*var\(--on-accent\)/);
  });
  it('gölge etikete z-index ile geçmiyor', () => {
    const self = css.match(/^\.wf-bar-self \{([^}]*)\}/m);
    expect(self).not.toBeNull();
    expect(self![1]).not.toMatch(/z-index/);
  });
});

// v0.10.920 — etiket mürekkebi bar rengine göre seçilir; CSS varsayılanı
// (beyaz) yalnız koyu barlar için kalır.
describe('şelale bar etiketi mürekkebi (v0.10.920)', () => {
  it('TraceWaterfall etikette inkOn kullanıyor', () => {
    const src = readFileSync(resolve(__dirname, '..', 'components', 'TraceWaterfall.tsx'), 'utf8');
    expect(src).toContain('const ink = inkOn(color);');
    expect(src).toMatch(/className="wf-bar-label"[\s\S]{0,120}ink === '#ffffff'/);
    // öz-süre gölgesi mürekkepten uzaklaşır (inceleme turu: açıkta koyu mürekkebi gömüyordu)
    expect(src).toMatch(/className="wf-bar-self"[\s\S]{0,300}ink === '#ffffff'/);
  });
});
