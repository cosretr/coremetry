import { describe, expect, it } from 'vitest';
import { TIER_DIM_KEYS, EXEMPLAR_AGGS } from './resolverEligibility';
import {
  TIER_DIM_KEYS as MODEL_DIMS, EXEMPLAR_AGGS as MODEL_AGGS,
} from '@/pages/explore/model';

// GRAN-D (v0.8.249) — the constants extracted from pages/explore/model.ts
// (layering fix: lib must not depend on one page's builder model) re-export
// as the SAME objects, so Explore's exemplarDescriptor behaviour is unchanged.
//
// v0.10.2154 — the serviceRedDescriptors / isResolverEligible contracts that
// used to be pinned here were deleted with their only consumer (ServiceCharts,
// the Service › Details "Performance" section the operator removed).
describe('layering — model.ts re-exports the SAME constants', () => {
  it('TIER_DIM_KEYS is one object, not a copy', () => {
    expect(MODEL_DIMS).toBe(TIER_DIM_KEYS);
  });
  it('EXEMPLAR_AGGS is one object, not a copy', () => {
    expect(MODEL_AGGS).toBe(EXEMPLAR_AGGS);
  });
});
