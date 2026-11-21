// @vitest-environment jsdom
//
// SreReport — CoSRE Faz C2: rapor sayfası (salt-okunur) + saf yardımcılar.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter, Routes, Route } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '@/lib/api';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import SreReport from './SreReport';
import { isSreReportPage, reportConclusion, reportProblemId } from './sreReportView';

const hyp = (over: Partial<SREHypothesis>): SREHypothesis => ({
  kind: 'deploy', category: 'deploy', label: 'Deploy', score: 0.8, band: 'yüksek', prior: 0.2,
  checks: [{ name: 'deploy_near', outcome: 1, weight: 0.35, reason: '4 dk önce svc-a v1.2.3' }],
  state: 'supported', stateRefs: ['deploys.evidences[0]', 'sweep'], ...over,
});

const inv = (over: Partial<SREInvestigation>): SREInvestigation => ({
  problemId: 'p-1', displayId: 'P-101', service: 'svc-a', kind: 'auto', state: 'done', attempts: 1, run: 1,
  enqueuedAt: 1, finishedAt: 2e18, problemStartedAt: 1, anchor: { from: 0, to: 1 },
  steps: [{ name: 'deploys', state: 'ok', summary: '1 deploy', durationMs: 10, at: 1 }],
  findings: [], hypotheses: [hyp({})],
  reach: [{ source: 'deploys', status: 'checked' }, { source: 'pivot', status: 'timed_out' }],
  ...over,
});

describe('sreReportView', () => {
  it('rapor kimliği ↔ problem kimliği ve kromsuz yol', () => {
    expect(reportProblemId('sre-p-1')).toBe('p-1');
    expect(reportProblemId('p-1')).toBe('p-1');
    expect(isSreReportPage('/sre/report/sre-p-1')).toBe(true);
    expect(isSreReportPage('/sre/report/')).toBe(false);
    expect(isSreReportPage('/sre/reports/x')).toBe(false);
  });
  it('kapı yetersizse neden yok; karar yoksa ≥2 kaynak kuralı', () => {
    expect(reportConclusion(inv({ coverage: { mode: 'shadow', verdict: 'insufficient', missing: [] } })).kind).toBe('insufficient');
    expect(reportConclusion(inv({ coverage: { mode: 'shadow', verdict: 'sufficient', top: 'deploy' } })).kind).toBe('cause');
    expect(reportConclusion(inv({})).kind).toBe('cause');
    expect(reportConclusion(inv({ hypotheses: [hyp({ stateRefs: ['deploys'] })] })).kind).toBe('insufficient');
    expect(reportConclusion(inv({ hypotheses: [] })).kind).toBe('none');
  });
});

describe('SreReport', () => {
  let root: Root | null = null;
  let host: HTMLDivElement | null = null;
  afterEach(() => {
    act(() => root?.unmount());
    host?.remove();
    vi.restoreAllMocks();
  });

  async function mount(i: SREInvestigation) {
    vi.spyOn(api, 'problemInvestigation').mockResolvedValue({ state: i.state, enabled: true, investigation: i });
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root!.render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/sre/report/sre-p-1']}>
            <Routes><Route path="/sre/report/:id" element={<SreReport />} /></Routes>
          </MemoryRouter>
        </QueryClientProvider>,
      );
    });
    await act(async () => { await new Promise(r => setTimeout(r, 0)); });
    return host!;
  }

  it('neden, hipotez ağacı, kör noktalar ve probleme dönüş bağlantısı', async () => {
    const el = await mount(inv({ coverage: { mode: 'shadow', verdict: 'sufficient', top: 'deploy' } }));
    expect(api.problemInvestigation).toHaveBeenCalledWith('p-1', expect.anything());
    expect(el.querySelector('[data-testid="srr-cause"]')?.textContent).toContain('Deploy');
    expect(el.querySelector('[data-testid="srr-blind"]')?.textContent).toMatch(/pivot/i);
    expect(el.querySelector('a[href="/problems?problem=p-1"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="srr-insufficient"]')).toBeNull();
  });

  it('kanıt yetersiz → neden yazılmaz', async () => {
    const el = await mount(inv({ coverage: { mode: 'shadow', verdict: 'insufficient', missing: [{ code: 'no_supported' }] } }));
    expect(el.querySelector('[data-testid="srr-insufficient"]')).not.toBeNull();
    expect(el.querySelector('[data-testid="srr-cause"]')).toBeNull();
  });
});
