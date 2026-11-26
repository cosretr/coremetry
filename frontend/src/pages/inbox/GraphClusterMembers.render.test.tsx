// @vitest-environment jsdom
//
// v0.10.2341 (entity grafı S4) — katlanan kümenin üyeleri satırdan açılır;
// küme alanı yoksa (S4 kapalı) hiçbir şey çizilmez.
import { describe, it, expect, vi } from 'vitest';

vi.mock('uplot', () => ({ default: function UPlot() {} })); // InboxCells → ProblemDetail zinciri; jsdom'da matchMedia yok
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { GraphClusterMembers, RowChips, graphClusterSummary } from './InboxCells';
import type { InboxCluster, InboxItem } from '@/lib/types';

const cluster: InboxCluster = {
  ownerId: 'p-db', entities: 2, services: 1, problems: 3, members: [
    { problemId: 'p-db', name: 'orders-db', type: 'database', startedAt: 1, owner: true, priority: 'P2' },
    { problemId: 'p-co', name: 'checkout-api', startedAt: 2, link: 'M-CALL' },
    { problemId: 'p-co-2', name: 'checkout-api', startedAt: 3, link: 'M-SVC', late: true },
  ],
};

function mount(c?: InboxCluster) {
  const div = document.createElement('div');
  div.innerHTML = renderToStaticMarkup(
    <MemoryRouter><GraphClusterMembers cluster={c} hrefOf={id => ({ to: `/inbox?problem=${id}`, replace: true })} /></MemoryRouter>);
  return div;
}

describe('GraphClusterMembers', () => {
  it('lists every folded member with its own detail link', () => {
    const d = mount(cluster);
    const rows = Array.from(d.querySelectorAll('[data-testid="pg-cluster-member"]'));
    expect(rows).toHaveLength(3);
    expect(rows[1].querySelector('a')?.getAttribute('href')).toBe('/inbox?problem=p-co');
    expect(rows[1].textContent).toContain('M-CALL');
    expect(rows[2].textContent).toMatch(/geç katıldı|joined late/); // arayüz dili
  });
  it('renders nothing without a cluster (S4 off)', () => {
    expect(mount(undefined).innerHTML).toBe('');
  });
});

// v0.10.2473 — özet satırı etkilenen servis sayısını ve açılan listedeki
// Problem sayısını söyler; satır kapalı gelir, tıklayınca açılır / kapanır.
describe('GraphClusterMembers summary + expand (v0.10.2473)', () => {
  const tt = (k: string, v: Record<string, string | number>) => `${k}:${JSON.stringify(v)}`;
  it('service count from the whole cluster; Problem count = listed members', () => {
    const chain: InboxCluster = {
      ownerId: 'p-pay', entities: 3, services: 3, problems: 3, members: [
        { problemId: 'p-pay', name: 'payments', type: 'service', startedAt: 1, owner: true },
        { problemId: 'p-gw', name: 'api-gateway', type: 'service', startedAt: 2, link: 'M-CALL' },
        { problemId: 'p-co', name: 'checkout', type: 'service', startedAt: 3, link: 'M-CALL' },
      ],
    };
    expect(graphClusterSummary(chain, tt)).toBe('pg.cluster.summary:{"s":3,"m":3}');
    // Servis sayısı bilinmiyor (üye tavanı kesti) → varlık sayısı.
    expect(graphClusterSummary({ ...chain, services: 0 }, tt)).toBe('pg.cluster.summaryEnt:{"n":3,"m":3}');
    // Süzgeç üyelerden birini eledi: sayılan Problem açılan listeyle aynı.
    expect(graphClusterSummary({ ...chain, members: chain.members.slice(0, 2) }, tt)).toBe('pg.cluster.summary:{"s":3,"m":2}');
  });
  it('collapsed by default, toggles open and closed', () => {
    const d = document.createElement('div');
    document.body.appendChild(d);
    d.innerHTML = renderToStaticMarkup(
      <MemoryRouter><GraphClusterMembers cluster={cluster} hrefOf={id => ({ to: `/inbox?problem=${id}` })} /></MemoryRouter>);
    const det = d.querySelector('details[data-testid="pg-cluster"]') as HTMLDetailsElement;
    expect(det.open).toBe(false);
    expect(d.querySelector('[data-testid="pg-cluster-summary"]')?.textContent).toMatch(/1 servis etkilendi · 3 Problem|1 services affected · 3 Problems/);
    (det.querySelector('summary') as HTMLElement).click();
    expect(det.open).toBe(true);
    (det.querySelector('summary') as HTMLElement).click();
    expect(det.open).toBe(false);
    d.remove();
  });
});

// v0.10.2342 (entity grafı S5, karar 5) — "yinelenen" çipi Problem satırında
// yalnız S5 açıkken (sunucu frequentEnforce) yerini "sık tekrar eden"e bırakır.

describe('RowChips recurring vs frequent (S5)', () => {
  const it0 = {
    id: 'problem:p-1', kind: 'problem', source: 'Anomaly', priority: 'P3', priorityReason: '', severity: 'warning', service: 'checkout-api',
    title: 'x', description: '', startedAt: 1, lastSeen: 1, status: 'open',
    problem: { id: 'p-1', ruleId: 'anomaly-auto:checkout-api:error_rate', metric: '', value: 0, threshold: 0, episodeCount: 4, firstStartedAt: 1 },
  } as unknown as InboxItem;
  const marks = (fe?: boolean) => {
    const d = document.createElement('div');
    d.innerHTML = renderToStaticMarkup(<MemoryRouter><RowChips it={it0} real={false} frequentEnforce={fe} /></MemoryRouter>);
    return d.querySelectorAll('[data-recurring]').length;
  };
  it('keeps today\'s marker when S5 is off and drops it when on', () => {
    expect(marks(undefined)).toBe(1);
    expect(marks(true)).toBe(0);
  });
});
