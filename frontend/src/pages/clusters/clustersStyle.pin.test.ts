// clustersStyle.pin.test.ts — Clusters S2 (v0.10.2167; plan
// docs/frontend/clusters-shadcn-plan.md §5). Kaynak pini.
//
// pages/clusters/* çizimlerinin statik stili satır içinden `.cl-*` sınıflarına
// (globals.css) taşındı. Çivilenen:
//   1. Bu modüllerde statik `style={{` bloğu ve renk literali yok; hesaplanan
//      değer (ölçü, doluluk, dilim rengi) adlandırılmış stil nesnesiyle gider.
//   2. Palet değişmedi (v0.10.922 K5): iz --border, normal doluluk / Running
//      dilimi nötr --text3, sapma --warn / --err; yeşil yok.
//   3. Bileşenin kullandığı her `cl-*` sınıfı globals.css'te tanımlı ve
//      sınıf bloğu renkleri yalnız token'dan alıyor.
import { describe, it, expect } from 'vitest';
import { readLegacyCss } from '@/styles/legacyCss';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { stripTsComments } from '@/styles/zLayers.test';

const read = (f: string) => stripTsComments(readFileSync(resolve(__dirname, f), 'utf8'));
// NamespaceCombobox bu listede yok: S5'te shadcn giydirilirken sıfırlanır.
const DRAWINGS = ['Gauge', 'MiniBar', 'NodeHeatmap', 'PhaseDonut', 'PodRing', 'PromQLList', 'TrendPanel', 'MetricArea'];
const css = readLegacyCss();
const blockStart = css.indexOf('/* ── /clusters çizimleri');
const block = css.slice(blockStart, css.indexOf('/* ── Copilot araç-çağrısı paneli', blockStart));

describe('Clusters S2 — çizim modüllerinde statik satır içi stil yok', () => {
  for (const name of DRAWINGS) {
    it(`${name}: style={{ yok, renk literali yok`, () => {
      const src = read(`${name}.tsx`);
      expect(src).not.toContain('style={{');
      expect(src).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
    });
  }

  it('hesaplanan değerler adlandırılmış stil nesnesiyle gider', () => {
    expect(read('MiniBar.tsx')).toContain('style={fillBox(shown, color ?? pctColor(shown))}');
    expect(read('Gauge.tsx')).toContain('style={squareBox(size)}');
    expect(read('NodeHeatmap.tsx')).toContain('style={dotFill(roleColor)}');
    expect(read('PhaseDonut.tsx')).toContain('style={swatch(seg.color)}');
  });
});

describe('Clusters S2 — palet aynı (K5)', () => {
  it('normal doluluk ve Running dilimi nötr; sapma warn / err; yeşil yok', () => {
    const th = read('thresholds.ts');
    expect(th).toContain("if (pct > 85) return 'var(--err)';");
    expect(th).toContain("if (pct > 65) return 'var(--warn)';");
    expect(read('PhaseDonut.tsx')).toContain("{ key: 'running', label: 'Running', color: 'var(--text3)' }");
    for (const name of DRAWINGS) expect(read(`${name}.tsx`), name).not.toContain('var(--ok)');
  });

  it('çubuk izi ve kadran halkası --border', () => {
    expect(block).toMatch(/\.cl-bar \{[^}]*background: var\(--border\)/);
    expect(read('Gauge.tsx')).toContain('stroke="var(--border)"');
    expect(read('PhaseDonut.tsx')).toContain('stroke="var(--border)"');
  });
});

describe('Clusters S2 — cl-* sınıfları tanımlı, renkler token', () => {
  it('blok bulundu ve dosyanın sonunda değil', () => {
    expect(blockStart).toBeGreaterThan(0);
    expect(block.length).toBeGreaterThan(500);
    expect(css.length - (blockStart + block.length)).toBeGreaterThan(10_000);
  });

  it('bileşenlerde geçen her cl-* sınıfı blokta tanımlı', () => {
    const used = new Set<string>();
    for (const name of DRAWINGS) {
      for (const m of read(`${name}.tsx`).matchAll(/className="([^"]*)"/g)) {
        for (const c of m[1].split(/\s+/)) if (c.startsWith('cl-')) used.add(c);
      }
    }
    expect(used.size).toBeGreaterThan(25);
    const missing = [...used].filter(c => !block.includes(`.${c}`));
    expect(missing).toEqual([]);
  });

  it('blokta renk literali yok', () => {
    expect(block).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
  });
});
