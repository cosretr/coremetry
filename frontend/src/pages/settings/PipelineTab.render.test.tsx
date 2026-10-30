// @vitest-environment jsdom
// PipelineTab.render.test.tsx — Faz 2 dilim 1 R14. ui/ DataTable kancası →
// coremetry DataTable, Modal → coremetry/overlay Dialog geçişinin davranış
// pini (işaretlemeden bağımsız: rol/etiket/metin):
//   - kural satırı kolonları basılır (ad, tür rozeti, yüklem + AND koşulu);
//   - "Enabled" kutusu tersini upsert eder (gövde kuralın tamamı);
//   - "+ New rule" gerçek bir diyalog açar (role=dialog + başlık), Cancel kapatır.
import '@/test/radixJsdom';
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { PipelineRule } from '@/lib/api';
import { ConfirmGate } from '@/components/coremetry/Confirm';
import { MemoryRouter } from 'react-router-dom';

const RULE: PipelineRule = {
  id: 'r1', name: 'drop health', kind: 'drop', signal: 'spans', enabled: true,
  when: { key: 'http.route', op: '=~', value: '^/health' },
  and: [{ key: 'metric', op: '=', value: 'http.server.duration' }],
} as PipelineRule;

const h = vi.hoisted(() => ({ upsert: vi.fn(async () => ({})) }));
vi.mock('@/lib/api', () => ({
  api: {
    listPipelineRules: async () => ({ rules: [RULE] }),
    upsertPipelineRule: h.upsert,
    deletePipelineRule: async () => ({}),
    getMetricExclusions: async () => ({ rules: [] }),
    putMetricExclusions: async () => ({ rules: [] }),
  },
}));

import { PipelineTab } from './PipelineTab';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null; let root: Root | null = null;
async function render(): Promise<HTMLElement> {
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><ConfirmGate><PipelineTab /></ConfirmGate></MemoryRouter>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  h.upsert.mockClear();
});
const btn = (txt: string) => [...document.querySelectorAll('button')].find(b => b.textContent?.trim() === txt)!;

describe('PipelineTab — coremetry DataTable + Dialog', () => {
  it('kural satırı: ad, DROP rozeti, yüklem ve AND koşulu', async () => {
    const el = await render();
    const row = [...el.querySelectorAll('tbody tr')].find(tr => tr.textContent?.includes('drop health'));
    expect(row, 'kural satırı yok').toBeTruthy();
    expect(row!.textContent).toContain('DROP');
    expect(row!.textContent).toContain('http.route =~ "^/health"');
    expect(row!.textContent).toContain('AND metric = "http.server.duration"');
  });

  it('Enabled kutusu tersini upsert eder', async () => {
    const el = await render();
    const cb = el.querySelector<HTMLElement>('[role="checkbox"][aria-label="drop health enabled"]')!;
    expect(cb.getAttribute('aria-checked')).toBe('true');
    await act(async () => { cb.click(); });
    expect(h.upsert).toHaveBeenCalledTimes(1);
    expect(h.upsert.mock.calls[0]).toEqual([{ ...RULE, enabled: false }]);
  });

  it('"+ New rule" diyalog açar, Cancel kapatır', async () => {
    await render();
    await act(async () => { btn('+ New rule').click(); });
    const dlg = document.querySelector('[role="dialog"]');
    expect(dlg, 'diyalog yok').toBeTruthy();
    expect(dlg!.textContent).toContain('New pipeline rule');
    expect((document.getElementById([...dlg!.querySelectorAll('label')].find(l => l.textContent === 'Rule name')!.htmlFor) as HTMLInputElement).required).toBe(true);
    await act(async () => { btn('Cancel').click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });
});
