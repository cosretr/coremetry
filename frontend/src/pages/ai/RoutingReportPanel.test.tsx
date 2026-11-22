// @vitest-environment jsdom
// RoutingReportPanel.test.tsx — v0.10.2448 (operatör, 2026-11-22: "kimler
// yazmış göremiyorum"): "Kim" kolonu yalnız admin'de; e-postasız eski tur "—";
// editör kolonu hiç görmez (sunucu da e-postayı göndermez).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { RoutingReport } from '@/lib/types';

const m = vi.hoisted(() => ({ role: 'admin' as string }));

const report: RoutingReport = {
  days: 7, turns: 4, suspicious: 2, truncated: false, generatedAt: 0, tiers: [],
  items: [
    { exchangeId: 'x1', createdAt: '2026-11-20T10:00:00Z', userEmail: 'asker@example.test', question: 'orders-api hata veriyor mu',
      tier: 'loop', score: 3, reasons: ['error'] },
    { exchangeId: 'x2', createdAt: '2026-11-20T09:00:00Z', question: 'eski tur', tier: 'loop', score: 3, reasons: ['error'] },
  ],
};

vi.mock('@/lib/api', () => ({ api: { aiRoutingReport: vi.fn(async () => report) } }));
vi.mock('@/components/AuthProvider', () => ({ useAuthRole: () => m.role }));

import { RoutingReportPanel } from './RoutingReportPanel';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.role = 'admin'; });

async function render() {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><RoutingReportPanel /></MemoryRouter>); });
  await act(async () => { await Promise.resolve(); });
  return host;
}

const headers = (h: HTMLElement) => Array.from(h.querySelectorAll('th')).map(th => (th.textContent ?? '').replace(/[↕↑↓]/g, '').trim());

describe('RoutingReportPanel — kim sordu (v0.10.2448)', () => {
  it('admin: Kim kolonu e-postayı, eski turda "—" gösterir', async () => {
    const h = await render();
    expect(headers(h)).toContain('Kim');
    const rows = Array.from(h.querySelectorAll('tbody tr'));
    expect(rows).toHaveLength(2);
    expect(rows[0].textContent).toContain('asker@example.test');
    expect(rows[1].textContent).toContain('—');
  });

  it('editör: Kim kolonu yok', async () => {
    m.role = 'editor';
    const h = await render();
    expect(headers(h)).not.toContain('Kim');
    expect(h.textContent).not.toContain('asker@example.test');
  });
});
