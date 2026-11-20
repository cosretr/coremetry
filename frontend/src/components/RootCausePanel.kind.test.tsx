// @vitest-environment jsdom
//
// RootCausePanel.kind — v0.10.1495 (operatör, prod `runtime:jvm-gc:<svc>:<pod>`):
// "Where errors concentrate — top attribute over the error spans" bir GC
// problemi için `messaging.message.body.size` gösterdi; manşet "services below
// moved — bağlantı doğrulanamadı" derken aynı servisler bağlı gibi tabloda.
//
// NE ÇİVİLİYOR: kıyas bölümü problem türünden (sunucunun bubbleMode'u): none →
// bölüm YOK; window → hata dili yok; errors → eski başlık. Topoloji kenarı
// olmayan co-mover'lar "What else changed" tablosunda DEĞİL, ayrı kapalı
// sayımlı grupta. Blast radius bağın türünü söyler. Servis adları sentetik.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import type { ChangedService, RootCause, RootCauseBubbleUp } from '@/lib/types';

const h = vi.hoisted(() => ({ rc: null as unknown, bu: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: {
    problemRootCauseCore: () => Promise.resolve(h.rc),
    problemRootCauseBubbleUp: () => Promise.resolve(h.bu),
  },
}));

import { RootCausePanel } from './RootCausePanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const chg = (service: string, relation?: ChangedService['relation']): ChangedService => ({
  service, relation, baselineRate: 1, currentRate: 2, rateDeltaPct: 100, baselineErrorRate: 0, currentErrorRate: 0,
  errDeltaPct: 0, baselineP99Ms: 10, currentP99Ms: 30, p99DeltaPct: 200, score: 40, reasons: [`${service} p99 ↑`], direction: 'worse',
});

const bubble = {
  selectionTotal: 40, baselineTotal: 900, attributes: [{
    key: 'k8s.pod.name', values: [{ value: 'core-api-0', selectionCount: 30, baselineCount: 90, selectionPct: 0.75, baselinePct: 0.1, score: 0.65 }],
  }],
};

function rc(over: Partial<RootCause>): RootCause {
  return {
    problemId: 'p1', service: 'core-api', metric: 'jvm_gc_pause_ratio', startedAt: 1.7e18, fromNs: 1.7e18, toNs: 1.7e18 + 6e11,
    correlations: [], topologyKnown: false,
    blastRadius: { service: 'core-api', windowSec: 600, totalCallers: 2, cascadingCallers: 0, totalRps: 3, totalErrorsPerSec: 0,
      callers: [{ service: 'shop-bff', calls: 100, errors: 0, rps: 1, errorRate: 0, hasOpenProblem: false }] },
    ...over,
  };
}

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(core: RootCause, bu: RootCauseBubbleUp | null): Promise<HTMLElement> {
  h.rc = core; h.bu = bu;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter><RootCausePanel problemId="p1" service="core-api" /></MemoryRouter>); });
  await act(async () => { await Promise.resolve(); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

describe('RootCausePanel — kıyas bölümü türe göre', () => {
  it('prod: runtime (none) — "errors concentrate" YOK, manşet localized, co-mover\'lar ayrı kapalı grup', async () => {
    const el = await mount(rc({ bubbleMode: 'none', correlations: [chg('shop-bff'), chg('infra-agent'), chg('crm-api')] }),
      { fromNs: 0, toNs: 1, mode: 'none' });
    const text = el.textContent ?? '';
    expect(text).not.toMatch(/errors concentrate|What stands out|Bu pencerede öne çıkan/i);
    expect(text).toContain('localized to');
    // bağlantısız co-mover'lar tabloda değil
    expect(text).not.toContain('What else changed');
    const grp = el.querySelector('[data-co-movers]')!;
    expect(grp.textContent).toMatch(/\(3\)/);
    expect(grp.querySelector('[aria-expanded="false"]')).not.toBeNull();
    expect(grp.textContent).not.toContain('infra-agent'); // kapalı
    expect(grp.textContent).toMatch(/topology unavailable|topoloji okunamadı/);
    expect(el.querySelector('[data-blast-relation]')?.textContent).toMatch(/call edges|çağrı kenarı/);
  });

  it('gecikme (window) — hata dili yok, önceki pencereye göre', async () => {
    const el = await mount(rc({ metric: 'p99_ms', bubbleMode: 'window' }), { fromNs: 0, toNs: 6e11, mode: 'window', bubbleUp: bubble });
    const text = el.textContent ?? '';
    expect(text).not.toMatch(/errors concentrate|error spans/i);
    expect(text).toMatch(/What stands out in this window|Bu pencerede öne çıkan/);
    expect(text).toContain('stands out');
  });

  it('hata (errors) — eski başlık ve manşet', async () => {
    const el = await mount(rc({ metric: 'error_rate', bubbleMode: 'errors' }), { fromNs: 0, toNs: 6e11, mode: 'errors', bubbleUp: bubble });
    expect(el.textContent).toMatch(/Where errors concentrate|Hatalar nerede yoğunlaşıyor/);
    expect(el.textContent).toContain('Errors concentrate in');
  });

  it('kenarlı co-mover tabloda, kenarsız ayrı grupta — asla karışık değil', async () => {
    const el = await mount(rc({ metric: 'p99_ms', bubbleMode: 'window', topologyKnown: true,
      correlations: [chg('orders-api', 'downstream'), chg('shop-bff')] }), { fromNs: 0, toNs: 1, mode: 'window' });
    const table = Array.from(el.querySelectorAll('table')).find(t => t.textContent?.includes('What changed'))!;
    expect(table.textContent).toContain('orders-api');
    expect(table.textContent).not.toContain('shop-bff');
    expect(el.querySelector('[data-co-movers]')?.textContent).toMatch(/\(1\)/);
  });
});
