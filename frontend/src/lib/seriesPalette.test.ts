import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { SERIES_PALETTES, SERIES_SLOTS, assignSeriesSlots, preferredSlot, seriesColorsFor, inkOn } from './chartFmt';

// v0.10.510 (dış skill denetimi D7, mockup onaylı) — sekiz yuvalı, tema
// başına adımlanmış palet; durum renkleri paletin dışında; panel içi yuva
// ataması sekize kadar çakışmasız, ad tercihli, sabitlemeli.
// v0.10.920 (sade palet) — durum tokenları globals.css'ten OKUNUR (sabit
// tablo sessizce kayardı: inceleme turu, alt-dize araması yanlış blokta
// bulunan aynı hex'le yeşil kalıyordu).
const CSS_RAW = readFileSync(resolve(__dirname, '..', 'styles', 'globals.css'), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, '');
function themeBlock(theme: 'dark' | 'light' | 'redhat'): string {
  if (theme === 'dark') return /:root\s*\{([\s\S]*?)\n\}/.exec(CSS_RAW)![1];
  return new RegExp(`\\[data-theme="${theme}"\\]\\s*\\{([\\s\\S]*?)\\n\\}`).exec(CSS_RAW)![1];
}
function tok(theme: 'dark' | 'light' | 'redhat', name: string): string {
  const m = new RegExp(`${name}\\s*:\\s*(#[0-9a-fA-F]{6})\\s*;`).exec(themeBlock(theme));
  if (!m) throw new Error(`${theme} ${name} bulunamadı`);
  return m[1].toLowerCase();
}
const STATUS = {
  dark: ['--ok', '--warn', '--err'].map(t => tok('dark', t)),
  light: ['--ok', '--warn', '--err'].map(t => tok('light', t)),
  redhat: ['--ok', '--warn', '--err'].map(t => tok('redhat', t)),
};

// OKLab (Ottosson) Öklid mesafesi — algısal yakınlık. Eski palet durum
// renklerine 0.025-0.052 yakındı (yeşil/kırmızı seri "durum" gibi okunuyordu).
function oklab(hex: string): [number, number, number] {
  const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  const l = Math.cbrt(0.4122214708 * c[0] + 0.5363325363 * c[1] + 0.0514459929 * c[2]);
  const m = Math.cbrt(0.2119034982 * c[0] + 0.6806995451 * c[1] + 0.1073969566 * c[2]);
  const s = Math.cbrt(0.0883024619 * c[0] + 0.2817188376 * c[1] + 0.6299787005 * c[2]);
  return [0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s];
}
const dist = (a: string, b: string) => { const x = oklab(a), y = oklab(b); return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]); };

describe('SERIES_PALETTES', () => {
  it('her temada 8 tekil renk, durum tokenlarıyla çakışmaz', () => {
    for (const [theme, pal] of Object.entries(SERIES_PALETTES)) {
      expect(pal).toHaveLength(SERIES_SLOTS);
      expect(new Set(pal.map(c => c.toLowerCase())).size).toBe(SERIES_SLOTS);
      for (const st of STATUS[theme as keyof typeof STATUS]) expect(pal.map(c => c.toLowerCase())).not.toContain(st);
    }
  });
  it('hiçbir yuva durum rengine algısal olarak yakın değil (OKLab ≥ 0.10)', () => {
    for (const [theme, pal] of Object.entries(SERIES_PALETTES)) {
      for (const c of pal) for (const st of STATUS[theme as keyof typeof STATUS]) {
        expect(dist(c, st), `${theme} ${c} ~ ${st}`).toBeGreaterThanOrEqual(0.10);
      }
    }
  });
  it('durum renkleri gerçekten tema bloklarından okundu (boş tarama yok)', () => {
    for (const t of ['dark', 'light', 'redhat'] as const) expect(STATUS[t]).toHaveLength(3);
  });
});

describe('assignSeriesSlots', () => {
  const labels = ['api-gateway', 'payments', 'cart', 'checkout', 'inventory', 'search', 'auth', 'notifications'];
  it('sekiz seri, sekiz farklı yuva', () => {
    const m = assignSeriesSlots(labels);
    expect(new Set(m.values()).size).toBe(8);
    for (const l of labels) expect(m.get(l)).toBeGreaterThanOrEqual(0);
  });
  it('tercih edilen yuva boşsa aynen alınır', () => {
    const m = assignSeriesSlots(['payments']);
    expect(m.get('payments')).toBe(preferredSlot('payments'));
  });
  it('sabitleme: süzgeç seri sayısını değiştirince kalanlar yerinde kalır', () => {
    const first = assignSeriesSlots(labels);
    const fewer = labels.filter((_, i) => i % 2 === 0);
    const second = assignSeriesSlots(fewer, first);
    for (const l of fewer) expect(second.get(l)).toBe(first.get(l));
  });
  it('dokuzuncu seri dolaşır (çakışma kaçınılmaz, çağıran katlar)', () => {
    const m = assignSeriesSlots([...labels, 'ninth']);
    expect(m.size).toBe(9);
    expect(m.get('ninth')).toBe(preferredSlot('ninth'));
  });
  it('seriesColorsFor temaya göre renk verir', () => {
    const dark = seriesColorsFor(['a', 'b'], undefined, 'dark');
    const light = seriesColorsFor(['a', 'b'], undefined, 'light');
    expect(dark.get('a')).not.toBe(light.get('a'));
    expect(SERIES_PALETTES.dark).toContain(dark.get('a'));
    expect(SERIES_PALETTES.light).toContain(light.get('a'));
  });
});

// v0.10.920 — şelale bar etiketi mürekkebi (inkOn).
describe('inkOn', () => {
  // WCAG göreli parlaklık — inkOn'un seçtiği mürekkep gerçekten daha karşıt olanı mı?
  const L = (hex: string) => {
    const c = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255)
      .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  };
  const ratio = (a: string, b: string) => { const x = L(a), y = L(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  it('her palet renginde daha karşıt mürekkebi seçer', () => {
    for (const pal of Object.values(SERIES_PALETTES)) for (const c of pal) {
      const other = inkOn(c) === '#ffffff' ? '#15171a' : '#ffffff';
      expect(ratio(inkOn(c), c), c).toBeGreaterThanOrEqual(ratio(other, c));
    }
  });
  it.each([
    ['#06d8d9', '#15171a'], ['#b5b614', '#15171a'], ['#fca0e8', '#15171a'], ['#95b6fe', '#15171a'], ['#cb4f73', '#ffffff'],
    ['#045c93', '#ffffff'], ['#921545', '#ffffff'], ['#7b489e', '#ffffff'],
    ['var(--err)', '#ffffff'], ['not-a-colour', '#ffffff'],
  ])('%s → %s', (fill, want) => { expect(inkOn(fill)).toBe(want); });
});
