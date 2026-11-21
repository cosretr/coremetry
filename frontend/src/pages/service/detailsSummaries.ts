// detailsSummaries — v0.10.2132 (servis sekmeleri sadeleştirme S1,
// docs/frontend/service-tabs-plan.md §3). Details'in KAPALI bölüm satırlarının
// tek cümle özetleri. SAF: yalnız sayfada ZATEN yüklü veriden (bundle
// `info`, metrik kataloğu, Props şeridinin 7 günlük rollout okuması) kurulur;
// veri yoksa sabit açıklama. Yeni istek YOK (ana oturum kararı 2026-11-14).
// v0.10.2436 — Latency (heatmap) bölümü kalktı, Uygulama metrikleri açık
// bölüm oldu: ikisinin özeti ve kapanır kimliği de kalktı.
import type { Rollout } from '@/lib/types';
import { agoTR } from '@/lib/utils';

export const ENDPOINTS_SUMMARY = "HTTP route'ları: çağrı payı, hata oranı, p50 / p99.";
const DEPLOYS_STATIC = "Son 7 günün rollout'ları ve sürümleri.";

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

/** Kapanabilir bölümlerin kimlikleri (= başlık satırının id'si).
 *  v0.10.2154 — 'dtl-perf' (Performance) bölümüyle birlikte kalktı; saklı
 *  listede kalan eski kimlik parseOpenSections'ta bilinmeyen diye düşer. */
// CoSRE Faz E — 'dtl-cosre-notes': servisin ekip notları (tembel panel).
// v0.10.2436 — 'dtl-latency' (bölüm kalktı) ve 'dtl-metrics' (açık bölüm) düştü.
export const COLLAPSIBLE_SECTIONS = ['dtl-endpoints', 'dtl-runtime', 'dtl-cosre-notes'] as const;
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
