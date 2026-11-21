import { describe, it, expect } from 'vitest';
import { podScopeFilter, ratioMs } from './RuntimePoolCards';
import { hasDBPool, hasOwnRuntimeMetrics } from './ServiceRuntimeSection';

describe('HikariCP pod kapsamı (follow-up 2026-11-21)', () => {
  it('pod listesi → host_name IN süzgeci; boş → null', () => {
    expect(podScopeFilter([])).toBeNull();
    expect(podScopeFilter(['svc-a-2', '', 'svc-a-1'])).toEqual({ k: 'resource.host.name', op: 'IN', v: ['svc-a-1', 'svc-a-2'] });
  });
  it('sum/count rate → ortalama ms', () => {
    const s = { groupKey: [], points: [{ time: 1, value: 0.5 }, { time: 2, value: 1 }] };
    const c = { groupKey: [], points: [{ time: 1, value: 10 }] };
    expect(ratioMs(s, c)).toEqual([{ time: 1, value: 50 }, { time: 2, value: null }]);
  });
  it('havuz sinyali yalnız podScoped iken; runtime ailesini saymaz', () => {
    const sigs = [{ signal: 'db_pool_pending', podScoped: true }];
    expect(hasDBPool(sigs)).toBe(true);
    expect(hasDBPool([{ signal: 'db_pool_pending' }])).toBe(false);
    expect(hasOwnRuntimeMetrics(sigs)).toBe(false);
  });
});
