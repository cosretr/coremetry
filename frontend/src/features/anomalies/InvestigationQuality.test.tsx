// @vitest-environment jsdom
// InvestigationQuality.test.tsx — CoSRE Faz B: hipotez ağacı (desteklenen
// önce, elenenler katlı + gerekçe), erişim beyanı, kanıt kapısı bandı
// YALNIZ show kipinde (shadow = gösterilmez).
import { describe, it, expect, vi, afterEach } from 'vitest';

vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createRoot, type Root } from 'react-dom/client';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import { InvestigationGateBanner, InvestigationQuality } from './InvestigationQuality';
import { gapText, hypothesisTree, reachSplit, showGate } from './investigationQualityView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });

function render(node: React.ReactNode) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(node));
  return host;
}

const h = (kind: SREHypothesis['kind'], state: SREHypothesis['state'], score: number, extra: Partial<SREHypothesis> = {}): SREHypothesis => ({
  kind, category: 'x', label: kind, score, band: 'orta', prior: 0.1, checks: [], state, ...extra,
});

const inv = (over: Partial<SREInvestigation> = {}): SREInvestigation => ({
  problemId: 'p1', service: 'checkout', kind: 'auto', state: 'done', attempts: 1, run: 1, enqueuedAt: 1, problemStartedAt: 1,
  anchor: { from: 0, to: 1 }, steps: [], findings: [],
  hypotheses: [
    h('db_slow', 'tested', 0.3),
    h('deploy', 'supported', 0.75, { stateRefs: ['deploys.evidences[0]', 'sweep'] }),
    h('external', 'ruled_out', 0, { stateReason: 'dış servis problemi yok' }),
    h('config', 'untested', 0.1),
  ],
  playbook: { type: 'failure_rate', label: 'Hata oranı artışı', checks: [] },
  reach: [
    { source: 'sweep', status: 'checked' }, { source: 'deploys', status: 'checked' },
    { source: 'deps', status: 'timed_out', note: 'süre bütçesi doldu' }, { source: 'errors', status: 'not_run' },
  ],
  coverage: { mode: 'shadow', verdict: 'insufficient', missing: [{ code: 'no_supported' }] },
  ...over,
});

describe('investigationQuality helpers', () => {
  it('ağaç: desteklenen önce, elenen ayrı; durumsuz kayıt boş', () => {
    const t = hypothesisTree(inv());
    expect(t.open.map(x => x.kind)).toEqual(['deploy', 'db_slow', 'config']);
    expect(t.ruledOut.map(x => x.kind)).toEqual(['external']);
    expect(hypothesisTree(inv({ hypotheses: [h('deploy', undefined, 0.8)] })).open).toEqual([]);
  });
  it('erişim bölünmesi + kapı yalnız show/insufficient', () => {
    const r = reachSplit(inv().reach);
    expect(r.checked.map(x => x.source)).toEqual(['sweep', 'deploys']);
    expect(r.blind.map(x => x.source)).toEqual(['deps', 'errors']);
    expect(showGate(inv().coverage)).toBe(false);
    expect(showGate({ mode: 'show', verdict: 'insufficient' })).toBe(true);
    expect(showGate({ mode: 'show', verdict: 'sufficient' })).toBe(false);
    expect(gapText({ code: 'blind_source', source: 'deps', status: 'timed_out' }, 'tr')).toBe('Bağımlılık bakılamadı (zaman aşımı)');
  });
});

describe('InvestigationQuality', () => {
  it('ağaç + plan + erişim; elenen katlı, açılınca gerekçe', () => {
    const el = render(<InvestigationQuality inv={inv()} />);
    expect(el.textContent).toContain('Plan: Hata oranı artışı');
    expect(el.querySelector('[data-state="supported"]')?.textContent).toContain('destekleniyor');
    expect(el.textContent).toContain('2 kanıt');
    expect(el.textContent).not.toContain('dış servis problemi yok');
    const btn = [...el.querySelectorAll('button')].find(b => b.textContent?.includes('Elenen 1 hipotez'))!;
    act(() => btn.click());
    expect(el.textContent).toContain('dış servis problemi yok');
    const reach = el.querySelector('[data-testid="iq-reach"]')!.textContent!;
    expect(reach).toContain('Bakılan: Servis taraması, Deploy');
    expect(reach).toContain('Bağımlılık (zaman aşımı)');
    expect(reach).toContain('Hata özeti (planlanmadı)');
  });
  it('eski / plain kayıt → çizilmez', () => {
    const el = render(<InvestigationQuality inv={inv({ hypotheses: [], reach: undefined, playbook: undefined })} />);
    expect(el.querySelector('[data-testid="iq"]')).toBeNull();
  });
  it('kapı bandı: shadow gizli, show görünür', () => {
    expect(render(<InvestigationGateBanner inv={inv()} />).querySelector('[data-testid="iq-gate"]')).toBeNull();
    act(() => root?.unmount()); host?.remove();
    const el = render(<InvestigationGateBanner inv={inv({ coverage: { mode: 'show', verdict: 'insufficient', missing: [{ code: 'no_supported' }] } })} />);
    expect(el.textContent).toContain('Kanıt yetersiz');
    expect(el.textContent).toContain('desteklenen hipotez yok');
  });
  it('ekip kararı: viewer salt-okur (otomatik durum yanında), editor iki eylem', () => {
    const ruled = inv({ hypotheses: [
      h('deploy', 'supported', 0.75, { humanState: { state: 'dismissed', by: 'op@example.test', at: 1, note: 'geri alındı, sürüyor' } }),
      h('db_slow', 'tested', 0.3, { humanState: { state: 'confirmed', at: 1 } }),
    ] });
    const tr = hypothesisTree(ruled);
    expect(tr.open.map(x => x.kind)).toEqual(['db_slow']);
    expect(tr.ruledOut.map(x => x.kind)).toEqual(['deploy']);
    const el = render(<InvestigationQuality inv={ruled} />);
    const row = el.querySelector('[data-ruling="confirmed"]')!;
    expect(row.textContent).toContain('Doğrulandı');
    expect(row.textContent).toContain('otomatik: test edildi');
    expect(el.querySelector('[data-ruling-action]')).toBeNull();
    act(() => root?.unmount()); host?.remove();
    const qc = new QueryClient();
    const ed = render(<QueryClientProvider client={qc}><InvestigationQuality inv={ruled} canEdit /></QueryClientProvider>);
    const acts = [...ed.querySelectorAll('[data-ruling="confirmed"] [data-ruling-action]')].map(b => b.textContent);
    expect(acts).toEqual(['Kararı kaldır', 'Reddedildi']);
  });
});
