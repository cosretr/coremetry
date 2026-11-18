import { describe, it, expect } from 'vitest';
import { graphShadowLines, graphCriterionLines, type ProblemGraphShadowReport } from './problemGraph';

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
    expect(l[6]).toBe('Olası köken (farklı küme): kazanan kural yok · belirsiz 0 · güven yok');
    expect(l[7]).toContain('entity_relations 5 (tur başına en çok 3)');
    expect(graphShadowLines({ ...base, originWins: { 'K-DEEP': 1, 'K-SHARED': 2 }, originUnclear: 3, originConf: { weak: 1, strong: 2 } })[6])
      .toBe('Olası köken (farklı küme): kazanan kural K-SHARED 2 · K-DEEP 1 · belirsiz 3 · güven güçlü 2 · zayıf 1');
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

// ── v0.10.2279 (S2) — köken, kanıt, liste alt satırı, yol çizimi ─────────
import {
  originLineText, originRuleText, graphRowSubText, graphDiagramLayout, namesPlus, DIAGRAM_MAX_NODES,
  type ProblemGraphOrigin, type ProblemGraphMember,
} from './problemGraph';

const trT = (k: string, v?: Record<string, string | number>) => tf(k, 'tr', v);
const enT = (k: string, v?: Record<string, string | number>) => tf(k, 'en', v);
const hm = (ns: number) => `t${ns}`;
const strong: ProblemGraphOrigin = {
  entity: 'db:orders-db', name: 'orders-db', type: 'database', confidence: 'strong', rule: 'K-SHARED',
  leads: ['K-SHARED', 'K-DEEP'], onsetAt: 2, earliest: 'orders-db', earliestAt: 2,
  rules: [
    { rule: 'K-ROLLOUT', separated: false, source: 'rollout' },
    { rule: 'K-SHARED', separated: true, leader: 'orders-db', others: ['cart-api', 'checkout-api'], source: 'calls' },
    { rule: 'K-DEEP', separated: true, leader: 'orders-db', source: 'calls' },
    { rule: 'K-FIRST', separated: false, leader: 'orders-db', at: 2, runner: 'cart-api', runAt: 4, source: 'onset' },
  ],
  blast: { affected: ['cart-api', 'checkout-api'], atRisk1: ['invoice-api'], atRisk1N: 1, atRisk2N: 0 },
};

describe('origin sentences', () => {
  it('builds the origin line from rule ids (TR and EN)', () => {
    expect(originLineText(strong, trT, hm)).toBe('orders-db: cart-api, checkout-api için ortak bağımlılık ve çağrı zincirinin en derin halkası; ilk sapma t2.');
    expect(originLineText(strong, enT, hm)).toBe('orders-db: shared dependency of cart-api, checkout-api and deepest link of the call chain; first deviation t2.');
  });
  it('never claims an origin when unclear', () => {
    const u: ProblemGraphOrigin = { confidence: 'unclear', earliest: 'cart-api', earliestAt: 5, rules: [] };
    expect(originLineText(u, trT, hm)).toBe('Köken belirsiz; en erken başlayan cart-api (t5).');
  });
  it('writes evidence rows in rule order, including the tie rule', () => {
    expect(originRuleText(strong.rules[3], trT, hm)).toBe('Ayırmadı. orders-db t2, cart-api t4. Fark 5 dakikanın altında, eşit sayılır.');
    expect(originRuleText(strong.rules[1], enT, hm)).toBe('orders-db leads. cart-api, checkout-api depend on it.');
    expect(originRuleText({ rule: 'K-ROLLOUT', separated: false, off: true, source: 'rollout' }, enT, hm)).toContain('No data');
  });
  it('list sub-line: origin, unclear, nothing for a single service', () => {
    expect(graphRowSubText({ origin: 'orders-db', confidence: 'strong', entities: 4, events: 5 }, trT)).toBe('köken orders-db · 4 varlık · 5 olay');
    expect(graphRowSubText({ confidence: 'unclear', earliest: 'inventory-api', entities: 2, events: 3 }, trT))
      .toBe('köken belirsiz · en erken başlayan inventory-api · 2 varlık · 3 olay');
    expect(graphRowSubText(undefined, trT)).toBeNull();
    expect(graphRowSubText({ origin: 'x', confidence: 'weak', entities: 1, events: 2 }, trT)).toBeNull();
  });
  it('names plus remainder', () => {
    expect(namesPlus(['a', 'b'], 5)).toBe('a, b +3');
    expect(namesPlus([], 0)).toBe('');
  });
});

describe('graphDiagramLayout', () => {
  const ms: ProblemGraphMember[] = [
    { id: 'p-db', entity: 'db:orders-db', type: 'database', name: 'orders-db', startedAt: 2, link: 'M-CALL', path: ['cart-api', 'orders-db'] },
    { id: 'p-cart', entity: 'svc:cart-api', type: 'service', name: 'cart-api', startedAt: 4, owner: true },
    { id: 'p-co', entity: 'svc:checkout-api', type: 'service', name: 'checkout-api', startedAt: 5, link: 'M-CALL', path: ['checkout-api', 'orders-db'] },
    { id: 'p-grpc', entity: 'svc:checkout-grpc', type: 'service', name: 'checkout-grpc', startedAt: 7, link: 'M-WL', peer: 'checkout-api', via: 'checkout' },
    { id: 'p-cart2', entity: 'svc:cart-api', type: 'service', name: 'cart-api', startedAt: 9, link: 'M-SVC', peer: 'cart-api' },
  ];
  it('puts the origin right, callers left, calm callers leftmost; one node per entity', () => {
    const l = graphDiagramLayout(ms, strong);
    const at = (n: string) => l.nodes.find(x => x.name === n)!;
    expect(l.nodes.map(n => n.name)).toEqual(['orders-db', 'cart-api', 'checkout-api', 'checkout-grpc', 'invoice-api']);
    expect(at('orders-db').kind).toBe('origin');
    expect(at('invoice-api').kind).toBe('calm');
    expect(at('orders-db').x).toBeGreaterThan(at('cart-api').x);
    expect(at('cart-api').x).toBeGreaterThan(at('invoice-api').x);
    expect(at('checkout-grpc').x).toBe(at('checkout-api').x);
    expect(l.edges.filter(e => e.kind === 'call')).toHaveLength(2);
    expect(l.edges.find(e => e.kind === 'link')?.label).toBe('checkout');
    expect(at('cart-api').order).toBe(2);
  });
  it('caps at 25 nodes', () => {
    const many: ProblemGraphMember[] = Array.from({ length: 30 }, (_, i) => ({ id: `p${i}`, entity: `svc:s${i}`, type: 'service', name: `s${i}`, startedAt: i }));
    const l = graphDiagramLayout(many, undefined);
    expect(l.nodes).toHaveLength(DIAGRAM_MAX_NODES);
    expect(l.truncated).toBe(true);
  });
});

// v0.10.2341 — admin raporunda S4/S5 kipi ve geçiş ölçütü (yalnız Türkçe).

describe('graphCriterionLines', () => {
  it('says both slices are off and states the criterion with the counts', () => {
    const l = graphCriterionLines({ s4: false, s5: false }, {
      windowStart: 0, mergeJoined: 4, mergeWouldHold: 3, mergeHeldP1: 0, frequentWouldHold: 2, frequentHeldP1: 0, late: 1,
    }, '16.11 14:00');
    expect(l[0]).toBe('S4: kapalı / S5: kapalı');
    expect(l[1]).toContain('en az 7 gün gölge');
    expect(l.join('\n')).toContain('açılış bildirimi tutulacak 3; bunlardan sonradan P1\'e çıkan 0 (hedef 0)');
    expect(l.join('\n')).toContain('Sık tekrar (S5): açılış bildirimi tutulacak 2');
  });
  it('reports an enabled slice and works without counters (old blob)', () => {
    expect(graphCriterionLines({ s4: true, s5: false }, undefined, '')).toEqual([
      'S4: açık / S5: kapalı', expect.stringContaining('Açma ölçütü'),
    ]);
  });
});
