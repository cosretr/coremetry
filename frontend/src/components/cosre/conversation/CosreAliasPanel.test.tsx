// @vitest-environment jsdom
// CosreAliasPanel.test.tsx — v0.10.2335 (conversation-audit §6.1 G): kullanıcı
// kendi eş adlarını görür ve siler; bayat eşleme işaretli.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ deletes: [] as string[] }));
vi.mock('@/lib/api', () => ({
  api: {
    getCosreUserPrefs: vi.fn(async () => ({
      address: 'sen', maxAliases: 50, bytes: 120, maxBytes: 4096,
      aliases: [
        { phrase: 'kart tarafı', entityId: 'svc:payments', name: 'payments', notId: 'svc:orders', notName: 'orders', at: 2, stale: false },
        { phrase: 'eski taraf', entityId: 'svc:inventory', name: 'inventory', at: 1, stale: true },
      ],
    })),
    deleteCosreAlias: vi.fn(async (p: string) => { m.deletes.push(p); return {}; }),
  },
}));

vi.mock('@/components/ui/ConfirmDialog', () => ({ useConfirm: () => async () => true }));

import { CosreAliasPanel } from './CosreAliasPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.deletes = []; });

describe('CosreAliasPanel', () => {
  it('listeler, bayatı işaretler, siler', async () => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreAliasPanel /></QueryClientProvider>); });
    for (let i = 0; i < 20 && !host.querySelector('li'); i++) {
      await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    }
    const items = host.querySelectorAll('li');
    expect(items).toHaveLength(2);
    expect(items[0].textContent).toContain('«kart tarafı» → payments (değil orders)');
    expect(items[0].hasAttribute('data-stale')).toBe(false);
    expect(items[1].getAttribute('data-stale')).toBe('true');
    expect(items[1].textContent).toContain('bayat');
    const del = items[0].querySelector('button[aria-label="Sil: kart tarafı"]') as HTMLButtonElement;
    await act(async () => { del.click(); });
    expect(m.deletes).toEqual(['kart tarafı']);
  });
});
