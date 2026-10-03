// @vitest-environment jsdom
//
// AnomalyTab.opLatency — v0.10.1056, v0.10.1085.
//
// v0.10.1056 (operatör, prod): "Trace op latency false pozitif geliyor, gerek
// yok gelmelerine bence." → kapalı varsayılan. v0.10.1085 (operatör, sürdürme
// önerisine: "Önerini yapalım"): varsayılan AÇIK, yalnız iki ardışık kova
// sürdürme kuralıyla (anomaly_sensitivity.opLatency backend *bool, yok = AÇIK;
// opLatencyDwellBuckets 1–6, vars. 2).
//
// NE ÇİVİLİYOR ("Operasyon gecikmesi anomalileri" kutusu, Dedektör hassasiyeti):
//   • alanı taşımayan blob → kutu AÇIK, ipucu sürdürme kuralını söyler;
//   • kayıtlı false → kutu kapalı, kova alanı devre dışı (operatörün kapatması kalır);
//   • dokunmadan kayıt AÇIK `opLatency: true` gönderir (alan atlanmaz);
//   • kutuyu kapatıp kayıt `opLatency: false` gönderir; gövdenin geri kalanı korunur;
//   • kova sayısı alanı kayıtla gider (yoksa 2 gösterilir).
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
      // Sunucu Normalize'ının taklidi: opLatency somutlaşır (yok → true),
      // kova sayısı aralık dışıysa 2.
      const b = body as Record<string, unknown>;
      const d = Number(b.opLatencyDwellBuckets);
      return { ...b, opLatency: b.opLatency !== false, opLatencyDwellBuckets: d >= 1 && d <= 6 ? d : 2 };
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

// Bilerek opLatency / opLatencyDwellBuckets TAŞIMAYAN blob (v0.10.1056 öncesi kayıt).
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
  const b = el.querySelector<HTMLInputElement>('input[type="checkbox"][aria-label="Operasyon gecikmesi anomalileri"]');
  if (!b) throw new Error('Operasyon gecikmesi kutusu yok');
  return b;
};

function sensitivitySave(el: HTMLElement): HTMLButtonElement {
  const h2 = [...el.querySelectorAll('h2')].find(h => h.textContent === 'Dedektör hassasiyeti');
  const btn = [...(h2?.parentElement?.querySelectorAll('button') ?? [])].find(b => b.textContent?.includes('Kaydet'));
  if (!btn) throw new Error('Dedektör hassasiyeti Kaydet düğmesi yok');
  return btn as HTMLButtonElement;
}

beforeEach(() => { m.puts = []; m.sens = baseSens(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

const dwellInput = (el: HTMLElement) => {
  const i = el.querySelector<HTMLInputElement>('input[type="number"][aria-label="Operasyon gecikmesi ardışık kova"]');
  if (!i) throw new Error('Operasyon gecikmesi kova alanı yok');
  return i;
};

describe('Operasyon gecikmesi anomalileri kutusu', () => {
  it('alanı taşımayan blob → AÇIK, kova 2, ipucu sürdürmeyi söyler', async () => {
    expect('opLatency' in m.sens).toBe(false);
    const el = await mount();
    expect(toggle(el).checked).toBe(true);
    expect(dwellInput(el).value).toBe('2');
    expect(dwellInput(el).disabled).toBe(false);
    expect(el.textContent).toContain('iki ardışık kovada sürmeli; tek sıçrama');
    expect(el.textContent).toContain('alarm açmaz');
    expect(el.textContent).toContain('Servis düzeyindeki gecikme anomalileri etkilenmez.');
  });

  it('kayıtlı false → kapalı, kova alanı devre dışı', async () => {
    m.sens = baseSens({ opLatency: false, opLatencyDwellBuckets: 3 });
    const el = await mount();
    expect(toggle(el).checked).toBe(false);
    expect(dwellInput(el).disabled).toBe(true);
    expect(dwellInput(el).value).toBe('3');
  });

  it('dokunmadan kayıt AÇIK true gönderir', async () => {
    const el = await mount();
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect('opLatency' in m.puts[0]).toBe(true);
    expect(m.puts[0].opLatency).toBe(true);
  });

  it('kutuyu kapatıp kayıt false gönderir, gövdenin geri kalanı korunur', async () => {
    const el = await mount();
    await act(async () => { toggle(el).click(); });
    expect(toggle(el).checked).toBe(false);
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].opLatency).toBe(false);
    expect(m.puts[0].criticalZ).toBe(6);
    expect(m.puts[0].serviceSilent).toBe(false);
    expect(toggle(el).checked).toBe(false);
  });

  it('kova sayısı kayıtla gider', async () => {
    const el = await mount();
    const input = dwellInput(el);
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
      setter.call(input, '1');
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
    expect(dwellInput(el).value).toBe('1');
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].opLatencyDwellBuckets).toBe(1);
    expect(dwellInput(el).value).toBe('1');
  });
});
