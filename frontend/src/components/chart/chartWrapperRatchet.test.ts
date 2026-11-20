// chartWrapperRatchet — grafik sarmalayıcı bileşen sayısı YALNIZ AŞAĞI iner.
//
// "Yeni grafik = CorePanelMulti" (frontend-design-system §7). Bu tarama,
// dışa açılan grafik-benzeri bileşenleri sayar: adı Chart / Spark /
// Sparkline / Histogram / Heatmap ile biten her `export function|const`
// + çizim motorları (CorePanel ailesi, TimeSeriesPanel, UPlotChart).
// Test dosyaları ve donmuş components/ui hariç.
//
// Yeni bir sarmalayıcı eklemek testi kırar: önce CorePanelMulti'nin
// karşılayamadığını göster, sonra tabanı gerekçeyle artır. Biri silinince
// test tabanı düşürmeni ister (ratchet).
import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const SRC = fileURLToPath(new URL('../../', import.meta.url));

// 2026-11-21 — 23 → 22: ölü ChartCard + OverviewChart silindi.
const BASELINE = 22;

const NAME = /^export (?:default )?(?:function|const) ([A-Z][A-Za-z0-9]*)\b/gm;
const IS_CHART = /(?:Chart|Spark|Sparkline|Histogram|Heatmap)$|^(?:CorePanel|CorePanelMulti|CorePanelWithFrames|TimeSeriesPanel|UPlotChart)$/;

function walk(dir: string, out: string[]): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !p.endsWith('.test.tsx')) out.push(p);
  }
  return out;
}

function chartWrappers(): string[] {
  const found: string[] = [];
  for (const f of walk(SRC, [])) {
    const rel = relative(SRC, f);
    if (rel.startsWith('components/ui/')) continue;
    for (const m of readFileSync(f, 'utf8').matchAll(NAME)) {
      if (IS_CHART.test(m[1])) found.push(`${rel}:${m[1]}`);
    }
  }
  return found.sort();
}

describe('grafik sarmalayıcı ratchet', () => {
  it(`sayı tabanı aşmaz (taban ${BASELINE}, yalnız aşağı)`, () => {
    const list = chartWrappers();
    expect(list.length, `Yeni grafik sarmalayıcı: CorePanelMulti kullan.\n${list.join('\n')}`)
      .toBeLessThanOrEqual(BASELINE);
    expect(list.length, `Sarmalayıcı silindi — BASELINE'ı ${list.length}'e düşür.`).toBe(BASELINE);
  });
});
