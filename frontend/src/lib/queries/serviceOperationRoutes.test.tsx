// @vitest-environment jsdom
//
// serviceOperationRoutes.test.tsx — v0.10.1023 inceleme R1. Operatör
// bildirimi: "Operation kısmında POST GET neden detail gözükmüyor, sonra
// trace'e girince çıkıyor." main.tsx küresel `placeholderData:
// keepPreviousData` koyuyor; rota sorgusu ondan çıkmazsa anahtar değişince
// (aralık / zoom / servis) ya da sorgu kapalıyken `data` ÖNCEKİ anahtarın
// satırları olur ve tablo eski pencerenin rota satırlarını yeni bundle'ın ham
// satırlarına uygular. Burada main.tsx'le AYNI varsayılanlı bir QueryClient
// ile kanıtlanır: anahtar değişince hook verisi undefined, yer tutucu değil.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider, keepPreviousData } from '@tanstack/react-query';
import type { OperationRoutesFor, OperationRoutesResponse } from '@/lib/types';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => ({ calls: [] as string[] }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      serviceOperationRoutes: async (svc: string, r: { from: number; to: number }): Promise<OperationRoutesResponse> => {
        m.calls.push(`${svc}:${r.from}-${r.to}`);
        return {
          covered: true,
          rows: [{ name: 'GET', route: `/w${r.from}`, spanCount: 1, errorCount: 0, errorRate: 0, avgDurationMs: 1, p50DurationMs: 1, p95DurationMs: 1, p99DurationMs: 1, apdex: 0 }],
        };
      },
    },
  };
});

import { useServiceOperationRoutes } from './services';

type Seen = { asked: OperationRoutesFor | null; data: unknown; isPlaceholderData: boolean };
let last: Seen = { asked: null, data: undefined, isPlaceholderData: false };
// Her render kaydedilir: yükleme sırasındaki ara render'lar da denetlensin.
const history: Seen[] = [];
function Probe({ forWin, enabled }: { forWin: OperationRoutesFor | null; enabled: boolean }) {
  const q = useServiceOperationRoutes(forWin, enabled);
  last = { asked: forWin, data: q.data, isPlaceholderData: q.isPlaceholderData };
  history.push(last);
  return null;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  m.calls.length = 0;
  history.length = 0;
});

// main.tsx'teki varsayılanlarla aynı yer tutucu kuralı.
function client() {
  return new QueryClient({ defaultOptions: { queries: { placeholderData: keepPreviousData, retry: false } } });
}
async function render(qc: QueryClient, forWin: OperationRoutesFor | null, enabled: boolean) {
  await act(async () => {
    if (!root) {
      host = document.createElement('div');
      document.body.appendChild(host);
      root = createRoot(host);
    }
    root.render(<QueryClientProvider client={qc}><Probe forWin={forWin} enabled={enabled} /></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

const A: OperationRoutesFor = { svc: 'payments-api', from: 1000, to: 2000, env: '' };
const B: OperationRoutesFor = { svc: 'payments-api', from: 3000, to: 4000, env: '' };

describe('useServiceOperationRoutes — yer tutucu veri yok (inceleme R1)', () => {
  it('veri istendiği üçlüyle damgalanır', async () => {
    const qc = client();
    await render(qc, A, true);
    expect(m.calls).toEqual(['payments-api:1000-2000']);
    expect((last.data as { for: OperationRoutesFor }).for).toEqual(A);
  });

  it('anahtar değişip sorgu KAPALIYKEN önceki pencerenin verisi gelmez', async () => {
    const qc = client();
    await render(qc, A, true);
    expect(last.data).toBeDefined();
    await render(qc, B, false); // ör. başka sekmeye geçildi, aralık değişti
    expect(last.data).toBeUndefined();
    expect(last.isPlaceholderData).toBe(false);
  });

  it('anahtar değişip sorgu açıkken: yükleme boyunca önceki pencerenin verisi gelmez, sonra yenisi', async () => {
    const qc = client();
    await render(qc, A, true);
    const mark = history.length;
    await render(qc, B, true);
    const afterSwitch = history.slice(mark);
    // En az bir ara render (yükleniyor) ve hiçbirinde A'nın verisi / yer tutucu yok.
    expect(afterSwitch.some(h => h.data === undefined)).toBe(true);
    for (const h of afterSwitch) {
      expect(h.isPlaceholderData).toBe(false);
      if (h.data !== undefined) expect((h.data as { for: OperationRoutesFor }).for).toEqual(B);
    }
    expect((last.data as { for: OperationRoutesFor }).for).toEqual(B);
  });

  it('env doluyken ya da pencere yokken istek atılmaz', async () => {
    const qc = client();
    await render(qc, { ...A, env: 'prod' }, true);
    await render(qc, null, true);
    expect(m.calls).toEqual([]);
  });
});
