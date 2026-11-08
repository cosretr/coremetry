// traceSummary — v0.10.1952: tekil trace düz cümle özetinin parçaları (saf).
import { describe, it, expect } from 'vitest';
import { traceSummary, errorMessageOf, HOT_MIN_SHARE } from './traceSummary';
import type { SpanRow } from './types';

let n = 0;
const sp = (p: Partial<SpanRow> & { serviceName: string; durationMs: number }): SpanRow => ({
  traceId: 't', spanId: p.spanId ?? `s${n++}`, parentSpanId: '', name: 'op', kind: 'internal', startTime: 0,
  statusCode: 'unset', statusMessage: '', attributes: {}, events: null, ...p,
} as SpanRow);

describe('traceSummary', () => {
  const root = sp({ spanId: 'r', serviceName: 'api-gateway', durationMs: 2400, kind: 'server', statusCode: 'error', httpStatus: 500 });
  const pay = sp({ spanId: 'p', parentSpanId: 'r', serviceName: 'payments-mobile', durationMs: 1600, kind: 'client', dbSystem: 'oracle', startTime: 10 });
  const card = sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'card-service', durationMs: 300, kind: 'server', statusCode: 'error', startTime: 20 });
  const ins = sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'card-service', durationMs: 250, kind: 'client', statusCode: 'error', startTime: 30,
    events: [{ name: 'exception', timeNano: 1, attributes: { 'exception.message': 'ORA-00001: unique constraint (CARD.PK) violated' } }] });

  it('süre, hatayla bitiş, en uzun dış çağrının payı, kök hata ve kodu', () => {
    const s = traceSummary([root, pay, card, ins])!;
    expect(s.totalMs).toBe(2400);
    expect(s.failed).toBe(true);
    expect(s.httpStatus).toBe(500);
    expect(s.hot).toMatchObject({ service: 'payments-mobile', target: 'oracle', ms: 1600 });
    expect(s.hot!.share).toBeCloseTo(1600 / 2400);
    // kök hata: hatalı çocuğu olmayan (card-service'in sunucu span'i değil, INSERT'i)
    expect(s.firstError).toMatchObject({ spanId: 'i', service: 'card-service', code: 'ORA-00001' });
    expect(s.errorCount).toBe(1);
  });

  it('payı eşiğin altındaki çağrı cümleye girmez; hatasız trace başarılı', () => {
    const r = sp({ spanId: 'r2', serviceName: 'api', durationMs: 1000, kind: 'server' });
    const c = sp({ spanId: 'c2', parentSpanId: 'r2', serviceName: 'api', durationMs: 1000 * HOT_MIN_SHARE - 1, kind: 'client' });
    const s = traceSummary([r, c])!;
    expect(s.hot).toBeUndefined();
    expect(s.failed).toBe(false);
    expect(s.firstError).toBeUndefined();
  });

  it('en büyük tekrar grubu yalnız ≥ 5 ise', () => {
    const r = sp({ spanId: 'r3', serviceName: 'api', durationMs: 100 });
    expect(traceSummary([r], [{ service: 'orders-api', name: 'redis GET', count: 12, totalMs: 40 } as never])!.repeat).toEqual({ service: 'orders-api', name: 'redis GET', count: 12 });
    expect(traceSummary([r], [{ service: 'orders-api', name: 'redis GET', count: 3, totalMs: 4 } as never])!.repeat).toBeUndefined();
    expect(traceSummary([])).toBeNull();
  });

  it('hata mesajı: exception olayı > öznitelik > durum mesajı', () => {
    expect(errorMessageOf(sp({ serviceName: 'x', durationMs: 1, statusMessage: 'boom' }))).toBe('boom');
    expect(errorMessageOf(sp({ serviceName: 'x', durationMs: 1, statusMessage: 'boom', attributes: { 'exception.message': 'attr' } }))).toBe('attr');
  });
});
