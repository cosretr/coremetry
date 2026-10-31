// @vitest-environment jsdom
//
// WikiPagesTable — v0.10.1705 (tablo tuzağı: fixed + nowrap + dar genişlik
// sessizce kırpar). Ayrıntı satırının sınıfsız <td>'si genel `tbody td`
// kuralını (nowrap + max-width 320px + overflow hidden) alıyordu; tüm satıra
// yayılan önizleme 320px'te kesiliyordu. Sözleşme: ayrıntı hücresi
// `cell-wrap` (sarar, kırpmaz); başlık hücresi "…" + title ile tam metni taşır.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { WikiPagesPage } from '@/lib/types';

const h = vi.hoisted(() => ({
  pages: vi.fn<(q: string, off: number, lim: number) => Promise<WikiPagesPage>>(),
}));
vi.mock('@/lib/api', () => ({
  api: { getWikiPages: (q: string, off: number, lim: number) => h.pages(q, off, lim) },
}));

import { WikiPagesTable } from './WikiPagesTable';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  host = null;
  root = null;
});

const LONG_TITLE = 'Runbook: orders-api ödeme akışı yeniden deneme ve kuyruk boşaltma prosedürü (payments-core)';
const PAGE: WikiPagesPage = {
  rows: [{ project: 'Platform', wiki: 'Platform.wiki', wikiId: 'w1', path: '/Runbooks/orders-api', title: LONG_TITLE,
    url: 'https://wiki.acme.example/Runbooks/orders-api', chunks: 3, updatedAt: '2026-10-30T10:00:00Z',
    preview: 'x'.repeat(900) }],
  total: 1, offset: 0, limit: 100, preview: true,
};

describe('WikiPagesTable', () => {
  it('ayrıntı hücresi 320px kuralıyla kırpılmaz; başlık title taşır', async () => {
    h.pages.mockResolvedValue(PAGE);
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<MemoryRouter><WikiPagesTable mode="sync" /></MemoryRouter>); });
    await act(async () => { await new Promise(r => setTimeout(r, 400)); });

    const link = host.querySelector<HTMLAnchorElement>(`a[href="${PAGE.rows[0].url}"]`)!;
    expect(link).not.toBeNull();
    expect(link.className).toContain('dt-trunc');
    expect(link.getAttribute('title')).toBe(LONG_TITLE);

    const btn = host.querySelector<HTMLButtonElement>(`button[aria-label="${LONG_TITLE} önizlemesi"]`)!;
    await act(async () => { btn.click(); });
    const td = host.querySelector('tr[data-dt-detail] > td')!;
    expect(td).not.toBeNull();
    expect(td.classList.contains('cell-wrap')).toBe(true);
    expect(td.querySelector('[data-testid="wiki-page-preview"]')?.textContent).toBe(PAGE.rows[0].preview);
  });
});
