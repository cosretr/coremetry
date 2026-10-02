// @vitest-environment jsdom
//
// AnomalyTab.opLatency — v0.10.1056.
//
// Operatör (prod): "Trace op latency false pozitif geliyor, gerek yok
// gelmelerine bence." trace_op_latency dedektörü varsayılan KAPALI
// (anomaly_sensitivity.opLatency, backend *bool, yok = kapalı).
//
// NE ÇİVİLİYOR ("Operasyon gecikmesi anomalileri" kutusu, Dedektör hassasiyeti):
//   • alanı taşımayan blob (eski kayıt) → kutu KAPALI;
//   • kayıtlı true → kutu açık;
//   • dokunmadan kayıt AÇIK `opLatency: false` gönderir (alan atlanmaz);
//   • kutuyu açıp kayıt `opLatency: true` gönderir; gövdenin geri kalanı korunur.
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
      // Sunucu Normalize'ının taklidi: opLatency somutlaşır (yok → false).
      const b = body as Record<string, unknown>;
      return { ...b, opLatency: b.opLatency === true };
    },
  };
  return {
    api: new Proxy({}, { get: (_t, k: string) => stub[k] ?? (() => new Promise(() => {})) }),
    isCanceled: () => false,
  };
});

import { AnomalyPromotionTab } from './AnomalyTab';

// Bilerek opLatency TAŞIMAYAN blob (v0.10.1056 öncesi kayıt).
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

describe('Operasyon gecikmesi anomalileri kutusu', () => {
  it('alanı taşımayan blob → KAPALI, ipucu görünür', async () => {
    expect('opLatency' in m.sens).toBe(false);
    const el = await mount();
    expect(toggle(el).checked).toBe(false);
    expect(el.textContent).toContain('Kapalı (varsayılan): operasyon bazında p99 sıçramaları anomali açmaz');
    expect(el.textContent).toContain('Servis düzeyindeki gecikme anomalileri etkilenmez.');
  });

  it('kayıtlı true → açık', async () => {
    m.sens = baseSens({ opLatency: true });
    const el = await mount();
    expect(toggle(el).checked).toBe(true);
  });

  it('dokunmadan kayıt AÇIK false gönderir', async () => {
    const el = await mount();
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts).toHaveLength(1);
    expect('opLatency' in m.puts[0]).toBe(true);
    expect(m.puts[0].opLatency).toBe(false);
  });

  it('kutuyu açıp kayıt true gönderir, gövdenin geri kalanı korunur', async () => {
    const el = await mount();
    await act(async () => { toggle(el).click(); });
    expect(toggle(el).checked).toBe(true);
    await act(async () => { sensitivitySave(el).click(); });
    await settle();
    expect(m.puts[0].opLatency).toBe(true);
    expect(m.puts[0].criticalZ).toBe(6);
    expect(m.puts[0].serviceSilent).toBe(false);
    expect(toggle(el).checked).toBe(true);
  });
});
