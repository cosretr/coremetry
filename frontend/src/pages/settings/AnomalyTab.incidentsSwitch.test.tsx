// @vitest-environment jsdom
//
// AnomalyTab.incidentsSwitch — v0.10.1826 (operatör kararı 2026-11-05:
// "Incident sekmesi yerine Anomalies olsun; eski incident'lar devre dışı
// kalsın"). Settings › Anomaly: tek ana anahtar "Eski Incidents akışı
// (emekli)"; kapalıyken aile başına "incident'a bağla" kutuları devre dışı
// + gerekçe; kayıt PUT {enabled} gönderir.
import '@/test/radixJsdom';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { enabled: false, puts: [] as Record<string, unknown>[] };
});
vi.mock('./MLBaselineSections', () => ({ MLBaselineSections: () => null }));
vi.mock('@/lib/api', () => {
  const stub: Record<string, (...a: unknown[]) => Promise<unknown>> = {
    getAnomalyPromotion: async () => ({ enabled: true, minPeakRatio: 5, criticalPeakRatio: 20, minSustainedSec: 300, minCount: 10 }),
    getIncidentsSettings: async () => ({ enabled: m.enabled }),
    putIncidentsSettings: async (body: unknown) => { m.puts.push(body as Record<string, unknown>); return body; },
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
  host = null; root = null; m.puts = []; m.enabled = false;
});

async function mount() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<AnomalyPromotionTab />); });
  await settle();
  return host;
}
const box = (el: HTMLElement, label: string) => {
  const b = el.querySelector<HTMLButtonElement>(`button[role="checkbox"][aria-label="${label}"]`);
  if (!b) throw new Error(`kutu yok: ${label}`);
  return b;
};

describe('Eski Incidents akışı ana anahtarı', () => {
  it('kapalı (varsayılan): ana kutu işaretsiz, terfi bağlama kutusu devre dışı + gerekçe', async () => {
    const el = await mount();
    expect(el.textContent).toContain('Eski Incidents akışı (emekli)');
    expect(el.textContent).toContain('Veri silinmez.');
    expect(box(el, 'Eski Incidents akışı').getAttribute('aria-checked')).toBe('false');
    const attach = box(el, "Terfi Problem'i incident'a bağlansın");
    expect(attach.disabled).toBe(true);
    expect(el.textContent).toContain('Eski Incidents akışı kapalı');
  });

  it('açık: aile kutusu etkin, gerekçe yok', async () => {
    m.enabled = true;
    const el = await mount();
    expect(box(el, 'Eski Incidents akışı').getAttribute('aria-checked')).toBe('true');
    expect(box(el, "Terfi Problem'i incident'a bağlansın").disabled).toBe(false);
    expect(el.textContent).not.toContain('Eski Incidents akışı kapalı');
  });

  it('açıp kaydetmek PUT {enabled:true} gönderir ve kutuları açar', async () => {
    const el = await mount();
    await act(async () => { box(el, 'Eski Incidents akışı').click(); });
    const save = [...el.querySelectorAll('button')].find(b => b.textContent?.trim() === 'Kaydet');
    if (!save) throw new Error('Kaydet yok');
    await act(async () => { save.click(); });
    await settle();
    expect(m.puts).toEqual([{ enabled: true }]);
    expect(box(el, "Terfi Problem'i incident'a bağlansın").disabled).toBe(false);
  });
});
