// @vitest-environment jsdom
//
// inboxCount.test.tsx — v0.10.1086: kenar çubuğu Problems rozeti sunucunun
// `count` alanını AYNEN gösterir. Sunucu o sayıyı Problems listesinin
// varsayılan görünümünden (yalnız P1, incident katlaması dahil) okur; eskiden
// istemci problems + anomalies + incidents toplardı ve rozet ekrandaki
// satırdan büyük okunuyordu. Burada istemcinin toplama / düzeltme YAPMADIĞI
// çivilenir. Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => ({ body: null as unknown, envs: [] as (string | undefined)[] }));
vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      inboxCount: async (env?: string) => {
        m.envs.push(env);
        return m.body;
      },
    },
  };
});

import { useInboxCount } from './inbox';

let seen: { triage: number; exceptions: number } | undefined;
function Probe({ env }: { env?: string }) {
  seen = useInboxCount(env).data;
  return null;
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null;
  seen = undefined;
  m.envs.length = 0;
});

async function mount(env?: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><Probe env={env} /></QueryClientProvider>);
  });
  for (let i = 0; i < 20 && seen === undefined; i++) {
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  }
}

describe('useInboxCount — rozet sunucunun count alanını aynen gösterir', () => {
  it('count = varsayılan listenin satırı; eski kırılım alanları toplanmaz', async () => {
    // Eski sürümün önbellekte kalmış gövdesi kırılım alanlarını taşısa bile
    // manşet yalnız `count`u okur (2 — katlanmış incident + P1 exception).
    m.body = { count: 2, scanCapped: false, exceptions: 40, httpErrors: 3, problems: 3, anomalies: 1, incidents: 1 };
    await mount();
    expect(seen).toEqual({ triage: 2, exceptions: 43 });
  });

  it('büyük sayı ve tarama tavanı: biçimlenmeden, değiştirilmeden geçer', async () => {
    m.body = { count: 1234, scanCapped: true, exceptions: 0, httpErrors: 0 };
    await mount('uat');
    expect(seen?.triage).toBe(1234);
    expect(m.envs).toEqual(['uat']);
  });

  it('boş gövde sıfır rozet', async () => {
    m.body = null;
    await mount();
    expect(seen).toEqual({ triage: 0, exceptions: 0 });
  });
});
