// samplingWeight.ts — v0.10.2430 pure helpers for SamplingWeightPanel.
import type { CountFloorHint, RuleShadowRow, SamplingRule } from '@/lib/types';

/** Rules textarea → rules; string = error text. */
export function parseSamplingRules(text: string): SamplingRule[] | string {
  const s = text.trim();
  if (s === '') return [];
  let v: unknown;
  try { v = JSON.parse(s); } catch (e) { return `Rules: invalid JSON (${(e as Error).message})`; }
  if (!Array.isArray(v)) return 'Rules: expected a JSON array';
  for (const [i, r] of v.entries()) {
    if (typeof r !== 'object' || r === null) return `Rule ${i + 1}: expected an object`;
    const w = (r as { weight?: unknown }).weight;
    if (typeof w !== 'number' || !(w >= 1)) return `Rule ${i + 1}: weight must be a number ≥ 1`;
  }
  return v as SamplingRule[];
}

// Presets for "every failing trace + 5 % of the rest" (tail_sampling
// status_code + probabilistic 5). Replace policy names / percentage / salt
// with the collector's (docs/operator/tail-sampling-weights.md).

/** Exact: the collector records the deciding policy (recordpolicy gate). */
export const POLICY_RULES: SamplingRule[] = [
  { name: 'errors-policy', attr: 'tailsampling.policy', value: 'status-errors', weight: 1 },
  { name: 'probabilistic-policy', attr: 'tailsampling.policy', value: 'probabilistic-5', weight: 20 },
];

/** No collector change: reproduce the probabilistic policy's trace-id hash.
 *  Outside the 5 % bucket ⇒ kept only because it failed ⇒ 1. Inside ⇒ 20
 *  unless the trace's error is visible in the same export request. */
export const HASH_RULES: SamplingRule[] = [
  { name: 'failing-trace', traceError: true, weight: 1 },
  { name: 'error-only', hashPercent: 5, hashBucket: 'out', weight: 1 },
  { name: 'in-bucket', hashPercent: 5, weight: 20 },
];

/** Last resort: span/batch-level status only — OK spans of failing traces
 *  whose error is not in the same request are over-weighted (operator doc). */
export const FALLBACK_RULES: SamplingRule[] = [
  { name: 'failing-trace', traceError: true, weight: 1 },
  { name: 'rest', weight: 20 },
];

/** Tally → rows with share of spans (0–1), largest first (server sorts). */
export function withShares<T extends { spans: number }>(rows: T[]): (T & { share: number })[] {
  const total = rows.reduce((a, r) => a + r.spans, 0);
  return rows.map(r => ({ ...r, share: total > 0 ? r.spans / total : 0 }));
}

/** Represented spans ÷ stored spans (the multiplier the weights imply). */
export function impliedFactor(rows: { weight: number; spans: number }[]): number {
  const n = rows.reduce((a, r) => a + r.spans, 0);
  if (n === 0) return 0;
  return rows.reduce((a, r) => a + r.weight * r.spans, 0) / n;
}

// v0.10.2441 — rule shadow card helpers.

/** Totals across rules: evaluations, fires today / weighted, disagreements. */
export function ruleShadowTotals(rows: RuleShadowRow[]) {
  return rows.reduce(
    (a, r) => ({
      evals: a.evals + r.evals, today: a.today + r.todayFires, weighted: a.weighted + r.weightedFires,
      todayOnly: a.todayOnly + r.todayOnly, weightedOnly: a.weightedOnly + r.weightedOnly,
    }),
    { evals: 0, today: 0, weighted: 0, todayOnly: 0, weightedOnly: 0 },
  );
}

/** Threshold / rate value with enough precision for sub-1 % weighted rates. */
export function fmtShadowValue(v: number): string {
  if (!Number.isFinite(v)) return '—';
  const a = Math.abs(v);
  return a === 0 ? '0' : a < 1 ? v.toFixed(3) : a < 100 ? v.toFixed(2) : v.toFixed(0);
}

// v0.10.2457 — count floors (sample gates) under weighting.

/** Floor equivalent cell: the range that admits the same pairs, or the
 *  "unchanged" note when the reader still counts stored spans. */
export function fmtFloorEquivalent(f: CountFloorHint): string {
  const r = (v: number) => Math.round(v).toLocaleString();
  const range = f.failing === f.healthy ? r(f.healthy) : `${r(f.failing)}–${r(f.healthy)}`;
  return f.weightedRead ? range : `unchanged (stored read); ${range} once weighted`;
}
