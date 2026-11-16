// @vitest-environment jsdom
//
// ProblemGraphTable.render — v0.10.2275 (entity grafı S1): üyeler tablosu
// başlangıç sırasını korur, gerekçe çipi kural kimliğidir, graf verisi
// olmayan üye nötr notla görünür, bağlanmayanlar not satırı nedeni yazar.
import { describe, it, expect } from 'vitest';
import type { JSX } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { tf } from '@/lib/i18n';
import type { GraphT, ProblemGraphMember, ProblemGraphNearMiss } from '@/lib/problemGraph';
import { ProblemGraphMembers, ProblemGraphNearMisses } from './ProblemGraphTable';

function mount(el: JSX.Element) {
  const div = document.createElement('div');
  div.innerHTML = renderToStaticMarkup(el);
  const all = (id: string) => Array.from(div.querySelectorAll(`[data-testid="${id}"]`));
  return { all, one: (id: string) => all(id)[0] };
}

const tr: GraphT = (k, v) => tf(k, 'tr', v);
const en: GraphT = (k, v) => tf(k, 'en', v);
const clock = (ns: number) => `t${ns}`;

const members: ProblemGraphMember[] = [
  { id: 'p-db', entity: 'db:postgresql@orders-db', type: 'database', name: 'postgresql@orders-db', startedAt: 1, link: 'M-CALL', path: ['cart-api', 'postgresql@orders-db'], title: 'Query latency' },
  { id: 'p-cart', entity: 'svc:cart-api', type: 'service', name: 'cart-api', startedAt: 2, owner: true },
  { id: 'p-cart-2', entity: 'svc:cart-api', type: 'service', name: 'cart-api', startedAt: 3, link: 'M-SVC', peer: 'cart-api', graphMissing: true },
];
const near: ProblemGraphNearMiss[] = [{ id: 'p-search', name: 'search-api', type: 'service', startedAt: 4, reason: 'N-REL' }];

describe('ProblemGraphMembers', () => {
  it('renders rows in server order with rule chips and the no-graph note', () => {
    const s = mount(<ProblemGraphMembers members={members} near={near} t={tr} clock={clock} />);
    const rows = s.all('pg-member-row');
    expect(rows).toHaveLength(3);
    expect(rows[0].textContent).toContain('cart-api, postgresql@orders-db hedefini çağırıyor');
    expect(rows[0].textContent).toContain('M-CALL');
    expect(rows[0].textContent).toContain('cart-api → postgresql@orders-db');
    expect(rows[1].textContent).toContain('ilk açılan Problem');
    expect(rows[2].textContent).toContain('aynı servis');
    expect(s.one('pg-graph-missing').textContent).toContain('graf verisi yok');
    expect(s.one('pg-near-note').textContent).toBe("Bağlanmadı: search-api (t4) bu Problem'e bağlanmadı: graf ilişkisi yokN-REL");
  });
  it('English sentences come from the same rule ids', () => {
    const s = mount(<ProblemGraphMembers members={members} near={[]} t={en} clock={clock} />);
    expect(s.all('pg-member-row')[0].textContent).toContain('cart-api calls postgresql@orders-db');
  });
});

describe('ProblemGraphNearMisses', () => {
  it('shows the reason with hint wording', () => {
    const s = mount(<ProblemGraphNearMisses near={[{ id: 'p-x', name: 'search-api', type: 'service', startedAt: 5, reason: 'N-REL', hint: 'M-NODE', via: 'node-a-01', groupSize: 4 }]} t={tr} clock={clock} />);
    const t = s.one('pg-near').textContent ?? '';
    expect(t).toContain('aynı node (node-a-01), ama sapma o node ile sınırlı değil');
    expect(t).toContain('4 varlık');
  });
});
