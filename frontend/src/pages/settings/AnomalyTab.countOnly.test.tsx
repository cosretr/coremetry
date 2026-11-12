// @vitest-environment jsdom
//
// AnomalyTab.countOnly — v0.10.1587 (Davis paritesi FAZ 0.5 / B): sayım-yalnız
// (oransız) olayların terfi sayım tabanı. Alanı taşımayan blob → 20 gösterilir;
// değişiklik terfi Kaydet'iyle gider, gövdenin geri kalanı korunur.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R17 — Radix Checkbox
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { puts: [] as Record<string, unknown>[] };
});
// v0.10.1815 — öğrenme döngüsü bölümleri React Query + onay kapısı ister; bu test başka bölümü sınar.
vi.mock('./MLBaselineSections', () => ({ MLBaselineSections: () => null }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 5, criticalPeakRatio: 20, minSustainedSec: 300, minCount: 10 }),
    putAnomalyPromotion: async (body: unknown) => { m.puts.push(body as Record<string, unknown>); return body; },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null; m.puts = [];
});

describe('Yeni hata / yeni desen sayım tabanı', () => {
  it('alan yok → 20; değişiklik kayıtla gider', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<AnomalyPromotionTab />); });
    await settle();
    const input = host.querySelector<HTMLInputElement>('input[aria-label="Yeni hata en az sayım"]');
    if (!input) throw new Error('alan yok');
    expect(input.value).toBe('20');
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, '50');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    const save = [...host.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Save');
    if (!save) throw new Error('Save yok');
    await act(async () => { save.click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].countOnlyMinCount).toBe(50);
    expect(m.puts[0].minPeakRatio).toBe(5);
  });
});

// Incidents ölü kod dilimi 4 (v0.10.2073): incident bağlama kutuları silindi.
describe('Terfi sekmesinde incident bağlama kutusu yok', () => {
  it('kutu ve emekli gerekçesi çizilmez', async () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<AnomalyPromotionTab />); });
    await settle();
    expect(host.querySelector('button[role="checkbox"][aria-label="Terfi Problem\'i incident\'a bağlansın"]')).toBeNull();
    expect(host.textContent).not.toContain('Eski Incidents akışı emekli');
  });
});
