// samplingCorrection — v0.10.2435, sampling-weight Phase 2
// (docs/architecture/sampling-weight.md). GET /api/sampling/correction says
// from when span-derived numbers of a surface family are sampling-corrected
// (represented spans, not stored spans). Pure state derivation is separate
// from the badge so it is unit-tested.

export type SamplingSurface = 'services' | 'operations' | 'endpoints' | 'databases' | 'messaging';

export interface SamplingCorrection {
  mode: string;
  since: Partial<Record<SamplingSurface, string>>;
}

export type SamplingCorrectionState =
  | { kind: 'none' }
  | { kind: 'full'; sinceMs: number }
  | { kind: 'partial'; sinceMs: number };

/** Window [fromMs, toMs) vs the switch point: wholly after → full; straddles → partial (older part uncorrected); before / off → none. */
export function samplingCorrectionState(
  c: SamplingCorrection | undefined, surface: SamplingSurface, fromMs: number, toMs: number,
): SamplingCorrectionState {
  if (!c || c.mode !== 'on') return { kind: 'none' };
  const raw = c.since?.[surface];
  const sinceMs = raw ? Date.parse(raw) : NaN;
  if (!Number.isFinite(sinceMs) || sinceMs >= toMs) return { kind: 'none' };
  return sinceMs <= fromMs ? { kind: 'full', sinceMs } : { kind: 'partial', sinceMs };
}
