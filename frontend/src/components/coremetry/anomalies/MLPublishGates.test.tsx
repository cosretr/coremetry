// @vitest-environment jsdom
//
// v0.10.2343 — yayın kapısı gölge raporu (MLPublishGates.tsx, K16 incelemesi):
// dedektör satırları, kip satırı (A2 kapalı / açık), ölçüt, örnek olay
// bağlantıları; TR ve EN metin. Servis/olay adları SENTETİK.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { MLGateShadow, MLTickStatus } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'tr' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

import { MLPublishGatesSection, publishGateRows } from './MLPublishGates';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const g = (o: Partial<MLGateShadow> = {}): MLGateShadow => ({
  evaluated: 0, wouldPublish: 0, wouldDrop: 0, wouldInfo: 0, wouldTelemetryVolume: 0, notEvaluated: 0, invalid: 0,
  wouldDropProblem: 0, wouldDropEscalated: 0, ...o,
});
const tick = (o: Partial<MLTickStatus> = {}): MLTickStatus => ({
  at: 0, durationMs: 0, events: 0, backoff: [], publishGatesWindowH: 24, publishGatesMode: 'shadow',
  publishGates: {
    op_latency: g({ evaluated: 10, wouldPublish: 6, wouldDrop: 3, wouldInfo: 1, wouldDropProblem: 1, wouldDropEscalated: 2, dropExamples: ['abcdef0123456789'] }),
    svc_volume_spike: g({ notEvaluated: 4 }),
  },
  ...o,
});

let root: Root | null = null;
let host: HTMLDivElement | null = null;
function render(t: MLTickStatus) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><MLPublishGatesSection tick={t} /></MemoryRouter>); });
  return host;
}
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'tr'; });

describe('MLPublishGates', () => {
  it('publishGateRows: değerlendirilmeyen dedektör düşer', () => {
    expect(publishGateRows(tick()).map(r => r.detector)).toEqual(['op_latency']);
    expect(publishGateRows(undefined)).toEqual([]);
  });
  it('TR: gölge kip, ölçüt (yalnız üç kapı), örnek olay bağlantısı', () => {
    const el = render(tick());
    expect(el.querySelector('[data-testid="ml-publish-gates-mode"]')?.textContent).toContain('Kip: gölge');
    expect(el.textContent).toContain('en az 100 çağrı');
    expect(el.textContent).toContain('Tutulurdu');
    const a = el.querySelector('a[href="/anomalies/ml/abcdef0123456789"]');
    expect(a?.textContent).toBe('abcdef01');
  });
  it('EN + açık kip', () => {
    m.lang = 'en';
    const el = render(tick({ publishGatesMode: 'on', publishGatesEnforced: true }));
    expect(el.querySelector('[data-testid="ml-publish-gates-mode"]')?.textContent).toContain('Mode: on');
    expect(el.textContent).toContain('Would hold');
  });
  it('tanınmayan kip (eski tik durumu): kip satırı çizilmez', () => {
    const el = render(tick({ publishGatesMode: 'A2: kapalı' }));
    expect(el.querySelector('[data-testid="ml-publish-gates-mode"]')).toBeNull();
  });
});
