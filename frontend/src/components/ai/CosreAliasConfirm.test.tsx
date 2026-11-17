// @vitest-environment jsdom
// CosreAliasConfirm.test.tsx — v0.10.2335 (conversation-audit §6.1 G).
// Sözleşme: kart HİÇBİR ŞEYİ kendiliğinden kaydetmez; yalnız "Kaydet" tıkı
// kartın deyim / hedef / yanlış aday kimliğiyle POST eder; "Hayır" sunucuya
// hiç gitmez, kartı kapatır.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

const m = vi.hoisted(() => ({ saves: [] as unknown[] }));
vi.mock('@/lib/api', () => ({
  api: {
    saveCosreAlias: vi.fn(async (b: unknown) => { m.saves.push(b); return { address: '', aliases: [], maxAliases: 50, bytes: 0, maxBytes: 4096 }; }),
  },
}));

import { CosreAliasConfirm } from './CosreAliasConfirm';
import { parseCosreAliasAction } from '@/lib/pageActions';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.saves = []; });

const action = parseCosreAliasAction({
  kind: 'cosre_alias', phrase: 'kart tarafı', entityId: 'svc:payments', name: 'payments', notId: 'svc:orders', notName: 'orders',
  text: '«kart tarafı» dediğinde payments servisini kastettiğini hatırlayayım mı?', label: 'Kaydet', dismissLabel: 'Hayır',
})!;

async function render() {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><CosreAliasConfirm action={action} exchangeId="x1" /></QueryClientProvider>); });
  return host;
}

const button = (el: HTMLElement, text: string) =>
  Array.from(el.querySelectorAll('button')).find(b => b.textContent === text) as HTMLButtonElement;

describe('CosreAliasConfirm', () => {
  it('metni gösterir; render kaydetmez; Kaydet kartın kimlikleriyle POST eder', async () => {
    const el = await render();
    expect(el.textContent).toContain('«kart tarafı» dediğinde payments servisini kastettiğini hatırlayayım mı?');
    expect(m.saves).toHaveLength(0);
    await act(async () => { button(el, 'Kaydet').click(); });
    expect(m.saves).toEqual([{ phrase: 'kart tarafı', entityId: 'svc:payments', notId: 'svc:orders', exchangeId: 'x1' }]);
    expect(button(el, 'Kaydedildi').disabled).toBe(true);
    expect(button(el, 'Hayır')).toBeUndefined();
  });
  it('Hayır sunucuya gitmez ve kartı kapatır', async () => {
    const el = await render();
    await act(async () => { button(el, 'Hayır').click(); });
    expect(m.saves).toHaveLength(0);
    expect(el.querySelector('[data-testid="cosre-alias-confirm"]')).toBeNull();
    expect(el.textContent).toContain('kaydetmedim');
  });
});
