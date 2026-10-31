// mlReplay.ts — ML replay görünümü saf yardımcıları (v0.10.1753). Kayıt
// (ml_event_snapshots) kendine yeter: seri, taban medyan+MAD, eşikler,
// tetiklenen kovalar, rollout'lar JSON string olarak gelir; burada çözülür.
import type { MLSample, MLSnapshot, RecentDeployEntry } from '@/lib/types';

export interface ReplayPoint { t: number; v: number }

function parseArr<T>(raw: string | undefined | null): T[] {
  if (!raw) return [];
  try {
    const v: unknown = JSON.parse(raw);
    return Array.isArray(v) ? (v as T[]) : [];
  } catch {
    return [];
  }
}

export function parseObj(raw: string | undefined | null): Record<string, unknown> {
  if (!raw) return {};
  try {
    const v: unknown = JSON.parse(raw);
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

/** SAF: sunucu effMAD ile aynı (internal/ml/log_red.go). */
export function effMAD(med: number, mad: number): number {
  return Math.max(mad * 1.4826, Math.max(0.1 * med, 1));
}

export interface ReplayChart {
  times: number[];
  value: (number | null)[];
  upper: (number | null)[];
  lower: (number | null)[];
  regions: { fromSec: number; toSec: number; label: string }[];
  deploys: number[];
  z: number;
}

/** SAF: kayıt → grafik verisi. Bant = medyan ± etkin z × effMAD (alt sınır ≥ 0). */
export function replayChart(s: MLSnapshot): ReplayChart {
  const pts = parseArr<ReplayPoint>(s.series).filter(p => Number.isFinite(p.t) && Number.isFinite(p.v)).sort((a, b) => a.t - b.t);
  const thr = parseObj(s.thresholds);
  const z = typeof thr.effectiveZ === 'number' ? thr.effectiveZ : typeof thr.criticalZ === 'number' ? thr.criticalZ : 4;
  const eff = effMAD(s.baselineMed, s.baselineMad);
  const hi = s.baselineMed + z * eff;
  const lo = Math.max(0, s.baselineMed - z * eff);
  const dwell = parseArr<number>(s.dwell);
  const step = s.bucketSec || 300;
  return {
    times: pts.map(p => p.t),
    value: pts.map(p => p.v),
    upper: pts.map(() => hi),
    lower: pts.map(() => lo),
    regions: dwell.map(t => ({ fromSec: t, toSec: t + step, label: 'tetik' })),
    deploys: parseArr<RecentDeployEntry>(s.deploys).map(d => Math.floor(d.firstSeenNs / 1e9)).filter(Number.isFinite),
    z,
  };
}

export function replaySamples(s: MLSnapshot): MLSample[] {
  return parseArr<MLSample>(s.samples);
}

const DAY_CLASS_TR: Record<string, string> = {
  holiday: 'tatil', half_day: 'yarım gün', sunday: 'pazar', saturday: 'cumartesi', weekday: 'iş günü', month_end: 'ay sonu benzeri',
};
/** SAF: takvim sınıfı etiketi (bilinmeyen = ham). */
export function dayClassLabel(c: string): string {
  return DAY_CLASS_TR[c] ?? (c || '—');
}

/** SAF: what-if penceresi — tespitten 24 sa önce → 2 sa sonra (şimdiyi aşmaz). unix sn. */
export function whatIfWindow(detectedAtNs: number, nowMs = Date.now()): { from: number; to: number } {
  const det = Math.floor(detectedAtNs / 1e9);
  return { from: det - 24 * 3600, to: Math.min(det + 2 * 3600, Math.floor(nowMs / 1000)) };
}

export interface ReplayContext {
  exceptions: { fingerprint: string; type: string; count: number }[];
  oracle: { traceId: string; sourceId: string; code: string; operation?: string; t: number }[];
  bubbleUp: { key: string; value: string; selPct: number; basePct: number }[] | null;
  bubbleUnreadable: boolean;
}

/** SAF: T1b bağlam referansları (kanıt JSON'undan; yoksa boş). v0.10.1754 */
export function replayContext(evidence: string): ReplayContext {
  const e = parseObj(evidence);
  const arr = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
  return {
    exceptions: arr(e.exceptions),
    oracle: arr(e.oracle),
    bubbleUp: Array.isArray(e.bubbleUp) ? (e.bubbleUp as ReplayContext['bubbleUp']) : null,
    bubbleUnreadable: typeof e.bubbleUp === 'string',
  };
}

/** SAF: exception grubu pivotu (Inbox ile aynı yol, lib/inboxHref.ts). */
export function exceptionGroupHref(fp: string): string {
  return `/problems?exc=${encodeURIComponent(fp)}`;
}
