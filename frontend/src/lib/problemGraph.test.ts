import { describe, it, expect } from 'vitest';
import { graphShadowLines, type ProblemGraphShadowReport } from './problemGraph';

const base: ProblemGraphShadowReport = {
  at: 1, since: 0, windowH: 24, units: 4, callStrict: 1, graphMissing: 2,
  reads: { passes: 10, skippedFew: 3, refreshes: 2, relations: 5, maxRelPerPass: 3, runs: 2, evidence: 1, errors: 0 },
};

describe('graphShadowLines', () => {
  it('lists both M-CALL variants, reasons, sizes and age buckets in fixed order', () => {
    const l = graphShadowLines({ ...base, links: { 'M-CALL': 3, 'M-SVC': 1 }, noLink: { 'N-REL': 2, 'N-HUB': 1 },
      sizes: { '3': 1, '2': 2 }, sizesStrict: { '2': 1 }, age: { '>90': 1, '0-30': 4 } });
    expect(l[1]).toBe('Kurulurdu (farklı bağ): M-SVC 1 · M-CALL 3 · M-CALL katı sürüm (çağıranda bağımlılık olayı): 1');
    expect(l[2]).toContain('N-REL 2 · N-HUB 1');
    expect(l[3]).toBe('Küme boyu, düz M-CALL: 2 varlık 2 · 3 varlık 1 · katı M-CALL: 2 varlık 1');
    expect(l[4]).toBe('Bağlanırken sahibin yaşı (servisler arası): 0-30 dk 4 · >90 dk 1');
    expect(l[6]).toContain('entity_relations 5 (tur başına en çok 3)');
  });
  it('says "yok" for empty counters', () => {
    expect(graphShadowLines(base)[1]).toContain('Kurulurdu (farklı bağ): yok');
  });
});

import { graphSectionMode, graphReasonText, graphLinkText, type ProblemGraphResponse } from './problemGraph';
import { tf } from './i18n';

describe('graph section helpers', () => {
  const empty: ProblemGraphResponse = { problemId: 'p', shadow: true, entities: 0, members: [], nearMisses: [] };
  it('renders nothing without links and near misses', () => {
    expect(graphSectionMode(undefined)).toBeNull();
    expect(graphSectionMode(empty)).toBeNull();
    expect(graphSectionMode({ ...empty, nearMisses: [{ id: 'x', name: 'cart-api', type: 'service', startedAt: 1, reason: 'N-REL' }] })).toBe('near');
  });
  it('templated reason and rule sentences in both languages', () => {
    const t = (k: string, v?: Record<string, string | number>) => tf(k, 'en', v);
    expect(graphReasonText({ id: 'x', name: 'a', type: 'service', startedAt: 1, reason: 'N-HUB', via: 'orders-db' }, t))
      .toBe('shared point orders-db is used by more than 20 services and has no signal of its own');
    expect(graphLinkText({ id: 'x', entity: 'svc:a', type: 'service', name: 'checkout-grpc', startedAt: 1, link: 'M-WL', peer: 'checkout-api', via: 'checkout' },
      (k, v) => tf(k, 'tr', v))).toBe('checkout-api ile aynı iş yükü: checkout');
  });
});
