// @vitest-environment jsdom
//
// v0.10.2341 (entity grafı S4) — katlanan kümenin üyeleri satırdan açılır;
// küme alanı yoksa (S4 kapalı) hiçbir şey çizilmez.
import { describe, it, expect, vi } from 'vitest';

vi.mock('uplot', () => ({ default: function UPlot() {} })); // InboxCells → ProblemDetail zinciri; jsdom'da matchMedia yok
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { GraphClusterMembers } from './InboxCells';
import type { InboxCluster } from '@/lib/types';

const cluster: InboxCluster = {
  ownerId: 'p-db', entities: 2, members: [
    { problemId: 'p-db', name: 'orders-db', startedAt: 1, owner: true, priority: 'P2' },
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
