// @vitest-environment jsdom
// AiToolsetsPanel.test.tsx — v0.10.1434 (CoSRE araştırma PR 1, §6.1).
// Sözleşme: 12 satır sunucu sırasıyla; sağlık rozeti durumu taşır (renk yalnız
// sapan değerde); zorunlu setin düğmesi yok; admin kapatırken onay ister ve
// PUT'a sıralı tam kapalı listeyi gönderir; viewer salt-okunur (düğme yok);
// okuma hatası tablonun durum satırında, yeniden dene ile.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AISourceToolset, AISourceToolsetsPayload } from '@/lib/types';

const NAMES = ['services', 'traces', 'problems', 'changes', 'metrics', 'logs', 'k8s', 'oracle', 'devops-code', 'devops-wiki', 'knowledge', 'meta'];
const REQUIRED = new Set(['services', 'traces', 'problems', 'meta']);
function payload(disabled: string[]): AISourceToolsetsPayload {
  const toolsets: AISourceToolset[] = NAMES.map(name => ({
    name, source: `src ${name}`, required: REQUIRED.has(name), enabled: !disabled.includes(name),
    health: name === 'metrics'
      ? { state: 'partial', backend: 'victoriametrics', notes: ['Thanos not configured'], usable: true }
      : name === 'oracle'
        ? { state: 'not_configured', detail: 'no enabled Oracle source', usable: false }
        : { state: 'ok', usable: true },
    tools: [`${name}_tool_a`, `${name}_tool_b`],
  }));
  return { toolsets, disabled, rev: disabled.length ? 1 : 0 };
}

const api = vi.hoisted(() => ({
  aiToolsets: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
  putAIToolsets: vi.fn(async (..._a: unknown[]): Promise<unknown> => null),
}));
vi.mock('@/lib/api', () => ({ api }));

import { AiToolsetsPanel } from './AiToolsetsPanel';
import { nextDisabled } from './AiToolsetsPanel';
import { ConfirmGate } from '@/components/coremetry/Confirm';

let host: HTMLDivElement | null = null; let root: Root | null = null;
function render(canEdit: boolean): HTMLElement {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  act(() => {
    root!.render(<QueryClientProvider client={qc}><ConfirmGate><AiToolsetsPanel canEdit={canEdit} /></ConfirmGate></QueryClientProvider>);
  });
  return host;
}
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  document.body.innerHTML = '';
  vi.clearAllMocks();
});
const tick = async () => {
  for (let i = 0; i < 3; i++) await act(async () => { await new Promise(r => setTimeout(r, 15)); });
};
const row = (el: HTMLElement, name: string) => el.querySelector(`tr[data-toolset="${name}"]`) as HTMLTableRowElement;
const buttonIn = (tr: HTMLElement) => tr.querySelector('button');

describe('AiToolsetsPanel', () => {
  it('12 satır, sağlık rozeti, zorunlu sette düğme yok; admin kapatır (onay + sıralı liste)', async () => {
    api.aiToolsets.mockResolvedValueOnce(payload(['devops-wiki']));
    api.putAIToolsets.mockResolvedValueOnce(payload(['devops-wiki', 'logs']));
    const el = render(true);
    await tick();
    expect(el.querySelectorAll('tbody tr[data-toolset]').length).toBe(12);
    expect(Array.from(el.querySelectorAll('tbody tr[data-toolset]')).map(r => r.getAttribute('data-toolset'))).toEqual(NAMES);
    expect(row(el, 'metrics').querySelector('[data-health]')?.getAttribute('data-health')).toBe('partial');
    // R11 (D7): sapma rozeti StatusBadge (data-status), nötr rozet shadcn Badge (data-status yok).
    expect(row(el, 'metrics').querySelector('[data-health] [data-status="warn"]')).not.toBeNull();
    expect(row(el, 'oracle').querySelector('[data-health="not_configured"]')).not.toBeNull();
    expect(row(el, 'logs').querySelector('[data-health="ok"] [data-slot="badge"]')).not.toBeNull();
    expect(row(el, 'logs').querySelector('[data-health="ok"] [data-status]')).toBeNull();
    for (const n of REQUIRED) expect(buttonIn(row(el, n))).toBeNull();
    expect(row(el, 'devops-wiki').textContent).toMatch(/Off|Kapalı/);

    act(() => { buttonIn(row(el, 'logs'))!.click(); });
    await tick();
    const dialog = document.body.querySelector('[role="dialog"], [role="alertdialog"]') as HTMLElement;
    expect(dialog).not.toBeNull();
    expect(dialog.textContent).toContain('logs');
    const confirmBtn = Array.from(dialog.querySelectorAll('button')).find(b => /Disable|Kapat/.test(b.textContent ?? '')) as HTMLButtonElement;
    act(() => { confirmBtn.click(); });
    await tick();
    expect(api.putAIToolsets).toHaveBeenCalledWith(['devops-wiki', 'logs']);
    expect(row(el, 'logs').textContent).toMatch(/Off|Kapalı/);

    // Kapalı seti açmak onay istemez.
    api.putAIToolsets.mockResolvedValueOnce(payload(['logs']));
    act(() => { buttonIn(row(el, 'devops-wiki'))!.click(); });
    await tick();
    expect(api.putAIToolsets).toHaveBeenLastCalledWith(['logs']);
  });

  it('viewer salt-okunur: düğme yok, durum görünür', async () => {
    api.aiToolsets.mockResolvedValueOnce(payload([]));
    const el = render(false);
    await tick();
    expect(el.querySelectorAll('tbody tr[data-toolset]').length).toBe(12);
    expect(el.querySelectorAll('tbody button').length).toBe(0);
    expect(el.textContent).toMatch(/Read-only|Salt okunur/);
  });

  it('okuma hatası durum satırında, yeniden dene', async () => {
    api.aiToolsets.mockRejectedValueOnce(new Error('HTTP 500: down'));
    api.aiToolsets.mockResolvedValueOnce(payload([]));
    const el = render(true);
    await tick();
    expect(el.querySelector('[data-dt-state="error"]')).not.toBeNull();
    const retry = el.querySelector('[data-dt-state="error"] button') as HTMLButtonElement;
    act(() => { retry.click(); });
    await tick();
    expect(el.querySelectorAll('tbody tr[data-toolset]').length).toBe(12);
  });

  it('nextDisabled saf: ekler / çıkarır, sıralı ve tekrarsız', () => {
    expect(nextDisabled(['logs'], 'k8s', true)).toEqual(['k8s', 'logs']);
    expect(nextDisabled(['logs', 'k8s'], 'logs', false)).toEqual(['k8s']);
    expect(nextDisabled(['logs'], 'logs', true)).toEqual(['logs']);
  });
});
