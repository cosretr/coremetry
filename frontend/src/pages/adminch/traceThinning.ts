// v0.10.2440 — trace inceltme gölge kartının saf yardımcıları
// (TraceThinningPanel.tsx). Sunucu tahmini chstore/trace_thinning.go'da.
import type { TraceThinningClass, TraceThinningClassCount, TraceThinningSettings } from '@/lib/types';

export const THIN_CLASSES: ReadonlyArray<{ id: TraceThinningClass; label: string; hint: string }> = [
  { id: 'hash', label: 'deterministik örnek', hint: 'cityHash64(trace_id) % 100 < keepPercent' },
  { id: 'error', label: 'hata', hint: 'en az bir span status=error (örneğe girmeyenler)' },
  { id: 'ref', label: 'referanslı', hint: 'kök neden hipotezi exemplar\'ı, paylaşılan snapshot ya da CoSRE konuşması' },
  { id: 'slow', label: 'yavaş kuyruk', hint: 'kök (servis, operasyon) günlük p99\'u üstü (≥100 örnek)' },
];

/** a / b yüzde (b=0 → 0), tek ondalık. */
export function pct(a: number, b: number): number {
  if (!b || b <= 0) return 0;
  return Math.round((a / b) * 1000) / 10;
}

/** Tutulanların sınıf payları (trace bazında), toplam tutulana oranla. */
export function classShares(
  byClass: Partial<Record<TraceThinningClass, TraceThinningClassCount>>,
  keepTraces: number,
): Array<{ id: TraceThinningClass; label: string; hint: string; traces: number; share: number }> {
  return THIN_CLASSES.map(c => {
    const traces = byClass[c.id]?.traces ?? 0;
    return { ...c, traces, share: pct(traces, keepTraces) };
  });
}

/** Form doğrulaması — sunucu Validate'inin aynası (mode=on ayrılmış). */
export function validateThinning(s: TraceThinningSettings): string | null {
  if (s.mode !== 'off' && s.mode !== 'shadow') return 'Mod off ya da shadow olmalı (on ayrılmış)';
  if (!Number.isInteger(s.keepPercent) || s.keepPercent < 1 || s.keepPercent > 100) return 'keepPercent 1..100 olmalı';
  if (!Number.isInteger(s.fullDays) || s.fullDays < 1 || s.fullDays > 89) return 'fullDays 1..89 olmalı';
  return null;
}
