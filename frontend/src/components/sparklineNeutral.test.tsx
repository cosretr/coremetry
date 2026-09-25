// @vitest-environment jsdom
// sparklineNeutral.test.tsx — v0.10.922 (sade palet adım 1, operatör onayı
// 2026-09-25). Renk yalnız SAPMA için (K5): düz sıfır çizgisi çağıranın
// rengini (hata serisinin --err'i) taşımaz, nötr --text3 çizilir. Buna
// karşılık gerçek sapma sinyalleri — bars modunun eşik-tabanlı err/warn
// kovaları ve area modunun eşik-geçme kırmızısı — AYNEN korunur. Delta
// çipi yön rengi (sabit rgb kırmızı/yeşil) taşımaz; yön ok + metinde.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { Sparkline } from './Sparkline';

let host: HTMLDivElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

describe('Sparkline — sade palet (v0.10.922)', () => {
  it('düz sıfır seri: --err verilse de çizgi nötr --text3', () => {
    act(() => { root.render(<Sparkline values={[0, 0, 0, 0]} color="var(--err)" />); });
    const line = host.querySelector('svg line');
    expect(line).not.toBeNull();
    expect(line!.getAttribute('stroke')).toBe('var(--text3)');
    expect(host.innerHTML).not.toContain('var(--err)');
  });

  it('>0 kovalı seri çağıranın rengini korur (sapma sinyali)', () => {
    act(() => { root.render(<Sparkline values={[0, 0, 2.5, 0]} color="var(--err)" />); });
    const path = host.querySelector('path[fill="none"]');
    expect(path!.getAttribute('stroke')).toBe('var(--err)');
  });

  it('bars + threshold: err/warn kova renklendirmesi KORUNDU, normal kova nötr', () => {
    // threshold 10 → ≥10 err, ≥7 warn, gerisi soluk gri.
    act(() => { root.render(<Sparkline values={[1, 8, 12]} mode="bars" threshold={10} />); });
    const fills = Array.from(host.querySelectorAll('rect')).map(r => r.getAttribute('fill'));
    expect(fills).toEqual(['var(--text3)', 'var(--warn-solid)', 'var(--err)']);
  });

  it('area + threshold geçildi: çizgi kırmızıya döner (korundu)', () => {
    act(() => { root.render(<Sparkline values={[1, 2, 50]} color="var(--text3)" threshold={10} />); });
    const path = host.querySelector('path[fill="none"]');
    expect(path!.getAttribute('stroke')).toBe('var(--err)');
  });

  it('delta çipi rgb literal / yön rengi taşımaz; ok + yüzde yazılı', () => {
    act(() => { root.render(<Sparkline values={[10, 20, 40]} showDelta />); });
    expect(host.innerHTML).not.toMatch(/rgb\(/);
    expect(host.textContent).toContain('↑ 300%');
  });
});
