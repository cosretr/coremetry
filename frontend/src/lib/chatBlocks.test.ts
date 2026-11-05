import { describe, it, expect } from 'vitest';
import { appendChatBlock, chartBlocks, evidenceBlocksByRef, mergeBlockLinks, operationBlocks, operationDeltaTexts, traceListBlocks } from './chatBlocks';
import type { ChatTypedBlock } from './types';

// v0.10.541 — blok biriktirme: id tekil (son kazanır), seq sıralı; chart süzgeci
// şekli doğrular; link blokları href tekil olarak çiplere katılır.
const mk = (id: string, seq: number, type: ChatTypedBlock['type'], payload: unknown): ChatTypedBlock => ({ id, seq, type, final: true, payload });

describe('chatBlocks', () => {
  it('append: sıra seq, aynı id yerine geçer', () => {
    let bs = appendChatBlock(undefined, mk('b2', 2, 'link', { href: '/x' }));
    bs = appendChatBlock(bs, mk('b1', 1, 'chart', { service: 'api', agg: 'p95' }));
    bs = appendChatBlock(bs, mk('b2', 2, 'link', { href: '/y', label: 'Y' }));
    expect(bs.map(b => b.id)).toEqual(['b1', 'b2']);
    expect((bs[1].payload as { href: string }).href).toBe('/y');
  });
  it('chartBlocks yalnız geçerli şekli döner', () => {
    const bs = [mk('b1', 1, 'chart', { service: 'api', agg: 'p95', fromNs: 1, toNs: 2 }), mk('b2', 2, 'chart', { nope: 1 }), mk('b3', 3, 'link', { href: '/x' })];
    expect(chartBlocks(bs)).toEqual([{ service: 'api', agg: 'p95', fromNs: 1, toNs: 2 }]);
    expect(chartBlocks(undefined)).toEqual([]);
  });
  it('mergeBlockLinks: href tekil, etiket yoksa href', () => {
    const bs = [mk('b1', 1, 'link', { href: '/traces?x', label: 'Traces' }), mk('b2', 2, 'link', { href: '/logs' }), mk('b3', 3, 'link', { href: '/traces?x', label: 'dup' })];
    expect(mergeBlockLinks([{ label: 'A', href: '/a' }], bs)).toEqual([
      { label: 'A', href: '/a' }, { label: 'Traces', href: '/traces?x' }, { label: '/logs', href: '/logs' },
    ]);
  });
});

// v0.10.1314 — react-router v6 ters bölü açık yönlendirmesi: ChatBubble http(s)
// olmayan her link href'ini `<Link to>` çizer; "/\evil.com" v6'nın "//" kontrolünü
// geçip window.location.assign ile dışarı açılırdı. Güvensiz href (turn.links ve
// tipli link bloğu) düşer; deepLink/tracesHref güvensiz blok da düşer.
describe('chatBlocks — güvensiz uygulama yolu (v0.10.1314)', () => {
  const BAD = ['/\\evil.com', '//evil.com', '/%5Cevil.com', '/%2F%2Fevil.com', 'javascript:alert(1)', 'x'];
  for (const h of BAD) {
    it(`mergeBlockLinks ${JSON.stringify(h)} düşer`, () => {
      expect(mergeBlockLinks([{ label: 'A', href: h }], [mk('b1', 1, 'link', { href: h, label: 'B' })])).toEqual([]);
    });
  }
  it('http(s) ve güvenli yol kalır', () => {
    expect(mergeBlockLinks([{ label: 'W', href: 'https://wiki.example/x' }], [mk('b1', 1, 'link', { href: '/traces?service=a', label: 'T' })]))
      .toEqual([{ label: 'W', href: 'https://wiki.example/x' }, { label: 'T', href: '/traces?service=a' }]);
  });
  it('trace_list / operasyon bloğu güvensiz deepLink/tracesHref ile düşer', () => {
    const tl = (deepLink: string, evidenceRef?: string) => ({ query: 'q', window: { fromNs: 1, toNs: 2 }, traces: [{ traceId: 't' }], deepLink, truncated: false, evidenceRef });
    expect(traceListBlocks([mk('b1', 1, 'trace_list', tl('/\\evil.com'))])).toEqual([]);
    expect(traceListBlocks([mk('b1', 1, 'trace_list', tl('/traces?x=1'))])).toHaveLength(1);
    expect(evidenceBlocksByRef([mk('b1', 1, 'trace_list', tl('/\\evil.com', 'ev1'))]).size).toBe(0);
    expect(evidenceBlocksByRef([mk('b1', 1, 'trace_list', tl('/traces?x=1', 'ev1'))]).size).toBe(1);
    const op = (tracesHref: string) => ({ operation: 'X', tracesHref, current: { calls: 1, errors: 0, p99Ms: 1, measured: true } });
    expect(operationBlocks([mk('b1', 1, 'operation', op('/\\evil.com'))])).toEqual([]);
    expect(operationBlocks([mk('b1', 1, 'operation', op('/traces?operation=X'))])).toHaveLength(1);
  });
});

// v0.10.1783 (operator-reported: endpoint ✨ açıklaması yanıt süresi artışını
// söylemiyordu) — operasyon kartının deltaText'i cevabın üstünde çizilir.
describe('operationDeltaTexts (v0.10.1783)', () => {
  const op = (deltaText?: unknown, tracesHref = '/traces?operation=X') =>
    ({ operation: 'X', tracesHref, current: { calls: 1, errors: 0, p99Ms: 1, measured: true }, deltaText });
  it('dolu deltaText döner', () => {
    expect(operationDeltaTexts([mk('b1', 1, 'operation', op('Son 1 saatte yanıt süresi p99 420 ms → 1,9 sn (+%352)'))]))
      .toEqual(['Son 1 saatte yanıt süresi p99 420 ms → 1,9 sn (+%352)']);
  });
  it('yok / boş / dize değil → düşer', () => {
    expect(operationDeltaTexts([mk('b1', 1, 'operation', op())])).toEqual([]);
    expect(operationDeltaTexts([mk('b1', 1, 'operation', op('  '))])).toEqual([]);
    expect(operationDeltaTexts([mk('b1', 1, 'operation', op(42))])).toEqual([]);
  });
  it('güvensiz tracesHref ile kart düşerse satır da düşer', () => {
    expect(operationDeltaTexts([mk('b1', 1, 'operation', op('x', '/\\evil.com'))])).toEqual([]);
  });
});
