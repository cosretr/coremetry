// detailsSummaries — v0.10.2132 (servis sekmeleri sadeleştirme S1,
// docs/frontend/service-tabs-plan.md §3). Details'in KAPALI bölüm satırlarının
// tek cümle özetleri. SAF: yalnız sayfada ZATEN yüklü veriden (bundle
// `info`, metrik kataloğu, Props şeridinin 7 günlük rollout okuması) kurulur;
// veri yoksa sabit açıklama. Yeni istek YOK (ana oturum kararı 2026-11-14).
//
// Gecikme özeti planın "p50 / p99"si yerine "ortalama / p99": sayfada yüklü
// servis özeti (`Service`) p50 taşımıyor, p50 için yeni okuma gerekirdi.
import type { Rollout, Service } from '@/lib/types';
import { agoTR } from '@/lib/utils';

type Kpi = Pick<Service, 'spanCount' | 'errorRate' | 'p99DurationMs' | 'avgDurationMs'> | null | undefined;

export const ENDPOINTS_SUMMARY = "HTTP route'ları: çağrı payı, hata oranı, p50 / p99.";
const PERF_STATIC = 'İstek hızı, hata oranı ve p99 grafikleri.';
const LATENCY_STATIC = 'Span süresi dağılımı (heatmap).';
const METRICS_STATIC = 'Servisin OTLP metrik panelleri.';
const DEPLOYS_STATIC = "Son 7 günün rollout'ları ve sürümleri.";

/** SAF: "12,3" — Türkçe ondalık, gereksiz sıfır atılır. */
function trNum(v: number, digits = 1): string {
  let s = v.toFixed(digits);
  if (s.includes('.')) s = s.replace(/\.?0+$/, '');
  return s.replace('.', ',');
}

/** SAF: ms → "850 ms" / "1,4 sn". */
export function msTR(ms: number): string {
  if (!Number.isFinite(ms)) return '—';
  return ms >= 1000 ? `${trNum(ms / 1000, 1)} sn` : `${Math.round(ms)} ms`;
}

/** SAF: Performance satırı — "Bu pencerede 12,5/sn, %0,4 hata, p99 850 ms." */
export function perfSummary(info: Kpi, windowNs: number): string {
  if (!info || !(windowNs > 0) || !(info.spanCount > 0)) return PERF_STATIC;
  const rate = info.spanCount / (windowNs / 1e9);
  return `Bu pencerede ${trNum(rate, rate >= 100 ? 0 : 1)}/sn, %${trNum(info.errorRate, 1) || '0'} hata, p99 ${msTR(info.p99DurationMs)}.`;
}

/** SAF: Gecikme dağılımı satırı. */
export function latencySummary(info: Kpi): string {
  if (!info || !(info.spanCount > 0)) return LATENCY_STATIC;
  return `Ortalama ${msTR(info.avgDurationMs)}, p99 ${msTR(info.p99DurationMs)}; dağılım grafiği kapalı.`;
}

/** SAF: Metrikler satırı — "3 panel hazır (HTTP istek hızı, …)." */
export function metricsSummary(panels: ReadonlyArray<{ title: string }>): string {
  if (panels.length === 0) return METRICS_STATIC;
  return `${panels.length} panel hazır (${panels.map(p => p.title).join(', ')}).`;
}

/** SAF: Dağıtımlar satırı. rollouts undefined = henüz okunmadı → sabit. */
export function deploysSummary(rollouts: ReadonlyArray<Pick<Rollout, 'timeUnixNs'>> | undefined, nowMs = Date.now()): string {
  if (!rollouts) return DEPLOYS_STATIC;
  if (rollouts.length === 0) return 'Son 7 günde rollout yok.';
  const last = Math.max(...rollouts.map(r => r.timeUnixNs));
  return `Son 7 günde ${rollouts.length} rollout, sonuncusu ${agoTR(last, nowMs)}.`;
}

// ── Aç/kapa hafızası (ana oturum kararı 2026-11-14, plan §7 S1/1) ────────
// Açılan bölüm sonraki ziyarette açık kalır: TEK localStorage anahtarı
// (`svc.details.open`, JSON kimlik dizisi), tarayıcı başına, servisten
// bağımsız. Okuma/yazma çağıranda try/catch'li `lib/storage` üstünden;
// burada yalnız SAF çözüm.

/** Kapanabilir bölümlerin kimlikleri (= başlık satırının id'si). */
export const COLLAPSIBLE_SECTIONS = ['dtl-endpoints', 'dtl-perf', 'dtl-metrics', 'dtl-latency', 'dtl-runtime'] as const;
export type CollapsibleSection = typeof COLLAPSIBLE_SECTIONS[number];

/** SAF: saklı ham değer → açık bölüm kümesi (bozuk / bilinmeyen kimlik yok sayılır). */
export function parseOpenSections(raw: string | null | undefined): Set<CollapsibleSection> {
  const out = new Set<CollapsibleSection>();
  if (!raw) return out;
  try {
    const v: unknown = JSON.parse(raw);
    if (Array.isArray(v)) {
      for (const x of v) if ((COLLAPSIBLE_SECTIONS as readonly string[]).includes(x)) out.add(x as CollapsibleSection);
    }
  } catch { /* bozuk değer = hepsi kapalı */ }
  return out;
}

/** SAF: küme → saklanacak ham değer (sabit sıra). */
export function serializeOpenSections(s: ReadonlySet<CollapsibleSection>): string {
  return JSON.stringify(COLLAPSIBLE_SECTIONS.filter(id => s.has(id)));
}

/** SAF: adres çapası → açılacak bölüm. `#deploys` Dağıtımlar'ın çapası. */
export function sectionForHash(hash: string): CollapsibleSection | null {
  const id = hash.replace(/^#/, '');
  if (id === 'deploys') return 'dtl-runtime';
  return (COLLAPSIBLE_SECTIONS as readonly string[]).includes(id) ? (id as CollapsibleSection) : null;
}
