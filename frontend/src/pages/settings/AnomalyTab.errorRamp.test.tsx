// @vitest-environment jsdom
//
// AnomalyTab.errorRamp — v0.10.1518 sürekli teknik hata (5xx) yükselişi
// kuralının Settings bölümü (anomaly_sensitivity.errorRamp, aynı Kaydet).
//
// NE ÇİVİLİYOR:
//   • bölümü taşımayan blob → kutu AÇIK, varsayılanlar (6 kova = 30 dk, 2×,
//     %0,2, 1 hata/sn), yakaladığı / yok saydığı / karar kaynağı metni;
//   • kayıtlı değerler + enabled:false gidiş-dönüşü;
//   • alan değişikliği kayıtla gider, bölüm tam ve enabled AÇIK boolean,
//     gövdenin geri kalanı korunur;
//   • aralık dışı değer satır içi uyarı + Kaydet gövde GÖNDERMEZ (sunucu
//     aralık dışını sessizce varsayılana döndürürdü).
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

const toggle = (el: HTMLElement) => {
  const b = el.querySelector<HTMLButtonElement>('button[role="checkbox"][aria-label="Sürekli teknik hata yükselişi"]');
  if (!b) throw new Error('Teknik hata yükselişi kutusu yok');
  return b;
};
const numInput = (el: HTMLElement, label: string) => {
  const i = el.querySelector<HTMLInputElement>(`input[type="number"][aria-label="Teknik hata yükselişi: ${label}"]`);
  if (!i) throw new Error(`alan yok: ${label}`);
  return i;
};
function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Dedektör hassasiyeti Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}
async function type(input: HTMLInputElement, v: string) {
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, v);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}

beforeEach(() => { m.puts = []; m.sens = baseSens(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const W = 'Süreklilik (× 5 dk)';
const F = 'Artış katı (× taban medyanı)';
const R = 'Oran tabanı (%)';
const E = 'Hata hızı tabanı (hata/sn)';

describe('Sürekli teknik hata yükselişi bölümü', () => {
  it('bölümü taşımayan blob → AÇIK, varsayılanlar, dakika ve Türkçe kapsam metni', async () => {
    const el = await mount();
    expect(toggle(el).getAttribute('aria-checked')).toBe('true');
    expect(numInput(el, W).value).toBe('6');
    expect(numInput(el, F).value).toBe('2');
    expect(numInput(el, R).value).toBe('0.2');
    expect(numInput(el, E).value).toBe('1');
    expect(el.textContent).toContain('= 30 dakika');
    expect(el.textContent).toContain('teknik hata');
    expect(el.textContent).toContain('%1 mutlak tabanı');
    expect(el.textContent).toContain('4xx');
    expect(el.textContent).toContain('sıçramalar');
    expect(el.textContent).toContain('olağan günlük yükseliş');
    expect(el.textContent).toContain('düşük hacim');
    expect(el.textContent).toContain('VictoriaMetrics');
  });

  it('kayıtlı değerler + enabled:false gidiş-dönüş: aynı değerler, enabled false gider', async () => {
    m.sens = baseSens({ errorRamp: { enabled: false, windowBuckets: 12, ratioFactor: 3, minRatioPct: 0.5, minErrorsPerSec: 4 } });
    const el = await mount();
    expect(toggle(el).getAttribute('aria-checked')).toBe('false');
    expect(numInput(el, W).disabled).toBe(true);
    expect(numInput(el, W).value).toBe('12');
    expect(el.textContent).toContain('= 60 dakika');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].errorRamp).toEqual({ enabled: false, windowBuckets: 12, ratioFactor: 3, minRatioPct: 0.5, minErrorsPerSec: 4 });
  });

  it('alan değişikliği kayıtla gider: bölüm tam, enabled AÇIK boolean, gövdenin geri kalanı korunur', async () => {
    const el = await mount();
    await type(numInput(el, E), '2.5');
    await type(numInput(el, W), '8');
    expect(el.textContent).toContain('= 40 dakika');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].errorRamp).toEqual({ enabled: true, windowBuckets: 8, ratioFactor: 2, minRatioPct: 0.2, minErrorsPerSec: 2.5 });
    expect(m.puts[0].criticalZ).toBe(6);
    // Kayıt sonrası sunucunun döndürdüğü değer ekranda.
    expect(numInput(el, E).value).toBe('2.5');
  });

  it.each([
    [W, '1', '2–24'], [W, '25', '2–24'], [W, '6.5', 'Tam sayı'],
    [F, '1.1', '1,2–50'], [F, '51', '1,2–50'],
    [R, '0.001', '0,01–50'], [R, '60', '0,01–50'],
    [E, '0.01', '0,05–100.000'], [E, '', 'Sayı girin'],
  ])('%s = %j aralık dışı → uyarı, Kaydet gövde göndermez', async (label, v, msg) => {
    const el = await mount();
    await type(numInput(el, label), v);
    expect(numInput(el, label).getAttribute('aria-invalid')).toBe('true');
    expect(el.textContent).toContain(msg);
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(0);
    expect(el.textContent).toContain('Kaydedilmedi');
  });

  it('kapatma enabled:false gönderir', async () => {
    const el = await mount();
    await act(async () => { toggle(el).click(); });
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect((m.puts[0].errorRamp as Record<string, unknown>).enabled).toBe(false);
  });
});
