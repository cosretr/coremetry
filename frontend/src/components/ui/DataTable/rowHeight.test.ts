// rowHeight.test — v0.10.933 (tablo standardı T6).
//
// Sanal tablo satırı JS'te `ROW_H` ile tahmin eder (estimateSize + tr
// yüksekliği), CSS aynı satırı `--row-h` ile çizer (`.vt-scroll … .row-link`
// yüksekliği, `.cv-row` tahmini). İkisi ayrışırsa Traces'te iç kaydırma
// çubuğu çıkar (v0.10.225 olayı) ya da scrollbar zıplar. Kapı: rahat
// yoğunluğun `--row-h`i ve `.vt-scroll`un geri sabitlediği değer ROW_H.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { ROW_H } from './rowHeight';

const CSS = readFileSync(resolve(__dirname, '../../../styles/globals.css'), 'utf8')
  .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));
const body = (selector: string): string => {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = new RegExp(`(?:^|[};])\\s*${esc}\\s*\\{([^}]*)\\}`, 'm').exec(CSS);
  if (!m) throw new Error(`kural yok: ${selector}`);
  return m[1];
};
const rowH = (b: string) => /--row-h\s*:\s*(\d+)px/.exec(b)?.[1];

describe('T6 — satır ritmi tek sabit', () => {
  it('ROW_H 36 (evin satırı)', () => {
    expect(ROW_H).toBe(36);
  });

  it(':root --row-h (rahat) === ROW_H', () => {
    expect(rowH(body(':root'))).toBe(String(ROW_H));
  });

  it('.vt-scroll --row-h === ROW_H (virtualizer yoğunluğu görmez)', () => {
    expect(rowH(body('.vt-scroll'))).toBe(String(ROW_H));
  });

  it('compact/dense kendi --row-h değerini bildirir ve rahattan KÜÇÜK', () => {
    for (const d of ['compact', 'dense']) {
      const v = rowH(body(`[data-density="${d}"]`));
      expect(v, `${d} --row-h yok`).toBeDefined();
      expect(Number(v)).toBeLessThan(ROW_H);
    }
  });

  it('.cv-row tahmini token\'dan, literal değil', () => {
    const b = body('.cv-row');
    expect(b).toMatch(/content-visibility:\s*auto/);
    expect(b).toMatch(/contain-intrinsic-size:\s*auto var\(--row-h\)/);
  });

  it('VirtualTable varsayılanı ROW_H (literal 36 yok)', () => {
    const src = readFileSync(resolve(__dirname, 'VirtualTable.tsx'), 'utf8');
    expect(src).toContain('rowHeight = ROW_H');
    expect(src).not.toMatch(/rowHeight = 36\b/);
  });
});
