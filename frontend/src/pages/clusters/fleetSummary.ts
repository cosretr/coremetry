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
import { t, type Lang } from '@/lib/i18n';
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

/** Kartlardan filo cümlesi; liste boşsa ''. */
export function fleetSentence(states: readonly ClusterState[]): string {
  const total = states.length;
  if (total === 0) return '';
  const n = (s: ClusterState) => states.filter(x => x === s).length;
  const healthy = n('healthy'), critical = n('critical'), down = n('unreachable'), pending = n('pending');
  const head = `${total} cluster: `;
  if (total === 1) {
    return head + (down ? 'yanıt vermiyor' : pending ? 'yükleniyor' : critical ? 'kritik alarm var' : 'sağlıklı') + '.';
  }
  if (healthy === total) return `${head}hepsi sağlıklı.`;
  if (critical === total) return `${head}hepsinde kritik alarm var.`;
  if (down === total) return `${head}hiçbiri yanıt vermiyor.`;
  if (pending === total) return `${head}yükleniyor.`;
  const parts: string[] = [];
  if (healthy) parts.push(`${healthy} sağlıklı`);
  if (critical) parts.push(`${trPossLoc(critical)} kritik alarm var`);
  if (down) parts.push(`${trPoss(down)} yanıt vermiyor`);
  if (pending) parts.push(`${trPoss(pending)} yükleniyor`);
  return `${head}${parts.join(', ')}.`;
}

// Çekirdek sayısı: 10'un altında bir ondalık (2.4), üstünde tam sayı (148).
function fmtCoreCount(v: number): string {
  if (v >= 10) return String(Math.round(v));
  return String(Math.round(v * 10) / 10);
}
// v0.10.2186 — yüzde biçimi dile göre: TR "%46", EN "46%".
const pctText = (used: number, cap: number, lang: Lang) => {
  const n = Math.round(safePct(used, cap) ?? 0);
  return lang === 'tr' ? `%${n}` : `${n}%`;
};

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
