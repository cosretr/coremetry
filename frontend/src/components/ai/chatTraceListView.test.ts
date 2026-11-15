// v0.10.2213 (operatör onayı 2026-11-15) — sohbetin tek trace tablosu.
import { describe, it, expect } from 'vitest';
import { opTraceListPayload, shortTraceId, traceTableShape } from './chatTraceListView';
import type { ChatOperationPayload } from '@/lib/types';

const row = (svc: string, op: string) => ({ serviceName: svc, rootName: op });

describe('traceTableShape — sabit kolon gizleme', () => {
  it('bütün satırlarda aynı Servis + İşlem → iki kolon da yok; başlık "İşlem · Servis"', () => {
    expect(traceTableShape([row('svc-a', 'POST /x'), row('svc-a', 'POST /x')]))
      .toEqual({ showService: false, showOp: false, caption: 'POST /x · svc-a' });
  });
  it('servis değişiyor, işlem aynı → yalnız Servis kolonu; başlık işlem', () => {
    expect(traceTableShape([row('svc-a', 'GET /y'), row('svc-b', 'GET /y')]))
      .toEqual({ showService: true, showOp: false, caption: 'GET /y' });
  });
  it('ikisi de değişiyor → iki kolon, başlık yok', () => {
    expect(traceTableShape([row('svc-a', 'a'), row('svc-b', 'b')])).toEqual({ showService: true, showOp: true, caption: '' });
  });
  it('boş ad başlığa yazılmaz', () => {
    expect(traceTableShape([row('svc-a', ''), row('svc-a', '')]).caption).toBe('svc-a');
  });
});

describe('shortTraceId / opTraceListPayload', () => {
  it('kısa kimlik 12 + …', () => {
    expect(shortTraceId('0123456789abcdef0123456789abcdef')).toBe('0123456789ab…');
    expect(shortTraceId('abc')).toBe('abc');
  });
  it('kartın trace\'leri paylaşılan yüke; boşsa null', () => {
    const op = {
      tracesHref: '/traces?x=1',
      traces: [{ traceId: 't1', service: 'svc-a', durationMs: 12, error: true, startTime: 5, rootName: 'POST /x' }],
    } as unknown as ChatOperationPayload;
    expect(opTraceListPayload(op)).toMatchObject({
      deepLink: '/traces?x=1',
      traces: [{ traceId: 't1', serviceName: 'svc-a', rootName: 'POST /x', startTime: 5, durationMs: 12, hasError: true }],
    });
    expect(opTraceListPayload({ ...op, traces: [] })).toBeNull();
  });
});
