// serviceRolloutLabel.ts — v0.10.984 (Rollouts v2 P2.3; docs/rollouts/v2-audit.md
// §2.2 "Other pod-churn consumers"). /api/services/{name}/rollouts satırının
// etiketleri, iki kaynaktan birine göre:
//   • pod-churn (v1, bugünkü) — "↻ N pods replaced", +eklenen/−emekli;
//   • KSM (source === 'ksm', rollouts.source="v2") — iş yükü + durum +
//     güncel/istenen replika. KSM emekli pod SAYMAZ (podsRemoved = 0):
//     pod-churn metnini KSM satırına basmak "0 pods replaced" yalanını
//     söylerdi.
// DeployHistoryPanel ve ServiceCharts işaretleri buradan okur. Saf;
// serviceRolloutLabel.test.ts pinler.
import type { Rollout } from './types';
import { statusLabel } from './rolloutRow';

export function isKsmRollout(r: Pick<Rollout, 'source'>): boolean {
  return r.source === 'ksm';
}

function ksmWorkload(r: Pick<Rollout, 'workloadKind' | 'workload'>): string {
  return `${r.workloadKind || 'Deployment'} ${r.workload || '?'}`;
}

function versionSuffix(r: Pick<Rollout, 'versionBefore' | 'versionAfter'>): string {
  return r.versionAfter ? ` · ${r.versionBefore || '?'}→${r.versionAfter}` : '';
}

/** Grafik işaretinin kısa etiketi. */
export function rolloutMarkerLabel(r: Rollout): string {
  return isKsmRollout(r) ? `↻ ${r.workload || 'rollout'}` : `↻ ${r.podsRemoved}p`;
}

/** Grafik işaretinin açıklaması (hover). */
export function rolloutMarkerDescription(r: Rollout): string {
  if (isKsmRollout(r)) {
    return `rollout · ${ksmWorkload(r)} · ${statusLabel(r.status ?? '')} · ${r.updatedReplicas ?? 0}/${r.specReplicas ?? 0} replika (KSM)` + versionSuffix(r);
  }
  return `rollout · ${r.podsRemoved} pod${r.podsRemoved === 1 ? '' : 's'} replaced (+${r.podsAdded})` + versionSuffix(r);
}

/** DeployHistoryPanel satır başlığı. */
export function rolloutHeadline(r: Rollout): string {
  if (isKsmRollout(r)) return `↻ ${ksmWorkload(r)} · ${statusLabel(r.status ?? '')}`;
  return `↻ ${r.podsRemoved} pod${r.podsRemoved === 1 ? '' : 's'} replaced`;
}

/** DeployHistoryPanel sayaç çipi + ipucu. */
export function rolloutCounts(r: Rollout): { text: string; title: string } {
  if (isKsmRollout(r)) {
    return {
      text: `${r.updatedReplicas ?? 0}/${r.specReplicas ?? 0}`,
      title: `${r.updatedReplicas ?? 0} güncel şablonda · ${r.activePods} hazır · ${r.specReplicas ?? 0} istenen (KSM)`,
    };
  }
  return { text: `+${r.podsAdded}/−${r.podsRemoved}`, title: `${r.podsAdded} new · ${r.podsRemoved} retired · ${r.activePods} now active` };
}

/** CoSRE deploy-impact isteğinin "version" alanı (sürüm yoksa açıklayıcı ad). */
export function rolloutExplainVersion(r: Rollout): string {
  if (r.versionAfter) return r.versionAfter;
  if (isKsmRollout(r)) return `rollout (${ksmWorkload(r)})`;
  return r.kind === 'restart' ? `restart (${r.podsRemoved} pods)` : `rollout (${r.podsRemoved} pods)`;
}

/** v0.10.984 (İnceleme) — DeployHistoryPanel satır anahtarı (React key +
 * Copilot explain durumu). KSM satırının zamanı KSM scrape zamanı: aynı
 * scrape'te nesli değişen iki iş yükü (tek Argo sync'te api + worker) AYNI
 * timeUnixNs'i taşır — yalnız zamanla anahtarlamak React key çakışması ve
 * bir satırın açıklamasının ötekinde görünmesi demekti. Pod-churn anahtarı
 * değişmez (zaman). */
export function rolloutRowKey(r: Pick<Rollout, 'timeUnixNs' | 'source' | 'cluster' | 'namespace' | 'workloadKind' | 'workload'>): string {
  if (!isKsmRollout(r)) return `${r.timeUnixNs}`;
  return [r.timeUnixNs, r.cluster ?? '', r.namespace ?? '', r.workloadKind ?? '', r.workload ?? ''].join('|');
}
