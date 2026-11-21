import type { ClusterPodRow } from '@/lib/types';
import type { InfraClusterRow } from './infraClusters';

// infraSummary — v0.10.2144 (servis sekmeleri sadeleştirme S5,
// docs/frontend/service-tabs-plan.md §3; mockup operatör onayı 2026-11-13).
// Infrastructure'ın üst düz cümlesi ve "Dikkat isteyenler" listesi: dört KPI
// karosunun yerine. Yalnız ZATEN yüklü yanıtlardan (Thanos pod envanteri +
// entity pod'ları); yeni okuma yok. SAF, vitest'li.
//
// Dürüstlük (infraClusters ile aynı): faz bilinmiyorsa "normal" DENMEZ —
// "durumu bilinmiyor"; yanıt vermeyen cluster sayıya girmez, ayrı söylenir;
// restart sayısı bilinmiyorsa sapma uydurulmaz.

export interface InfraSummaryInput {
  /** Thanos'ta pod'u bulunan cluster satırları. */
  clusters: InfraClusterRow[];
  /** Thanos pod envanteri (tüm cluster'lar). */
  pods: ClusterPodRow[];
  /** Sorguya yanıt vermeyen Thanos cluster'ları. */
  unreachable: string[];
  /** Span'lerin gördüğü ama Thanos envanterinde olmayan pod'lar. */
  entityOnly: { pod: string; cluster: string }[];
}

export interface InfraCounts {
  pods: number;
  clusters: number;
  /** Faz biliniyor ve çalışmayan pod yok. */
  normal: number;
  /** En az bir pod çalışmıyor. */
  failing: number;
  /** kube-state görünmüyor: pod fazı bilinmiyor. */
  unknown: number;
  unreachable: number;
}

export function infraCounts(inp: InfraSummaryInput): InfraCounts {
  let normal = 0, failing = 0, unknown = 0;
  for (const c of inp.clusters) {
    if (!c.phaseKnown) unknown++;
    else if (c.failing > 0) failing++;
    else normal++;
  }
  return { pods: inp.pods.length, clusters: inp.clusters.length, normal, failing, unknown, unreachable: inp.unreachable.length };
}

/** Üst cümle. Pod yoksa boş dize (cümle çizilmez; tablo kendi durumunu söyler). */
export function infraSentence(inp: InfraSummaryInput): string {
  const c = infraCounts(inp);
  if (c.pods === 0) return '';
  const head = `${c.pods} pod, ${c.clusters} cluster'da çalışıyor.`;
  if (c.failing === 0 && c.unknown === 0 && c.unreachable === 0) {
    return `${head} ${c.clusters === 1 ? 'Durum normal.' : 'Hepsi normal.'}`;
  }
  const parts: string[] = [];
  if (c.normal > 0) parts.push(`${c.normal} normal`);
  if (c.failing > 0) parts.push(`${c.failing} cluster'da çalışmayan pod var`);
  if (c.unknown > 0) parts.push(`${c.unknown} cluster'ın pod durumu bilinmiyor`);
  if (c.unreachable > 0) parts.push(`${c.unreachable} cluster'dan durum alınamıyor`);
  return `${head} ${parts.join(', ')}.`;
}

export type AttentionKind = 'notRunning' | 'oom' | 'restarts';
export interface AttentionItem {
  kind: AttentionKind;
  tone: 'err' | 'warn';
  /** Satırın öznesi (pod ya da cluster adı). */
  subject: string;
  /** Pod satırlarında cluster; cluster satırında boş. */
  cluster: string;
  text: string;
}

export const ATTENTION_MAX = 8;

/**
 * Yalnız sapan pod'lar, önem sırasıyla: çalışmayan → OOM → restart (çoktan
 * aza). Bir pod en ağır TEK nedeniyle listelenir. Boş dizi = kutu çizilmez.
 * 2026-11-21 (operatör): yanıt vermeyen cluster (üst cümle + tablo satırı
 * zaten söyler) ve "span'lerde var, Thanos'ta yok" pod'u gürültüydü, çıktı.
 */
export function infraAttention(inp: InfraSummaryInput): AttentionItem[] {
  const out: AttentionItem[] = [];
  const notRunning: AttentionItem[] = [], oom: AttentionItem[] = [];
  const restarts: (AttentionItem & { n: number })[] = [];
  for (const p of inp.pods) {
    if (p.phase && p.phase !== 'Running' && p.phase !== 'Succeeded') {
      notRunning.push({ kind: 'notRunning', tone: 'err', subject: p.pod, cluster: p.cluster, text: `çalışmıyor (${p.phase})` });
    } else if (p.lastTermReason === 'OOMKilled') {
      oom.push({ kind: 'oom', tone: 'err', subject: p.pod, cluster: p.cluster, text: 'bellek sınırında sonlandırıldı (OOMKilled)' });
    } else if (!p.restartsUnknown && (p.restarts ?? 0) > 0) {
      restarts.push({ kind: 'restarts', tone: 'warn', subject: p.pod, cluster: p.cluster, text: `${p.restarts} kez yeniden başladı`, n: p.restarts ?? 0 });
    }
  }
  const byName = (a: AttentionItem, b: AttentionItem) => a.cluster.localeCompare(b.cluster) || a.subject.localeCompare(b.subject);
  out.push(...notRunning.sort(byName), ...oom.sort(byName));
  out.push(...restarts.sort((a, b) => b.n - a.n || byName(a, b)).map(({ n: _n, ...it }) => it));
  return out;
}

/** Kaynak cümlesi parçası: "3.2 cores (limitin %41'i)" / "… (limit bilinmiyor)". */
export function usagePhrase(used: string, pct: number | null): { text: string; tone?: 'err' | 'warn' } {
  if (pct == null) return { text: `${used} (limit bilinmiyor)` };
  return { text: `${used} (limitin %${pct}'i)`, tone: pct >= 90 ? 'err' : pct >= 75 ? 'warn' : undefined };
}
