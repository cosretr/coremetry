// @vitest-environment jsdom
// RootCauseLabelCard.test.tsx — CoSRE doğruluk ölçümü A1:
// viewer etiketi salt-okunur görür (düğme yok, boş durum çağrısı), editor
// "Kök nedeni gir" ile formu açar ve PUT gönderir; servis suçlusu servis
// sayfasına bağlanır, db: düğümü düz metin kalır.
import { describe, it, expect, vi, afterEach } from 'vitest';

vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { RootCauseLabelInput, RootCauseLabelResponse } from '@/lib/types';

const m = vi.hoisted(() => ({
  role: 'viewer',
  resp: {} as Record<string, unknown>,
  puts: [] as { id: string; body: unknown }[],
}));

vi.mock('@/lib/api', () => ({
  api: {
    rootCauseLabel: vi.fn(async (id: string) => m.resp[id]),
    putRootCauseLabel: vi.fn(async (id: string, body: RootCauseLabelInput) => { m.puts.push({ id, body }); return m.resp[id]; }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { RootCauseLabelCard } from './RootCauseLabelCard';
import { culpritIsService } from './incidentCaseView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.puts = []; });

const tick = () => act(async () => { await new Promise(r => setTimeout(r, 0)); });

async function render(problemId: string) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><RootCauseLabelCard problemId={problemId} /></MemoryRouter></QueryClientProvider>);
  });
  await tick();
  await tick();
  return host;
}

const CATS = ['deploy', 'config', 'capacity', 'dependency', 'code_bug', 'infra', 'data', 'external', 'noise', 'other'];
const empty: RootCauseLabelResponse = { label: null, categories: CATS };
const filled: RootCauseLabelResponse = {
  label: { problemId: 'p2', caseId: 'p:p2', cause: 'orders-db bağlantı havuzu doldu', category: 'capacity', culprit: 'orders-api',
    link: 'https://pm.example.test/1', by: 'ops@example.test', at: Date.UTC(2026, 2, 1, 10, 0) },
  categories: CATS,
};

const buttons = (el: HTMLElement) => Array.from(el.querySelectorAll('button')).map(b => b.textContent?.trim());

describe('RootCauseLabelCard', () => {
  it('viewer: boş durum çağrısı, düzenleme düğmesi yok', async () => {
    m.role = 'viewer';
    m.resp.p1 = empty;
    const el = await render('p1');
    expect(el.textContent).toContain('Kök neden');
    expect(el.textContent).toContain('Henüz girilmedi');
    expect(el.textContent).toContain('editor ya da admin');
    expect(buttons(el)).toEqual([]);
  });

  it('viewer: girilmiş etiketi salt-okunur görür; servis suçlusu bağlantı', async () => {
    m.role = 'viewer';
    m.resp.p2 = filled;
    const el = await render('p2');
    expect(el.textContent).toContain('orders-db bağlantı havuzu doldu');
    expect(el.textContent).toContain('Kapasite');
    expect(el.textContent).toContain('ops@example.test');
    const svc = Array.from(el.querySelectorAll('a')).find(a => a.textContent === 'orders-api');
    expect(svc?.getAttribute('href')).toContain('/service');
    expect(buttons(el)).toEqual([]);
    expect(el.querySelector('textarea')).toBeNull();
  });

  it('editor: formu açar, PUT gönderir', async () => {
    m.role = 'editor';
    m.resp.p3 = { ...empty, suggested: 'deploy 2.3' };
    const el = await render('p3');
    const add = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kök nedeni gir'))!;
    await act(async () => { add.click(); });
    const ta = el.querySelector('textarea') as HTMLTextAreaElement;
    expect(ta.value).toBe('deploy 2.3'); // taslak önerisiyle dolu
    const save = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Kaydet'))!;
    await act(async () => { save.click(); });
    await tick();
    expect(m.puts).toHaveLength(1);
    expect(m.puts[0].id).toBe('p3');
    expect(m.puts[0].body).toMatchObject({ cause: 'deploy 2.3', category: 'other' });
  });

  it('editor: dolu etikette düğme "Düzenle"', async () => {
    m.role = 'admin';
    m.resp.p4 = filled;
    const el = await render('p4');
    expect(buttons(el)).toEqual(['Düzenle']);
  });
});

describe('culpritIsService', () => {
  it('önekli bileşen servis değil', () => {
    expect(culpritIsService('orders-api')).toBe(true);
    expect(culpritIsService('db:orders')).toBe(false);
    expect(culpritIsService('ext:bank-gw')).toBe(false);
    expect(culpritIsService('')).toBe(false);
  });
});
