import type { AnnotationItem } from '@/lib/types';
import type { ChartTimeRegion } from '@/lib/chart/overlays';

// logsMarkers — v0.10.1895 (Log Search Faz 4 dilim 4.1; audit §6 "Histogram +
// işaretler"). /logs histogramının bağlam işaretleri: aynı servisin rollout'u,
// anomalisi ve Problem'i. Kaynak `/api/annotations` (ClickHouse + API; ES
// isteği YOK), sayfa başına tek önbellekli istek, yalnız TEK servis seçiliyken
// (servissiz pencerede tüm kurulumun olayları gürültüdür).
//
// Üç tür AYRI simge + AYRI metin taşır (renk tek başına ayırt etmez):
//   ↻ Rollout (deploy + rollout)   ◈ Anomali   ⚠ Problem (yalnız açılış)
// Problem çözülmesi ve operatör olayları işaret DEĞİL (servis şeridinde zaten
// var; log histogramında "ne değişti" sorusunun üç cevabı yeter).

export type LogsMarkerKind = 'rollout' | 'anomaly' | 'problem';

export const LOGS_MARKER_META: Record<LogsMarkerKind, { glyph: string; label: string; color: string }> = {
  // Renk canvas yolunda resolveVar'dan geçer → çıplak var(--x) (eventRegions emsali).
  rollout: { glyph: '↻', label: 'Rollout', color: 'var(--text2)' },
  anomaly: { glyph: '◈', label: 'Anomali', color: 'var(--warn)' },
  problem: { glyph: '⚠', label: 'Problem', color: 'var(--err)' },
};

export interface LogsMarker {
  key: string;
  kind: LogsMarkerKind;
  tsNs: number;
  /** Simgesiz düz metin ("deploy v1.4.2", "error_rate: …"). */
  text: string;
}

/** SAF: annotation türü → işaret türü; işaret olmayan tür null. */
export function logsMarkerKind(kind: AnnotationItem['kind'] | string): LogsMarkerKind | null {
  switch (kind) {
    case 'deploy':
    case 'rollout': return 'rollout';
    case 'anomaly': return 'anomaly';
    case 'alert_fired': return 'problem';
    default: return null;
  }
}

// Sunucu başlıkları simgeyle başlayabilir ("🔥 P2 · …", "◈ kind: …"); simge
// tür meta'sından gelir, metinde ikinci kez basılmaz.
const LEADING_GLYPH = /^(?:[\s←-⯿\u{1F300}-\u{1FAFF}]|️)+/u;

export const LOGS_MARKERS_MAX = 50;

/** SAF: pencere içindeki işaretler, zamana göre artan; en çok `max` (en yeniler). */
export function logsMarkers(items: AnnotationItem[] | null | undefined, fromNs: number, toNs: number, max = LOGS_MARKERS_MAX): LogsMarker[] {
  if (!items?.length || !(toNs > fromNs)) return [];
  const out: LogsMarker[] = [];
  const seen = new Set<string>();
  for (const it of items) {
    const kind = logsMarkerKind(it.kind);
    if (!kind || !Number.isFinite(it.ts) || it.ts < fromNs || it.ts > toNs) continue;
    const key = `${kind}:${it.targetId ?? ''}:${it.ts}`;
    if (seen.has(key)) continue; // deploy + rollout aynı an/aynı hedef → tek işaret
    seen.add(key);
    out.push({ key, kind, tsNs: it.ts, text: (it.title ?? '').replace(LEADING_GLYPH, '').trim() });
  }
  out.sort((a, b) => a.tsNs - b.tsNs);
  return out.length > max ? out.slice(out.length - max) : out;
}

/** SAF: işaret → grafik bölgesi. Genişlik bir kova (anlık olay şeritte görünsün). */
export function logsMarkerRegions(markers: LogsMarker[], bucketSec: number): ChartTimeRegion[] {
  const w = bucketSec > 0 ? bucketSec : 60;
  return markers.map(m => {
    const meta = LOGS_MARKER_META[m.kind];
    const sec = m.tsNs / 1e9;
    return { fromSec: sec, toSec: sec + w, color: meta.color, label: `${meta.glyph} ${meta.label}`, id: m.key, noChronicLabel: true };
  });
}

/**
 * SAF: işarete tık = pencereyi olayın çevresine daralt. Pay ±15 dk (servis
 * şeridinin emsali), dar pencerede pencerenin 1/8'i (en az 1 dk); sonuç
 * mevcut pencereye kırpılır. Dönüş unix SANİYE (usePageZoomRange sözleşmesi).
 */
export function logsMarkerZoom(tsNs: number, fromNs: number, toNs: number): [number, number] {
  const spanSec = Math.max(0, (toNs - fromNs) / 1e9);
  const pad = Math.max(60, Math.min(900, spanSec / 8));
  const t = tsNs / 1e9;
  const lo = Math.max(fromNs / 1e9, t - pad);
  const hi = Math.min(toNs / 1e9, t + pad);
  return hi > lo ? [lo, hi] : [t - pad, t + pad];
}

/** SAF: düz özet — "2 rollout · 1 anomali · 1 Problem". */
export function logsMarkersSummary(markers: LogsMarker[]): string {
  const n: Record<LogsMarkerKind, number> = { rollout: 0, anomaly: 0, problem: 0 };
  for (const m of markers) n[m.kind]++;
  const parts: string[] = [];
  if (n.rollout) parts.push(`${n.rollout} rollout`);
  if (n.anomaly) parts.push(`${n.anomaly} anomali`);
  if (n.problem) parts.push(`${n.problem} Problem`);
  return parts.join(' · ');
}
