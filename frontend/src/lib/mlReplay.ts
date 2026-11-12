// mlReplay.ts — ML replay görünümü saf GRAFİK yardımcıları (v0.10.1753). Kayıt
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
  /** v0.10.1846 — kova bandı var: upper/lower = beklenen aralık (q75/q25, kart ve oynatma cümlesiyle AYNI), alarm = eşik çizgisi. */
  banded: boolean;
  alarm: (number | null)[];
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
  const banded = band.size > 0;
  const down = snapDirection(s) === 'down';
  return {
    times: pts.map(p => p.t),
    value: pts.map(p => p.v),
    // bant varsa beklenen aralık (q25–q75) — yoksa bugünkü eşik bandı (medyan ± z·effMAD)
    upper: pts.map(p => { const b = bandAt(p.t); return banded ? (b ? b.hi : null) : hi; }),
    lower: pts.map(p => { const b = bandAt(p.t); return banded ? (b ? b.lo : null) : lo; }),
    alarm: pts.map(p => { const b = bandAt(p.t); if (!banded || !b) return null; const e = z * effMAD(b.med, (b.hi - b.lo) / 1.349 / 1.4826); return down ? Math.max(0, b.med - e) : b.med + e; }),
    banded,
    regions,
    deploys: parseArr<RecentDeployEntry>(s.deploys).map(d => Math.floor(d.firstSeenNs / 1e9)).filter(Number.isFinite),
    z,
  };
}

export function replaySamples(s: MLSnapshot): MLSample[] {
  return parseArr<MLSample>(s.samples);
}

/** SAF: kayıttaki servis yayınları (tam kayıtta dolar; ön kayıtta boş). */
export function replayDeploys(s: MLSnapshot): RecentDeployEntry[] {
  return parseArr<RecentDeployEntry>(s.deploys).filter(d => Number.isFinite(d.firstSeenNs) && d.firstSeenNs > 0);
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

// v0.10.1843 — anlatım (başlık / özet / durum / "ne değişti" / değer çifti)
// SUNUCUDA: ml.BuildExplainCard (internal/ml/explain_card.go). Bu dosyada
// yalnız grafik + işaret yardımcıları kalır; ikinci bir Türkçe cümle kurucu
// eklemek mlSingleSource.pin.test.ts'i kırar.

/** SAF: kayıtta yön yok; tetiklenen kovaların ortalaması medyanın altındaysa "down". */
export function snapDirection(s: MLSnapshot): 'up' | 'down' {
  const dwell = new Set(parseArr<number>(s.dwell));
  const pts = parseArr<ReplayPoint>(s.series).filter(p => dwell.has(p.t));
  if (!pts.length) return 'up';
  const mean = pts.reduce((a, p) => a + p.v, 0) / pts.length;
  return mean < s.baselineMed ? 'down' : 'up';
}

/** SAF (v0.10.2032; operatör: üst üste binen "tetik" etiketleri okunmuyor):
 *  sade grafik için tek bölge — olay aralığı tek hafif bant, tek etiket
 *  "tespit HH:MM". Tetik kovaları ayrı çizilmez (oynatmalı tam görünüm
 *  teknik ayrıntıda). clock: unix sn → "HH:MM". */
export function compactRegions(regions: { fromSec: number; toSec: number; label: string }[], clock: (sec: number) => string): { fromSec: number; toSec: number; label: string }[] {
  const ev = regions.find(r => r.label === 'anomali') ?? regions[0];
  if (!ev) return [];
  return [{ fromSec: ev.fromSec, toSec: ev.toSec, label: `tespit ${clock(ev.fromSec)}` }];
}
