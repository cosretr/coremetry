// @vitest-environment jsdom
//
// AffectedEntitiesList.render — v0.10.1495 (operatör, prod JVM GC problemi:
// "AFFECTED ENTITIES (37)" hata görmeyen onlarca servisi etkilenen sayıyordu).
// NE ÇİVİLİYOR: sayım yalnız doğrulanmış + etkisi gözlenmiş varlıklar; her hap
// bağın türünü yazar; bağlı-etkisiz çağıranlar ayrı KAPALI grupta; topoloji
// okunamadı / pod kapsamı / cluster doğrulanamadı satırları. Servis adları sentetik.
import { describe, it, expect, afterEach, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ProblemAffectedResponse } from '@/lib/types';

const h = vi.hoisted(() => ({ res: null as unknown }));
vi.mock('@/lib/api', () => ({ api: { problemAffected: () => Promise.resolve(h.res) } }));

import { AffectedEntitiesList } from './AffectedEntitiesList';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement | null = null;
let root: Root | null = null;
async function mount(res: ProblemAffectedResponse): Promise<HTMLElement> {
  h.res = res;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><MemoryRouter><AffectedEntitiesList problemId="p1" service="core-api" /></MemoryRouter></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}
afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

const base: ProblemAffectedResponse = {
  problemId: 'p1', displayId: 'P-1', service: 'core-api', windowFromNs: 0, windowToNs: 1,
  entities: [{ kind: 'pod', id: 'core-api-7d9f-x', relation: 'subject-pod' }, { kind: 'cluster', id: 'prod-eu', relation: 'cluster' }],
  total: 2,
  linked: [{ kind: 'service', id: 'shop-bff', relation: 'caller', calls: 900 }, { kind: 'service', id: 'crm-api', relation: 'caller', calls: 40 }],
  linkedTotal: 2, topologyKnown: true, podScoped: true,
};

describe('AffectedEntitiesList', () => {
  it('prod: pod kapsamlı GC — sayım 2 (pod + cluster), etkisiz çağıranlar kapalı ayrı grupta', async () => {
    const el = await mount(base);
    const verified = el.querySelector('[data-affected-verified]')!;
    expect(el.textContent).toMatch(/\(2\)/);
    expect(verified.textContent).toContain('core-api-7d9f-x');
    expect(verified.textContent).toMatch(/problem pod/);
    expect(verified.textContent).not.toContain('shop-bff');
    const linked = el.querySelector('[data-affected-linked]')!;
    expect(linked.textContent).toMatch(/\(2\)/);
    expect(linked.querySelector('[aria-expanded="false"]')).not.toBeNull();
    expect(linked.textContent).not.toContain('shop-bff'); // kapalı
    await act(async () => { (linked.querySelector('button') as HTMLButtonElement).click(); });
    expect(linked.textContent).toContain('shop-bff');
    expect(linked.textContent).toMatch(/caller|çağıran/);
    expect(el.querySelector('[data-affected-note]')?.textContent).toMatch(/Pod-scoped|Pod kapsamlı/);
  });
  it('topoloji okunamadı — söylenir, yalnız doğrulanabilen', async () => {
    const el = await mount({ ...base, topologyKnown: false, linked: [], linkedTotal: 0, clustersUnverified: true, entities: [base.entities[0]], total: 1 });
    const notes = Array.from(el.querySelectorAll('[data-affected-note]')).map(n => n.textContent).join(' ');
    expect(notes).toMatch(/Topology \(callers\) unavailable|Topoloji \(çağıranlar\) okunamadı/);
    expect(notes).toMatch(/several clusters|birden çok cluster/);
    expect(el.querySelector('[data-affected-linked]')).toBeNull();
  });
});
