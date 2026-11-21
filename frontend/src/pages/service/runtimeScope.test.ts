import { describe, it, expect } from 'vitest';
import { scopedFilters } from './DotnetRuntimePanel';

describe('runtime ortam kapsamı (follow-up 2026-11-21)', () => {
  const env = { k: 'deployment_environment_name', op: 'IN' as const, v: ['clusterB', 'prod-clusterA'] };
  it('kapsam + sinyal süzgeci birlikte; ikisi de yoksa undefined', () => {
    expect(scopedFilters({}, undefined)).toBeUndefined();
    expect(JSON.parse(scopedFilters({ filterKey: 'type', filterValue: 'rss' }, [env])!)).toEqual([env, { k: 'type', op: '=', v: ['rss'] }]);
    expect(JSON.parse(scopedFilters({}, [env])!)).toEqual([env]);
  });
});
