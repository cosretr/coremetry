// fleetSummary — Clusters liste görünümünün düz cümlesi ve kart metinleri
// (Clusters S4, v0.10.2169; plan docs/frontend/clusters-shadcn-plan.md §0 D1).
// SAF: girdi kart başına summary sorgusunun durumu; model yok, ek sorgu yok.
//
// D1: filo alarm şeridi kalktı, yerine tek cümle ("8 cluster: 6 sağlıklı,
// 1'inde kritik alarm var, 1'i yanıt vermiyor."); çubuğun yanında "kullanılan
// / kapasite · %". Durum kuralı kartın eski rozetiyle aynı: yanıt vermiyor >
// kritik alarm > sağlıklı (yalnız uyarı düzeyi alarm kartı sağlıksız yapmaz;
// sayısı Alerts kutusunda görünür).
import { fmtBytes } from '@/lib/utils';
import { trPoss, trPossLoc } from '@/lib/trSuffix';
import { safePct } from './thresholds';
import { t, tf, fmtPctLang, type Lang } from '@/lib/i18n';
import type { ClusterSummary } from '@/lib/types';

export type ClusterState = 'healthy' | 'critical' | 'unreachable' | 'pending';

export function clusterState(
  q: { isError: boolean; isPending: boolean; data?: ClusterSummary } | undefined,
): ClusterState {
  if (!q) return 'pending';
  if (q.isError) return 'unreachable';
  if (q.isPending || !q.data) return 'pending';
  return (q.data.alertsCritical ?? 0) > 0 ? 'critical' : 'healthy';
}

/** Kartlardan filo cümlesi; liste boşsa ''. v0.10.2189 — metin arayüz dilinde
 *  (lib/i18n clusters.fleet.*); TR sayı ekleri {loc} / {poss}. */
export function fleetSentence(states: readonly ClusterState[], lang: Lang = 'tr'): string {
  const total = states.length;
  if (total === 0) return '';
  const n = (s: ClusterState) => states.filter(x => x === s).length;
  const healthy = n('healthy'), critical = n('critical'), down = n('unreachable'), pending = n('pending');
  const head = tf('clusters.fleet.head', lang, { n: total });
  if (total === 1) {
    return head + t(down ? 'clusters.state.unreachable' : pending ? 'clusters.state.loading' : critical ? 'clusters.fleet.oneCritical' : 'clusters.state.healthy', lang) + '.';
  }
  if (healthy === total) return head + t('clusters.fleet.allHealthy', lang);
  if (critical === total) return head + t('clusters.fleet.allCritical', lang);
  if (down === total) return head + t('clusters.fleet.allDown', lang);
  if (pending === total) return `${head}${t('clusters.state.loading', lang)}.`;
  const parts: string[] = [];
  if (healthy) parts.push(tf('clusters.fleet.healthyN', lang, { n: healthy }));
  if (critical) parts.push(tf('clusters.fleet.criticalN', lang, { n: critical, loc: trPossLoc(critical) }));
  if (down) parts.push(tf('clusters.fleet.downN', lang, { n: down, poss: trPoss(down) }));
  if (pending) parts.push(tf('clusters.fleet.pendingN', lang, { n: pending, poss: trPoss(pending) }));
  return `${head}${parts.join(', ')}.`;
}

// Çekirdek sayısı: 10'un altında bir ondalık (2.4), üstünde tam sayı (148).
function fmtCoreCount(v: number): string {
  if (v >= 10) return String(Math.round(v));
  return String(Math.round(v * 10) / 10);
}
// v0.10.2186 — yüzde biçimi dile göre: TR "%46", EN "46%".
const pctText = (used: number, cap: number, lang: Lang) => fmtPctLang(Math.round(safePct(used, cap) ?? 0), lang);

/** TR "148 / 320 çekirdek · %46", EN "148 / 320 cores · 46%"; kapasite yoksa
 *  "148 çekirdek"; kullanılan yoksa null. Birim i18n kataloğundan (clusters.cores). */
export function cpuUsageText(usedCores?: number, capacityCores?: number, lang: Lang = 'tr'): string | null {
  if (usedCores == null) return null;
  const unit = t('clusters.cores', lang);
  if (!capacityCores || capacityCores <= 0) return `${fmtCoreCount(usedCores)} ${unit}`;
  return `${fmtCoreCount(usedCores)} / ${fmtCoreCount(capacityCores)} ${unit} · ${pctText(usedCores, capacityCores, lang)}`;
}

/** "922 GiB / 1.28 TiB · %72"; birim aynıysa "118 / 384 GiB · %31"; kapasite yoksa "118 GiB". */
export function memUsageText(usedBytes?: number, capacityBytes?: number, lang: Lang = 'tr'): string | null {
  if (usedBytes == null) return null;
  const used = fmtBytes(usedBytes);
  if (!capacityBytes || capacityBytes <= 0) return used;
  const cap = fmtBytes(capacityBytes);
  const [uVal, uUnit] = used.split(' ');
  const [, cUnit] = cap.split(' ');
  const left = uUnit === cUnit ? uVal : used;
  return `${left} / ${cap} · ${pctText(usedBytes, capacityBytes, lang)}`;
}

/** Metnin tonu: çubuk rengiyle (pctColor) aynı eşikler; normal doluluk nötr. */
export function usageTone(pct: number | null): 'err' | 'warn' | null {
  if (pct == null) return null;
  return pct > 85 ? 'err' : pct > 65 ? 'warn' : null;
}
