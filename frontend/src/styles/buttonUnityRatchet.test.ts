import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';

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
const SRC = resolve(__dirname, '..');
const MAX_RAW_BUTTONS = 125;
const MAX_SEGMENTED_FILES = 13;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}
const files = walk(SRC).filter(p => !p.includes(join('components', 'ui') + '/'));

describe('buton bütünlüğü mandalı', () => {
  it('ham <button sayısı tavanı aşmaz', () => {
    const n = files.reduce((a, p) => a + (readFileSync(p, 'utf8').match(/<button\b/g)?.length ?? 0), 0);
    expect(n, 'yeni düğme için Button/IconButton/Chip/SegmentedControl kullan').toBeLessThanOrEqual(MAX_RAW_BUTTONS);
  });
  it('elle .segmented kuran dosya sayısı tavanı aşmaz', () => {
    const n = files.filter(p => /className="segmented/.test(readFileSync(p, 'utf8'))).length;
    expect(n, 'tek seçimli grup için SegmentedControl kullan').toBeLessThanOrEqual(MAX_SEGMENTED_FILES);
  });
  it('Traces toggle\'ları SegmentedControl üzerinden', () => {
    const s = readFileSync(resolve(SRC, 'pages', 'Traces.tsx'), 'utf8');
    expect(s).toMatch(/<SegmentedControl[^>]*aria-label="Grafik türü"/);
    for (const v of ['list', 'aggregate', 'shapes', 'volume', 'latency']) expect(s).toContain(`value: '${v}'`);
    expect(s).not.toMatch(/tl-go|tl-clear/);
  });
});
