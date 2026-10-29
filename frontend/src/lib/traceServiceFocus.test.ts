// v0.10.1556 — servis lejantından seçilen servisin span odağı.
import { describe, it, expect } from 'vitest';
import { serviceFocus } from './traceServiceFocus';
import type { SpanRow } from './types';

const sp = (spanId: string, serviceName: string, startTime: number) => ({ spanId, serviceName, startTime } as SpanRow);
const spans = [sp('r', 'gw', 10), sp('a2', 'orders', 40), sp('a1', 'orders', 30), sp('b', 'db', 35)];

describe('serviceFocus', () => {
  it('servisin bütün span kimlikleri + en erken başlayanı', () => {
    const f = serviceFocus(spans, 'orders');
    expect([...f.ids].sort()).toEqual(['a1', 'a2']);
    expect(f.first).toBe('a1');
  });
  it('servis yok / bilinmeyen servis / span yok → boş', () => {
    expect(serviceFocus(spans, null)).toEqual({ ids: new Set(), first: null });
    expect(serviceFocus(spans, 'nope').first).toBeNull();
    expect(serviceFocus(null, 'orders').ids.size).toBe(0);
  });
});
