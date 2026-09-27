// @vitest-environment jsdom
//
// tracePodMetrics.test.tsx — v0.10.968 — useTracePodMetricsChunks'ın zaman
// davranışı (Trace › Metrics yeniden tasarımı, inceleme turu F3 + F9).
//
// Pinlenen vaatler:
//   - açık pencere: `to + 90 sn`de TAM BİR yeniden çekme (yoklama DEĞİL);
//     sekme o an gizliyse görünür olana dek BEKLER; bağlantı sökülünce
//     zamanlayıcı ve visibilitychange dinleyicisi temizlenir;
//   - kapalı pencerede zamanlayıcı hiç kurulmaz;
//   - eşlenmemiş / Thanos kapalı cevabı (mapped:false) istemcide de hep
//     bayat: yeniden bağlanınca bir kez daha sorulur (sunucu onu önbelleğe
//     almaz — Ayarlar › Remote Cluster dönüşünde hemen doğru); eşlenmiş
//     cevap staleTime içinde yeniden SORULMAZ.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor), sahte saat.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { TracePodMetricsResponse } from '@/lib/types';
import type { TracePodChunk } from '@/pages/trace/traceMetrics';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => ({ calls: 0, mapped: true }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      tracePodMetrics: async (): Promise<TracePodMetricsResponse> => {
        m.calls++;
        return m.mapped
          ? { thanos: true, clusterValue: 'dc-east-1', mapped: true, cluster: { id: 'c-1', name: 'dc-east-1' }, start: 0, step: 15, points: 1, instant: 'ok', pods: [] }
          : { thanos: true, clusterValue: 'dc-east-1', mapped: false, unmappedReason: 'no_remote_cluster', pods: [] };
      },
    },
  };
});

import { useTracePodMetricsChunks } from './tracePodMetrics';

const CHUNKS: TracePodChunk[] = [
  { key: 'dc-east-1#0', clusterValue: 'dc-east-1', index: 0, pods: [{ ns: 'shop', pod: 'checkout-api-7d9f8b6c5d-x2k4q' }], podsParam: 'shop/checkout-api-7d9f8b6c5d-x2k4q' },
  { key: 'dc-east-2#0', clusterValue: 'dc-east-2', index: 0, pods: [{ ns: 'shop', pod: 'checkout-api-7d9f8b6c5d-y3l5r' }], podsParam: 'shop/checkout-api-7d9f8b6c5d-y3l5r' },
];
const NO_FORCED: ReadonlySet<string> = new Set();
const T = Date.UTC(2026, 8, 27, 12, 0, 0);

function Probe({ toNs, openWindow }: { toNs: number; openWindow: boolean }) {
  useTracePodMetricsChunks(CHUNKS, {
    fromNs: toNs - 30 * 60e9, toNs, mdp: 120, openWindow, loadRest: false, forced: NO_FORCED, active: true,
  });
  return null;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let qc: QueryClient;
let hidden = false;

function mount(toNs: number, openWindow: boolean) {
  host = document.createElement('div');
  document.body.appendChild(host);
  act(() => {
    root = createRoot(host!);
    root.render(<QueryClientProvider client={qc}><Probe toNs={toNs} openWindow={openWindow} /></QueryClientProvider>);
  });
}
function unmount() {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
}
const advance = (ms: number) => act(async () => { await vi.advanceTimersByTimeAsync(ms); });

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(T);
  qc = new QueryClient();
  m.calls = 0; m.mapped = true; hidden = false;
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
});
afterEach(() => {
  unmount();
  qc.clear();
  vi.useRealTimers();
  Reflect.deleteProperty(document, 'hidden');
});

describe('useTracePodMetricsChunks — v0.10.968 açık pencere yeniden çekmesi', () => {
  const openTo = (T + 30_000) * 1e6; // `to` 30 sn sonra → zamanlayıcı T + 120 sn

  it('gizli sekmede bekler, görünür olunca dilim başına TAM bir çekme; sonra yoklama yok', async () => {
    mount(openTo, true);
    await advance(0);
    expect(m.calls).toBe(2);
    await advance(119_000);
    expect(m.calls).toBe(2);
    hidden = true;
    await advance(2_000);
    expect(m.calls).toBe(2); // zamanlayıcı gizli sekmede ateşledi → çekme ertelendi
    hidden = false;
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await advance(0);
    expect(m.calls).toBe(4);
    await advance(10 * 60_000);
    expect(m.calls).toBe(4);
  });

  it('görünür sekmede to + 90 sn\'de tam bir çekme', async () => {
    mount(openTo, true);
    await advance(0);
    await advance(121_000);
    expect(m.calls).toBe(4);
    await advance(10 * 60_000);
    expect(m.calls).toBe(4);
  });

  it('zamanlayıcıdan önce sökülünce çekme yok; gizli ateşlemeden sonra sökülünce dinleyici de gider', async () => {
    mount(openTo, true);
    await advance(0);
    await advance(60_000);
    unmount();
    await advance(10 * 60_000);
    expect(m.calls).toBe(2);

    m.calls = 0;
    qc.clear();
    mount((Date.now() + 30_000) * 1e6, true); // sahte saatin ŞİMDİ'sine göre açık pencere
    await advance(0);
    const base = m.calls;
    expect(base).toBe(2);
    hidden = true;
    await advance(121_000); // zamanlayıcı gizli sekmede ateşler → dinleyici kurulur
    unmount();
    hidden = false;
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    await advance(0);
    expect(m.calls).toBe(base);
  });

  it('kapalı pencere: zamanlayıcı yok', async () => {
    mount((T - 20 * 60_000) * 1e6, false);
    await advance(0);
    expect(m.calls).toBe(2);
    await advance(10 * 60_000);
    expect(m.calls).toBe(2);
  });
});

describe('useTracePodMetricsChunks — v0.10.968 eşlenmemiş cevap istemcide de bayat (F3)', () => {
  const closedTo = (T - 20 * 60_000) * 1e6;

  it('mapped:false → yeniden bağlanınca yine sorulur (Ayarlar dönüşü)', async () => {
    m.mapped = false;
    mount(closedTo, false);
    await advance(0);
    expect(m.calls).toBe(2);
    unmount();
    m.mapped = true;
    mount(closedTo, false);
    await advance(0);
    expect(m.calls).toBe(4);
  });

  it('mapped:true → staleTime içinde yeniden bağlanma istek atmaz', async () => {
    mount(closedTo, false);
    await advance(0);
    expect(m.calls).toBe(2);
    unmount();
    mount(closedTo, false);
    await advance(0);
    expect(m.calls).toBe(2);
  });
});
