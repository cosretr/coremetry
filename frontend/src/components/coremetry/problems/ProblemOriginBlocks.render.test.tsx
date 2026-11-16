// @vitest-environment jsdom
//
// ProblemOriginBlocks.render — v0.10.2279 (entity grafı S2): köken satırı
// güven rozetiyle, etki alanı en çok 3 sabit satır, kanıt listesi kural
// sırasıyla; yol çizimi köken + üyeler + soluk çağıranlar, tema token'ları.
import { describe, it, expect } from 'vitest';
import type { JSX } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { tf } from '@/lib/i18n';
import type { GraphT, ProblemGraphMember, ProblemGraphOrigin } from '@/lib/problemGraph';
import { BlastTable, OriginEvidence, OriginLine } from './ProblemOriginBlocks';
import ProblemGraphDiagram from './ProblemGraphDiagram';

function mount(el: JSX.Element) {
  const div = document.createElement('div');
  div.innerHTML = renderToStaticMarkup(el);
  const all = (id: string) => Array.from(div.querySelectorAll(`[data-testid="${id}"]`));
  return { div, all, one: (id: string) => all(id)[0] };
}
const tr: GraphT = (k, v) => tf(k, 'tr', v);
const clock = (ns: number) => `t${ns}`;

const origin: ProblemGraphOrigin = {
  entity: 'db:orders-db', name: 'orders-db', type: 'database', confidence: 'weak', rule: 'K-DEEP', leads: ['K-DEEP'],
  onsetAt: 2, earliest: 'orders-db', earliestAt: 2,
  rules: [
    { rule: 'K-ROLLOUT', separated: false, source: 'rollout' },
    { rule: 'K-SHARED', separated: false, source: 'calls' },
    { rule: 'K-DEEP', separated: true, leader: 'orders-db', source: 'calls' },
    { rule: 'K-FIRST', separated: false, leader: 'orders-db', at: 2, runner: 'cart-api', runAt: 4, source: 'onset' },
  ],
  blast: { affected: ['cart-api'], atRisk1: ['invoice-api', 'order-history'], atRisk1N: 2, atRisk2: ['web-gateway'], atRisk2N: 11 },
};
const members: ProblemGraphMember[] = [
  { id: 'p-db', entity: 'db:orders-db', type: 'database', name: 'orders-db', startedAt: 2, link: 'M-CALL', path: ['cart-api', 'orders-db'] },
  { id: 'p-cart', entity: 'svc:cart-api', type: 'service', name: 'cart-api', startedAt: 4, owner: true },
];

describe('ProblemOriginBlocks', () => {
  it('origin line carries the sentence, confidence and preview badge', () => {
    const m = mount(<OriginLine origin={origin} t={tr} clock={clock} />);
    expect(m.one('pg-origin-line').textContent).toContain('Olası köken');
    expect(m.one('pg-origin-line').textContent).toContain('orders-db: çağrı zincirinin en derin halkası; ilk sapma t2.');
    expect(m.one('pg-origin-conf').textContent).toBe('zayıf · 1 kural');
    expect(m.one('pg-origin-line').textContent).toContain('gölge');
  });
  it('blast radius: three fixed rows, first names then +N', () => {
    const m = mount(<BlastTable origin={origin} t={tr} />);
    const rows = m.all('pg-blast-row');
    expect(rows).toHaveLength(3);
    expect(rows[1].textContent).toContain('invoice-api, order-history');
    expect(rows[2].textContent).toContain('web-gateway +10');
  });
  it('blast radius off: only the affected row and the note', () => {
    const m = mount(<BlastTable origin={{ ...origin, blast: { affected: ['cart-api'], atRisk1N: 0, atRisk2N: 0, off: true } }} t={tr} />);
    expect(m.all('pg-blast-row')).toHaveLength(1);
    expect(m.div.textContent).toContain('hesaplanmadı');
  });
  it('evidence in rule order with sources', () => {
    const m = mount(<OriginEvidence origin={origin} t={tr} clock={clock} />);
    const rows = m.all('pg-evidence-row').map(r => r.textContent ?? '');
    expect(m.all('pg-evidence-row').map(r => r.firstElementChild?.textContent)).toEqual(['K-ROLLOUT', 'K-SHARED', 'K-DEEP', 'K-FIRST']);
    expect(rows[2]).toContain('entity grafı · calls kenarları');
  });
  it('diagram: origin highlighted, calm callers dashed, theme tokens only', () => {
    const m = mount(<ProblemGraphDiagram members={members} origin={origin} t={tr} clock={clock} />);
    expect(m.all('pg-node-origin')).toHaveLength(1);
    expect(m.all('pg-node-member')).toHaveLength(1);
    expect(m.all('pg-node-calm')).toHaveLength(2);
    expect(m.all('pg-edge-call')).toHaveLength(1);
    const svg = m.div.querySelector('svg')!.outerHTML;
    expect(svg).toContain('var(--err-bg)');
    expect(svg).not.toMatch(/#[0-9a-f]{6}/i);
  });
});
