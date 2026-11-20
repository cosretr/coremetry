// @vitest-environment jsdom
//
// panelQueries.test.tsx — 2026-11-21: SpanDetail / Trace paylaşım popover'ı /
// Logs yan panelleri effect-fetch'ten lib/queries hook'larına taşındı.
//
// Pinlenen vaatler:
//   - her hook AbortSignal'i api'ye iletir (panel kapanınca istek kesilir);
//   - kapalı girdi (servis yok / pivot null / popover kapalı) = istek YOK;
//   - hata = tek atış (retry yok) ve çağıranın eski boş durumu;
//   - baseline p50 eşleşmesi: ham ad önce, sonra gösterim adı; p50 ≤ 0 → null;
//   - unmount uçuştaki isteğin signal'ini abort eder.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => ({
  signals: {} as Record<string, AbortSignal | undefined>,
  calls: {} as Record<string, number>,
  fail: new Set<string>(),
  hang: new Set<string>(),
}));

function rec(name: string, signal: AbortSignal | undefined, value: unknown): Promise<unknown> {
  m.signals[name] = signal;
  m.calls[name] = (m.calls[name] ?? 0) + 1;
  if (m.hang.has(name)) return new Promise(() => {});
  return m.fail.has(name) ? Promise.reject(new Error('boom')) : Promise.resolve(value);
}

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      profilesForSpan: (_s: string, _a: number, _b: number, signal?: AbortSignal) => rec('profiles', signal, null),
      spanHotspots: (_s: string, _a: number, _b: number, _t: number, signal?: AbortSignal) => rec('hotspots', signal, { frames: [] }),
      serviceOperations: (_s: string, _r: unknown, _n: boolean, _c: boolean, _e: string, signal?: AbortSignal) =>
        rec('ops', signal, [
          { name: 'GET /users/{id}', p50DurationMs: 12 },
          { name: 'zero', p50DurationMs: 0 },
        ]),
      logs: (_p: unknown, signal?: AbortSignal) => rec('logs', signal, { logs: [{ id: 1 }], total: 1 }),
      logsContext: (_p: unknown, signal?: AbortSignal) => rec('context', signal, { before: [], after: [] }),
      logsFields: (signal?: AbortSignal) => rec('fields', signal, { fields: ['a'], backend: 'elasticsearch' }),
      getKibanaSettings: (signal?: AbortSignal) => rec('kibana', signal, null),
      listTraceShares: (_id: string, signal?: AbortSignal) => rec('shares', signal, null),
    },
  };
});

import { useSpanProfiles, useSpanHotspots, useSpanBaselineP50 } from './spans';
import { useTracePanelLogs, useLogsContext, useLogsFields, useKibanaSettings } from './logs';
import { useTraceShares } from './trace';

let root: Root | null = null;
let host: HTMLDivElement | null = null;
let qc: QueryClient;

beforeEach(() => {
  m.signals = {}; m.calls = {}; m.fail.clear(); m.hang.clear();
  qc = new QueryClient({ defaultOptions: { queries: { retry: 3, retryDelay: 1 } } });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  qc.clear();
});

async function mount<T>(hook: () => T): Promise<{ get: () => T }> {
  let last!: T;
  function Probe() { last = hook(); return null; }
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><Probe /></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 5)); });
  return { get: () => last };
}

describe('panel hook\'ları — signal + boş durumlar', () => {
  it('SpanDetail hook\'ları signal iletir, null profil listesi [] olur', async () => {
    const h = await mount(() => ({
      p: useSpanProfiles('svc', 1, 2),
      h: useSpanHotspots('svc', 1, 2, 10),
      b: useSpanBaselineP50('svc', 2, ['raw', 'GET /users/{id}']),
    }));
    for (const k of ['profiles', 'hotspots', 'ops']) expect(m.signals[k], k).toBeInstanceOf(AbortSignal);
    expect(h.get().p.data).toEqual([]);
    expect(h.get().b.data).toBe(12); // ham ad yok → gösterim adı eşleşti
  });

  it('baseline: p50 ≤ 0 ya da eşleşme yok → null', async () => {
    const h = await mount(() => ({ z: useSpanBaselineP50('svc', 2, ['zero']), n: useSpanBaselineP50('svc', 2, ['nope']) }));
    expect(h.get().z.data).toBeNull();
    expect(h.get().n.data).toBeNull();
    expect(m.calls.ops).toBe(1); // aynı (servis, bitiş) → tek istek
  });

  it('servis/başlangıç yoksa istek yok', async () => {
    await mount(() => ({ p: useSpanProfiles('', 1, 2), b: useSpanBaselineP50('svc', 0, ['x']) }));
    expect(m.calls.profiles).toBeUndefined();
    expect(m.calls.ops).toBeUndefined();
  });

  it('hata tek atış (retry yok)', async () => {
    m.fail.add('logs');
    const h = await mount(() => useTracePanelLogs('t1', 1, 2));
    expect(h.get().isError).toBe(true);
    expect(m.calls.logs).toBe(1);
  });

  it('logs context: null = kapalı; açıkken signal iletilir', async () => {
    await mount(() => useLogsContext(null));
    expect(m.calls.context).toBeUndefined();
    await mount(() => useLogsContext({ ts: 5, n: 50 }));
    expect(m.signals.context).toBeInstanceOf(AbortSignal);
  });

  it('fields + kibana + shares signal iletir; popover kapalıyken shares istenmez', async () => {
    await mount(() => ({ f: useLogsFields(), k: useKibanaSettings(), s: useTraceShares('t1', false) }));
    expect(m.signals.fields).toBeInstanceOf(AbortSignal);
    expect(m.signals.kibana).toBeInstanceOf(AbortSignal);
    expect(m.calls.shares).toBeUndefined();
    const h = await mount(() => useTraceShares('t1', true));
    expect(m.signals.shares).toBeInstanceOf(AbortSignal);
    expect(h.get().data).toEqual([]);
  });

  it('unmount uçuştaki isteği keser', async () => {
    m.hang.add('context');
    await mount(() => useLogsContext({ ts: 9, n: 5 }));
    const sig = m.signals.context!;
    expect(sig.aborted).toBe(false);
    act(() => root!.render(<QueryClientProvider client={qc}><></></QueryClientProvider>));
    await act(async () => { await new Promise(r => setTimeout(r, 5)); });
    expect(sig.aborted).toBe(true);
  });
});
