// @vitest-environment jsdom
//
// AnomalyTab.behaviorHighVolume — v0.10.1521 davranış motorunun hacim farkında
// hata tabanı (Davranış değişimi alt bölümü, aynı Kaydet).
//
// NE ÇİVİLİYOR: alanı taşımayan blob → kutu AÇIK, 1 hata/sn + %0,2; kayıtlı
// false → kapalı, alanlar devre dışı; değişiklik kayıtla behavior içinde gider
// ve gövdenin geri kalanı korunur.
import '@/test/radixJsdom'; // Faz 2 dilim 1 R17 — Radix Checkbox
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { AnomalySensitivityConfig } from '@/lib/types';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    sens: {} as Record<string, unknown>,
    puts: [] as Record<string, unknown>[],
  };
});
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 15, criticalPeakRatio: 20, minSustainedSec: 1800, minCount: 1000 }),
    getAnomalyTracked: async () => ({ error_rate: true, p99_ms: true, request_rate: false }),
    getAnomalySensitivity: async () => m.sens,
    putAnomalySensitivity: async (body: unknown) => {
      m.puts.push(body as Record<string, unknown>);
      return body;
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

const baseSens = (extra: Partial<AnomalySensitivityConfig> = {}): Record<string, unknown> => ({
  metrics: {
    error_rate: { floorPct: 0.1, absFloor: 1, minAbsDelta: 0, minMAD: 0, minBaselineRate: 0 },
    p99_ms: { floorPct: 0.1, absFloor: 10, minAbsDelta: 0, minMAD: 1, minBaselineRate: 0 },
    request_rate: { floorPct: 0.15, absFloor: 0, minAbsDelta: 0, minMAD: 0, minBaselineRate: 0 },
  },
  dwellBuckets: 3, criticalZ: 6, attachToIncident: true, serviceSilent: false,
  batchServicePatterns: ['-batch'],
  ...extra,
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const settle = () => act(async () => { await new Promise(r => setTimeout(r, 30)); });

async function mount(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<AnomalyPromotionTab />); });
  await settle();
  return host;
}

const hvToggle = (el: HTMLElement) => {
  const b = el.querySelector<HTMLButtonElement>('button[role="checkbox"][aria-label="Yüksek hacimde düşük hata tabanı"]');
  if (!b) throw new Error('kutu yok');
  return b;
};
const numInput = (el: HTMLElement, label: string) => {
  const i = el.querySelector<HTMLInputElement>(`input[type="number"][aria-label="Yüksek hacim: ${label}"]`);
  if (!i) throw new Error(`alan yok: ${label}`);
  return i;
};
function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Kaydet yok');
  return btn as HTMLButtonElement;
}
const behavior = (extra: Record<string, unknown> = {}) => ({
  enabled: true, seasonalZ: 4, regimeRatio: 1.5, dwellSeasonal: 3, dwellRegime: 6, maxCandidatesPerTick: 50,
  minSamplesPerBucket: 12, minBucketRepeats: 3, minP99Ms: 200, minErrorRatePct: 1, spikyBandFactor: 1.5, ...extra,
});

beforeEach(() => { m.puts = []; m.sens = baseSens({ behavior: behavior() }); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('Davranış: yüksek hacimde düşük hata tabanı', () => {
  it('alanı taşımayan blob → AÇIK, varsayılanlar, Türkçe açıklama', async () => {
    const el = await mount();
    expect(hvToggle(el).getAttribute('aria-checked')).toBe('true');
    expect(numInput(el, 'hata hızı eşiği').value).toBe('1');
    expect(numInput(el, 'hata oranı tabanı').value).toBe('0.2');
    expect(el.textContent).toContain('düşük hacimli');
  });

  it('kayıtlı false → kapalı, alanlar devre dışı', async () => {
    m.sens = baseSens({ behavior: behavior({ highVolumeErrorFloor: false, highVolumeErrorsPerSec: 3, highVolumeMinErrorRatePct: 0.4 }) });
    const el = await mount();
    expect(hvToggle(el).getAttribute('aria-checked')).toBe('false');
    expect(numInput(el, 'hata hızı eşiği').disabled).toBe(true);
    expect(numInput(el, 'hata hızı eşiği').value).toBe('3');
  });

  it('değişiklik behavior içinde gider, geri kalanı korunur', async () => {
    const el = await mount();
    const input = numInput(el, 'hata oranı tabanı');
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, '0.3');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    // Faz 2 dilim 1 R17 — Radix Checkbox kontrollü prop'tan okur: iki tık ayrı act'te.
    await act(async () => { hvToggle(el).click(); });
    await act(async () => { hvToggle(el).click(); });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    const b = m.puts[0].behavior as Record<string, unknown>;
    expect(b.highVolumeMinErrorRatePct).toBe(0.3);
    expect(b.highVolumeErrorFloor).toBe(true);
    expect(b.minErrorRatePct).toBe(1);
    expect(m.puts[0].criticalZ).toBe(6);
  });
});
