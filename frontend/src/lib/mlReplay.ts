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
  const floor = med < 1 ? 0.005 : 1; // oran serisi (v0.10.1769)
  return Math.max(mad * 1.4826, Math.max(0.1 * med, floor));
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

/** SAF: thresholds.band ([t, q25, q50, q75][]) → kova başı → bant. C2 (v0.10.1771). */
export function parseBand(raw: string | undefined | null): Map<number, { lo: number; med: number; hi: number }> {
  const out = new Map<number, { lo: number; med: number; hi: number }>();
  const thr = parseObj(raw);
  if (Array.isArray(thr.band)) {
    for (const row of thr.band as unknown[]) {
      if (Array.isArray(row) && row.length === 4 && row.every(x => typeof x === 'number')) {
        const [t, q25, q50, q75] = row as number[];
        out.set(t, { lo: q25, med: q50, hi: q75 });
      }
    }
  }
  return out;
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
  // C2: kova başına mevsimsel beklenen bant (varsa); yoksa sabit medyan ± z·effMAD
  const band = parseBand(s.thresholds);
  const bandAt = (t: number) => band.get(t) ?? band.get(t - (t % step));
  const regions = dwell.map(t => ({ fromSec: t, toSec: t + step, label: 'tetik' }));
  regions.unshift({ fromSec: Math.floor(s.detectedAt / 1e9), toSec: Math.max(Math.floor(s.lastSeen / 1e9), Math.floor(s.detectedAt / 1e9) + step), label: 'anomali' });
  return {
    times: pts.map(p => p.t),
    value: pts.map(p => p.v),
    upper: pts.map(p => { const b = bandAt(p.t); return b ? b.med + z * effMAD(b.med, (b.hi - b.lo) / 1.349 / 1.4826) : hi; }),
    lower: pts.map(p => { const b = bandAt(p.t); return b ? Math.max(0, b.med - z * effMAD(b.med, (b.hi - b.lo) / 1.349 / 1.4826)) : lo; }),
    regions,
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

/** SAF: ilk sapma / tespit / tepe / bitiş işaretleri (unix sn). */
export function replayMarkers(s: MLSnapshot): { first: number | null; detect: number; peak: { t: number; v: number } | null; end: number | null } {
  const dwell = parseArr<number>(s.dwell).filter(Number.isFinite).sort((a, b) => a - b);
  const detect = Math.floor(s.detectedAt / 1e9);
  const end = Math.floor(s.lastSeen / 1e9);
  const pts = parseArr<ReplayPoint>(s.series).filter(p => p.t >= Math.min(detect, dwell[0] ?? detect) && p.t <= end);
  const down = snapDirection(s) === 'down';
  let peak: { t: number; v: number } | null = null;
  for (const p of pts) {
    const better = down ? (!peak || p.v < peak.v) : (!peak || p.v > peak.v);
    if (better) peak = { t: p.t, v: p.v };
  }
  return { first: dwell[0] ?? null, detect, peak, end: s.complete ? end : null };
}

// ── Minimalist anlatım (C3) — saf, hem MLEvent hem MLSnapshot besler ──────
export interface MLNarrativeInput {
  detector: string; service: string; part?: string; code?: string; pod?: string; level?: string;
  direction?: string; observed: number; baseline: number; evidence?: string;
}

function routeOf(n: MLNarrativeInput): string {
  if (n.code) return n.code.includes('|') ? n.code.split('|').slice(-1)[0] || n.code : n.code;
  if (n.part) { const p = n.part.split('|'); return p.length > 1 ? p[p.length - 2] : n.part; }
  return '';
}

/** SAF (test): insan diliyle başlık, jargon yok. */
export function mlHeadline(n: MLNarrativeInput): string {
  const svc = n.service, r = routeOf(n), down = n.direction === 'down';
  switch (n.detector) {
    case 'log_volume': return `${svc} ${down ? 'çok daha az' : 'çok daha fazla'} log üretiyor`;
    case 'error_rate': return `${svc} hata logları arttı`;
    case 'service_silent': return `${svc} log üretmeyi kesti`;
    case 'template_spike': return `${svc}: bir log kalıbı patladı`;
    case 'rare_template': return `${svc}: daha önce görülmeyen bir log kalıbı belirdi`;
    case 'rare_error_code': return `${svc}: yeni hata kodu ${r || ''}`.trim();
    case 'pod_outlier': return `${svc}: ${n.pod || 'bir pod'} diğerlerinden fazla hata veriyor`;
    case 'change_point': return `${svc} log seviyesi kalıcı olarak ${down ? 'düştü' : 'yükseldi'}`;
    case 'op_latency': return `${svc} ${r} yavaşladı`;
    case 'op_error_rate': return `${svc} ${r} hata vermeye başladı`;
    case 'throughput_drop': return `${svc} ${r} trafiği düştü`;
    case 'dependency_latency': return `${svc} → ${r} çağrıları yavaşladı`;
    case 'trace_pod_outlier': return `${svc}: ${n.pod || 'bir pod'} diğerlerinden ${n.code === 'p95' ? 'yavaş' : 'hatalı'}`;
    case 'trace_change_point': return `${svc} ${r} gecikmesi kalıcı olarak ${down ? 'düştü' : 'yükseldi'}`;
    default:
      if (n.detector.startsWith('metric_')) return `${svc}: ${r || n.detector.replace('metric_', '')} ${down ? 'düştü' : 'yükseldi'}`;
      return `${svc}: ${n.detector}`;
  }
}

const RATIO = new Set(['op_error_rate', 'op_latency', 'pod_outlier']);
const MS = new Set(['dependency_latency', 'trace_change_point']);
function unitOf(n: MLNarrativeInput): 'ratio' | 'ms' | 'count' | 'raw' {
  if (RATIO.has(n.detector) || (n.detector === 'trace_pod_outlier' && n.code !== 'p95')) return 'ratio';
  if (MS.has(n.detector) || (n.detector === 'trace_pod_outlier' && n.code === 'p95')) return 'ms';
  if (n.detector.startsWith('metric_')) return 'raw';
  return 'count';
}
function fmtUnit(v: number, u: ReturnType<typeof unitOf>, bucketMin: number): string {
  if (u === 'ratio') return `%${(v * 100).toFixed(v * 100 < 10 ? 1 : 0)}`;
  if (u === 'ms') return v >= 1000 ? `${(v / 1000).toFixed(1)} sn` : `${Math.round(v)} ms`;
  if (u === 'raw') return Number.isInteger(v) ? String(v) : v.toFixed(2);
  return `${Math.round(v)} / ${bucketMin} dk`;
}

/** SAF (test): tek satır "normal → şimdi" birimli + etkilenen; "normalin N×" YOK. */
export function mlSummary(n: MLNarrativeInput, bucketSec = 300): string {
  const u = unitOf(n), m = Math.round(bucketSec / 60);
  let s = `Normalde ${fmtUnit(n.baseline, u, m)}, şimdi ${fmtUnit(n.observed, u, m)}`;
  const ev = parseObj(n.evidence);
  if (typeof ev.affected === 'number') s += ` · ${Math.round(ev.affected)} istek etkilendi`;
  else if (u === 'count' && n.observed > n.baseline) s += ` · ${Math.round(n.observed - n.baseline)} fazla satır`;
  if (typeof ev.pods === 'number' && ev.pods > 0) s += ` · ${ev.pods} pod'dan biri`;
  return s;
}

/** SAF (test): durum + süre satırı. */
export function mlStatusLine(stage: string, startedNs: number, lastNs: number, nowMs = Date.now()): string {
  const open = stage !== 'closed';
  const endNs = open ? nowMs * 1e6 : lastNs;
  const m = Math.max(1, Math.round((endNs - startedNs) / 60e9));
  const dur = m < 60 ? `${m} dk` : `${Math.floor(m / 60)} sa ${m % 60} dk`;
  if (stage === 'chronic') return `Kronik desen · ${dur}`;
  if (stage === 'promoted') return `Problem'e taşındı · ${dur}`;
  return open ? `Sürüyor · ${dur}` : `Bitti · ${dur} sürdü`;
}

/** SAF (test): "Ne değişti" ≤3 kanıtlı madde (rollout, teyit, exception, bubbleUp, kanal). */
export function mlWhatChanged(evidence: string, confirmIds: string[], related: { signal: string; detector: string }[]): string[] {
  const ev = parseObj(evidence);
  const out: string[] = [];
  const rollouts = Array.isArray(ev.rollouts) ? (ev.rollouts as { service?: string; version?: string }[]) : [];
  if (rollouts.length) out.push(`±15 dk içinde rollout: ${rollouts.map(r => r.version || '?').slice(0, 2).join(', ')}`);
  const sigs = new Set(related.map(r => r.signal));
  if (confirmIds.length || related.length) out.push(`Aynı serviste ${confirmIds.length || related.length} başka sinyal${sigs.size ? ` (${[...sigs].join(', ')})` : ''}`);
  const exs = Array.isArray(ev.exceptions) ? (ev.exceptions as { type?: string; count?: number }[]) : [];
  if (exs.length && exs[0].type) out.push(`En sık exception: ${exs[0].type}${exs[0].count ? ` (${exs[0].count})` : ''}`);
  const bu = Array.isArray(ev.bubbleUp) ? (ev.bubbleUp as { key: string; value: string; selPct: number }[]) : [];
  if (bu.length) out.push(`Yoğunlaşma: ${bu[0].key}=${bu[0].value} (%${Math.round(bu[0].selPct)})`);
  const inf = Array.isArray(ev.influencers) ? (ev.influencers as { Value?: string; value?: string }[]) : [];
  if (inf.length) out.push(`Kanal: ${inf[0].Value ?? inf[0].value ?? '?'}`);
  return out.slice(0, 3);
}

/** SAF: kayıtta yön yok; tetiklenen kovaların ortalaması medyanın altındaysa "down". */
export function snapDirection(s: MLSnapshot): 'up' | 'down' {
  const dwell = new Set(parseArr<number>(s.dwell));
  const pts = parseArr<ReplayPoint>(s.series).filter(p => dwell.has(p.t));
  if (!pts.length) return 'up';
  const mean = pts.reduce((a, p) => a + p.v, 0) / pts.length;
  return mean < s.baselineMed ? 'down' : 'up';
}

/** SAF: replay kaydı → anlatım girdisi (code/pod seri parçasından türetilir). */
export function snapNarrative(s: MLSnapshot): MLNarrativeInput {
  const pts = parseArr<ReplayPoint>(s.series);
  const dwell = new Set(parseArr<number>(s.dwell));
  const hit = pts.filter(p => dwell.has(p.t));
  const observed = hit.length ? hit[hit.length - 1].v : (pts.length ? pts[pts.length - 1].v : 0);
  const seg = s.part.split('|');
  const code = s.signal === 'trace' && seg.length >= 2 ? seg.slice(0, -1).join('|').replace(/^dep\|[a-z]+\|/, '') : (s.signal === 'log' && s.part.startsWith('tpl|') ? s.part.slice(4) : s.part.startsWith('code|') ? s.part.slice(5) : '');
  return { detector: s.detector, service: s.service, part: s.part, code, direction: snapDirection(s), observed, baseline: s.baselineMed, evidence: s.evidence };
}
