// problemStats.ts — v0.10.774 (Dynatrace paritesi #7): Problems başlığı
// yaşam döngüsü şeridinin saf yardımcıları. Kablolama ProblemStatsStrip.tsx.
import type { ProblemStats } from '@/lib/types';
import { statusColor } from '@/lib/statusColor';

export const PROBLEM_STATS_WINDOWS = ['1h', '6h', '24h', '7d'] as const;
export type ProblemStatsWindow = (typeof PROBLEM_STATS_WINDOWS)[number];
export const PROBLEM_STATS_PARAM = 'sw';

/** URL parametresi → pencere; bilinmeyen → 24h (sunucuyla aynı varsayılan). */
export function statsWindowFromParam(raw: string | null | undefined): ProblemStatsWindow {
  return (PROBLEM_STATS_WINDOWS as readonly string[]).includes(raw ?? '') ? (raw as ProblemStatsWindow) : '24h';
}

/** MTTR metni: örnek yoksa "—"; <1 sa "N dk", <1 g "N.N sa", üstü "N.N g". */
export function fmtMttr(seconds: number, n: number): string {
  if (n <= 0 || !Number.isFinite(seconds)) return '—';
  if (seconds < 60) return `${Math.round(seconds)} sn`;
  if (seconds < 3600) return `${Math.round(seconds / 60)} dk`;
  if (seconds < 86400) return `${(seconds / 3600).toFixed(1)} sa`;
  return `${(seconds / 86400).toFixed(1)} g`;
}

/** Grafik modeli: iki çubuk serisi (açılan sarı, çözülen yeşil), x unix sn. */
export function statsChart(st: Pick<ProblemStats, 'buckets'>): {
  times: number[];
  series: { key: string; label: string; data: number[]; color: string; type: 'bar' }[];
} {
  return {
    times: st.buckets.map(b => b.t),
    series: [
      { key: 'opened', label: 'açılan', data: st.buckets.map(b => b.opened), color: statusColor('warn'), type: 'bar' },
      { key: 'resolved', label: 'çözülen', data: st.buckets.map(b => b.resolved), color: statusColor('ok'), type: 'bar' },
    ],
  };
}

/** Öncelik çipleri sabit sırada; kategori çipleri sayıya göre azalan, sıfırlar gizli. */
export function priorityChips(by: Record<string, number>): { key: string; count: number }[] {
  return ['P1', 'P2', 'P3'].map(k => ({ key: k, count: by[k] ?? 0 }));
}
export function categoryChips(by: Record<string, number>, max = 5): { key: string; count: number }[] {
  return Object.entries(by)
    .filter(([, c]) => c > 0)
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, max)
    .map(([key, count]) => ({ key, count }));
}
