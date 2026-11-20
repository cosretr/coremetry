// @vitest-environment jsdom
// ProblemInvestigationPanel.test.tsx — v0.10.1282 (AI SRE Faz 0a): kuyruk
// sırası, "SRE rolü yok" banner'ı, sonuç, viewer salt-okunur, editor
// "Yeniden araştır" → POST.
import { describe, it, expect, vi, afterEach } from 'vitest';

// v0.10.2285 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ProblemInvestigationResponse, SREInvestigation, SREStatus } from '@/lib/types';

const m = vi.hoisted(() => ({
  role: 'viewer',
  inv: {} as Record<string, unknown>,
  status: undefined as unknown,
  posted: [] as string[],
}));

vi.mock('@/lib/api', () => ({
  api: {
    problemInvestigation: vi.fn(async (id: string) => m.inv[id]),
    sreStatus: vi.fn(async () => m.status),
    enqueueProblemInvestigation: vi.fn(async (id: string) => { m.posted.push(id); return { state: 'queued', created: true }; }),
  },
}));
vi.mock('@/components/AuthProvider', () => ({
  useAuth: () => ({ user: { username: 'op', email: 'op@x', role: m.role }, loading: false }),
}));

import { ProblemInvestigationPanel } from './ProblemInvestigationPanel';
import { sreRoleMissing } from './sreInvestigationView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; m.posted = []; });

const NOW_NS = Date.now() * 1e6;

async function render(problemId: string) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><ProblemInvestigationPanel problemId={problemId} /></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

const base: Omit<SREInvestigation, 'state'> = { problemId: 'p1', service: 'pay', kind: 'auto', attempts: 0, run: 1, enqueuedAt: NOW_NS - 60e9, problemStartedAt: NOW_NS - 120e9, anchor: { from: 0, to: 0 }, steps: [], findings: [] };

const noRole: SREStatus = { enabled: true, launcher: 'none', roleSeen: false, pods: [], queueDepth: 3, running: 0 };

describe('ProblemInvestigationPanel', () => {
  it('kuyrukta: sıra + rol yoksa banner', async () => {
    m.role = 'viewer';
    m.inv.p1 = { state: 'queued', position: 2, enabled: true, investigation: { ...base, state: 'queued' } } satisfies ProblemInvestigationResponse;
    m.status = noRole;
    const el = await render('p1');
    expect(el.textContent).toContain('Kuyrukta (2. sırada)');
    expect(el.textContent).toContain('SRE rolü yok — kuyrukta 3 iş bekliyor');
    expect(el.querySelector('button')).toBeNull(); // viewer düğme görmez; kuyruktaki işe düğme yok
  });

  it('rol görülüyorsa banner yok', async () => {
    m.inv.p1 = { state: 'queued', position: 1, enabled: true, investigation: { ...base, state: 'queued' } };
    m.status = { ...noRole, roleSeen: true, launcher: 'deployment', pods: [] };
    const el = await render('p1');
    expect(el.textContent).not.toContain('SRE rolü yok');
  });

  it('sonuç: tarama özeti + bulgular + öneri notu; editor Yeniden araştır → POST', async () => {
    m.role = 'editor';
    m.inv.p2 = {
      state: 'done', enabled: true, investigation: {
        ...base, problemId: 'p2', state: 'done', finishedAt: NOW_NS - 120e9,
        steps: [{ name: 'sweep', state: 'ok', summary: 'Sorunlu: pod OOMKilled', durationMs: 900, at: NOW_NS }],
        findings: [{ kind: 'pod', name: 'pay-7d9', level: 'crit', reasons: ['OOMKilled ×3'] }],
      },
    };
    const el = await render('p2');
    expect(el.textContent).toContain('Servis taraması (pay): Sorunlu: pod OOMKilled');
    expect(el.textContent).toContain('pay-7d9');
    expect(el.textContent).toContain('OOMKilled ×3');
    expect(el.textContent).toContain('hiçbir değişiklik uygulanmadı');
    expect(el.textContent).toContain('2 dk önce');
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent?.includes('Yeniden araştır'));
    expect(btn).toBeTruthy();
    await act(async () => { btn!.click(); });
    expect(m.posted).toEqual(['p2']);
  });

  it('kapalıyken banner, editor düğme görmez; kayıt yoksa "henüz araştırılmadı"', async () => {
    m.role = 'admin';
    m.inv.p3 = { state: 'none', enabled: false };
    const el = await render('p3');
    expect(el.textContent).toContain('AI SRE kapalı');
    expect(el.textContent).toContain('henüz araştırılmadı');
    expect(el.querySelector('button')).toBeNull();
    // v0.10.1286 — bant operatörü API'ye değil ayar ekranına yollar.
    expect(el.querySelector('a')?.getAttribute('href')).toBe('/settings/ai-sre');
    expect(el.textContent).not.toContain('/api/');
    expect(el.textContent).not.toContain('sre_agent');
  });

  it('kapalı bandı viewer\'a bağlantı değil yönlendirme cümlesi gösterir (v0.10.1286)', async () => {
    m.role = 'viewer';
    m.inv.p5 = { state: 'none', enabled: false };
    const el = await render('p5');
    expect(el.textContent).toContain('Yönetici Ayarlar → AI SRE bölümünden açabilir.');
    expect(el.querySelector('a[href="/settings/ai-sre"]')).toBeNull();
  });

  it('paylaşılan kayıt diğer problemin araştırmasına bağlanır', async () => {
    m.role = 'viewer';
    m.inv.p4 = { state: 'shared', enabled: true, investigation: { ...base, problemId: 'p4', state: 'shared', sharedFrom: 'p1' } };
    const el = await render('p4');
    expect(el.querySelector('a')?.getAttribute('href')).toBe('/problems?problem=p1');
  });

  it('sreRoleMissing saf koşulu', () => {
    expect(sreRoleMissing(undefined)).toBe(false);
    expect(sreRoleMissing(noRole)).toBe(true);
    expect(sreRoleMissing({ ...noRole, queueDepth: 0 })).toBe(false);
    expect(sreRoleMissing({ ...noRole, launcher: 'inprocess' })).toBe(false);
    expect(sreRoleMissing({ ...noRole, roleSeen: true })).toBe(false);
  });
});
