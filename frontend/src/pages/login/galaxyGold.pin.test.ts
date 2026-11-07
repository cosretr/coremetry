// v0.10.1929 (operatör-raporlu: kurumsal temada login galaksisinin akan
// noktaları altın gibi görünmüyor) — 1928 `--warn` kullanıyordu; açık
// temalarda o kahverengi METİN tonu (#795600). Galaksinin kendi altın token'ı
// her temada altın olmalı ve canvas onu okumalı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');
const block = (sel: string) => { const i = css.indexOf(sel + ' {'); expect(i).toBeGreaterThanOrEqual(0); return css.slice(i, css.indexOf('\n}', i)); };
const gold = (b: string) => /--galaxy-gold:\s*(#[0-9a-f]{6})/i.exec(b)?.[1].toLowerCase();

// Altın: kırmızı yüksek, yeşil orta-yüksek, mavi düşük; açık zeminde kahverengiye kaçmasın (parlaklık).
function isGold(hex: string, minLum: number) {
  const r = parseInt(hex.slice(1, 3), 16), g = parseInt(hex.slice(3, 5), 16), b = parseInt(hex.slice(5, 7), 16);
  const lum = (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
  return r > g && g > b && r - b > 100 && lum >= minLum;
}

describe('login galaksisi altın token', () => {
  it('koyu (varsayılan) ve açık temalarda tanımlı ve altın', () => {
    expect(isGold(gold(css.slice(css.indexOf(':root {'), css.indexOf('\n}', css.indexOf(':root {'))))!, 0.55)).toBe(true);
    for (const t of ['[data-theme="light"]', '[data-theme="redhat"]', '[data-theme="stone"]']) {
      const v = gold(block(t));
      expect(v, t).toBeDefined();
      expect(isGold(v!, 0.6), `${t} ${v}`).toBe(true);
    }
  });
  it('canvas akan noktaları --galaxy-gold okur (--warn değil)', () => {
    const src = readFileSync(resolve(__dirname, 'TopologyBackdrop.tsx'), 'utf8');
    expect(src).toContain("flow: resolveVar('var(--galaxy-gold)')");
    expect(src).not.toContain("resolveVar('var(--warn)')");
  });
});
