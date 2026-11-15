// nsDetailModel — Clusters namespace detayı görünümünün (D7) saf çekirdeği
// (Clusters S9; plan docs/frontend/clusters-shadcn-plan.md §9, onaylı
// "Namespace" mockup'ı). Girdi zaten çekilen veriler: /pods satırları,
// /deployments satırları, /namespaces satırı ve /namespaces/detail özeti
// (S8). Model yok, ek sorgu yok; alan yoksa parça düşer.
import type { ClusterDeploymentRow, ClusterNamespaceSummary, ClusterPodRow } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { fmtCores, safePct } from './thresholds';
import { t, tf, fmtPctLang, type Lang } from '@/lib/i18n';

export type Tone = 'err' | 'warn' | null;

// Bekleme sebebi → düz sözcük (§10.2 ile aynı sözlük; "Sürekli çöküyor" mockup).
// v0.10.2189 — sözcükler lib/i18n kataloğunda (clusters.pod.*), arayüz dilinde.
const WAITING_WORD: Record<string, { key: string; tone: Tone }> = {
  CrashLoopBackOff: { key: 'clusters.pod.crashLoop', tone: 'err' },
  ImagePullBackOff: { key: 'clusters.pod.imagePull', tone: 'err' },
  ErrImagePull: { key: 'clusters.pod.imagePull', tone: 'err' },
  InvalidImageName: { key: 'clusters.pod.imagePull', tone: 'err' },
  CreateContainerConfigError: { key: 'clusters.pod.configMissing', tone: 'err' },
  ContainerCreating: { key: 'clusters.pod.starting', tone: null },
  PodInitializing: { key: 'clusters.pod.starting', tone: null },
};

const PHASE_WORD: Record<string, { key: string; tone: Tone }> = {
  Running: { key: 'clusters.pod.running', tone: null },
  Pending: { key: 'clusters.pod.pending', tone: 'warn' },
  Failed: { key: 'clusters.pod.failed', tone: 'err' },
  Succeeded: { key: 'clusters.pod.succeeded', tone: null },
  Unknown: { key: 'clusters.pod.unknown', tone: 'warn' },
};

/** Pod durumu tek ifade: bekleme sebebi fazı ezer. Faz yoksa ''. */
export function podStatusWord(r: Pick<ClusterPodRow, 'phase' | 'unschedulable'>, waiting?: string, lang: Lang = 'tr'): { word: string; tone: Tone } {
  if (waiting) {
    const w = WAITING_WORD[waiting];
    return w ? { word: t(w.key, lang), tone: w.tone } : { word: tf('clusters.pod.waiting', lang, { reason: waiting }), tone: 'warn' };
  }
  // v0.10.2182 (Clusters S13) — kube_pod_status_unschedulable (probe): mockup "Bekliyor · yer yok".
  if (r.unschedulable) return { word: t('clusters.pod.unschedulable', lang), tone: 'warn' };
  const p = PHASE_WORD[r.phase ?? ''];
  return p ? { word: t(p.key, lang), tone: p.tone } : { word: r.phase ?? '', tone: null };
}

// Son sonlanma sebebi → düz sözcük (yeniden başlama hücresinin eki).
export function termReasonWord(reason?: string, lang: Lang = 'tr'): string {
  if (!reason || reason === 'Completed') return '';
  if (reason === 'OOMKilled') return t('clusters.pod.oom', lang);
  if (reason === 'Error') return t('clusters.pod.error', lang);
  return reason;
}

/** "7 · bellek bitti"; bilinmiyorsa ''. */
export function podRestartText(r: Pick<ClusterPodRow, 'restarts' | 'restartsUnknown' | 'lastTermReason'>, lang: Lang = 'tr'): string {
  if (r.restartsUnknown) return '';
  const n = r.restarts ?? 0;
  const why = n > 0 ? termReasonWord(r.lastTermReason, lang) : '';
  return why ? `${fmtNum(n)} · ${why}` : fmtNum(n);
}

/** Sorun sırası: 3 çöküş / imaj / başarısız, 2 bekliyor, 1 restart ya da bellek ≥ %90, 0 normal. */
export function podProblemRank(r: ClusterPodRow, waiting?: string): number {
  const s = podStatusWord(r, waiting);
  if (s.tone === 'err') return 3;
  if (s.tone === 'warn') return 2;
  if ((r.restarts ?? 0) > 0 || (r.memPct ?? 0) >= 90) return 1;
  return 0;
}

/** "sorunlular önce, sonra bellek yüzdesi" sıralama değeri. */
export function podProblemSortValue(r: ClusterPodRow, waiting?: string): number {
  return podProblemRank(r, waiting) * 1000 + (r.memPct ?? 0);
}

/** İş yükünün servisi. v0.10.2191 — önce sunucunun entity çözümü (`service`:
 *  pod →runs→ servis kenarları → k8s.deployment.name attr bağı → ad eşitliği);
 *  yoksa pod'larının çoğunluk servisi (eşit ise alfabetik ilk); yoksa ''. */
export function workloadService(dep: Pick<ClusterDeploymentRow, 'podNames' | 'service'>, podsByName: ReadonlyMap<string, ClusterPodRow>): string {
  if (dep.service) return dep.service;
  const count = new Map<string, number>();
  for (const name of dep.podNames ?? []) {
    const svc = podsByName.get(name)?.service;
    if (svc) count.set(svc, (count.get(svc) ?? 0) + 1);
  }
  let best = '';
  for (const [svc, n] of [...count].sort((a, b) => a[0].localeCompare(b[0]))) {
    if (!best || n > (count.get(best) ?? 0)) best = svc;
  }
  return best;
}

/** İş yükünün pod'larının istek / limit toplamı (yalnız envanterdeki pod'lar). */
export function workloadLimits(dep: Pick<ClusterDeploymentRow, 'podNames'>, podsByName: ReadonlyMap<string, ClusterPodRow>) {
  let cpuReq = 0, memLim = 0;
  for (const name of dep.podNames ?? []) {
    const p = podsByName.get(name);
    if (!p) continue;
    cpuReq += p.cpuRequestCores ?? 0;
    memLim += p.memLimitBytes ?? 0;
  }
  return { cpuReq, memLim };
}

const coreCount = (v: number) => (v >= 10 ? String(Math.round(v)) : String(Math.round(v * 10) / 10));

/** "4.2 / 9.5 çekirdek"; istek yoksa "4.2 çekirdek". */
export function workloadCpuText(used: number, req: number, lang: Lang = 'tr'): string {
  const unit = t('clusters.cores', lang);
  return req > 0 ? `${coreCount(used)} / ${coreCount(req)} ${unit}` : `${coreCount(used)} ${unit}`;
}

/** "31 / 38 GiB · %82"; limit yoksa yalnız kullanılan. */
export function workloadMemText(used: number, lim: number, lang: Lang = 'tr'): { text: string; pct: number | null } {
  const pct = safePct(used, lim);
  if (!lim || pct == null) return { text: fmtBytes(used), pct: null };
  const u = fmtBytes(used), l = fmtBytes(lim);
  const [uv, uu] = u.split(' '), [, lu] = l.split(' ');
  return { text: `${uu === lu ? uv : u} / ${l} · ${fmtPctLang(Math.round(pct), lang)}`, pct };
}

export const KIND_WORD: Record<string, string> = {
  Deployment: 'Deployment', StatefulSet: 'StatefulSet', DaemonSet: 'DaemonSet', Job: 'Job', ReplicaSet: 'ReplicaSet',
};

/** İş yükü durumu: Available → "Hazır", Progressing → "Eksik replika", Degraded → "Kullanılamıyor". */
export function workloadStatusWord(status?: string, lang: Lang = 'tr'): { word: string; tone: Tone } {
  switch (status) {
    case 'Available': return { word: t('clusters.wl.ready', lang), tone: null };
    case 'Progressing': return { word: t('clusters.wl.progressing', lang), tone: 'warn' };
    case 'Degraded': return { word: t('clusters.wl.degraded', lang), tone: 'err' };
    default: return { word: '', tone: null };
  }
}

export interface NsBox { label: string; value: string; sub: string; tone: Tone; pct: number | null }

/** Dört kutu: Pods · CPU · Memory · Yeniden başlama (24 sa). */
export function namespaceBoxes(pods: readonly ClusterPodRow[], summary: ClusterNamespaceSummary | undefined,
  used: { cpu?: number; mem?: number }, truncated: boolean, lang: Lang = 'tr'): NsBox[] {
  const out: NsBox[] = [];
  const atLeast = truncated ? t('clusters.ns.atLeast', lang) : '';
  if (pods.length > 0) {
    const running = pods.filter(p => p.phase === 'Running').length;
    const pending = pods.filter(p => p.phase === 'Pending').length;
    const failed = pods.filter(p => p.phase === 'Failed').length;
    out.push({
      label: 'Pods', value: tf('clusters.ns.podsRunning', lang, { atLeast, running: fmtNum(running), total: fmtNum(pods.length) }),
      sub: [pending ? tf('clusters.pods.pending', lang, { n: fmtNum(pending) }) : '', failed ? tf('clusters.pods.failed', lang, { n: fmtNum(failed) }) : ''].filter(Boolean).join(' · '),
      tone: failed > 0 ? 'err' : pending > 0 ? 'warn' : null, pct: null,
    });
  }
  const s = summary ?? {};
  if (used.cpu != null) {
    const pct = safePct(used.cpu, s.cpuLimitCores);
    out.push({
      label: 'CPU', value: `${fmtCores(used.cpu)} ${t('clusters.cores', lang)}`,
      sub: [s.cpuRequestCores ? tf('clusters.ns.request', lang, { v: fmtCores(s.cpuRequestCores) }) : '', s.cpuLimitCores ? `limit ${fmtCores(s.cpuLimitCores)}` : ''].filter(Boolean).join(' · '),
      tone: pct != null && pct > 85 ? 'err' : null, pct,
    });
  }
  if (used.mem != null) {
    const pct = safePct(used.mem, s.memLimitBytes);
    const over = !!s.memRequestBytes && used.mem > s.memRequestBytes;
    out.push({
      label: 'Memory', value: fmtBytes(used.mem),
      sub: [s.memRequestBytes ? tf('clusters.ns.request', lang, { v: fmtBytes(s.memRequestBytes) }) + (over ? t('clusters.ns.over', lang) : '') : '', s.memLimitBytes ? `limit ${fmtBytes(s.memLimitBytes)}` : ''].filter(Boolean).join(' · '),
      tone: pct != null && pct > 85 ? 'err' : over ? 'warn' : null, pct,
    });
  }
  if (s.restarts24h != null) {
    out.push({ label: t('clusters.ns.restarts24', lang), value: fmtNum(s.restarts24h), sub: '', tone: s.restarts24h > 0 ? 'warn' : null, pct: null });
  }
  return out;
}

/** "6 iş yükü, 36/38 pod çalışıyor; 2 pod bellek limitinin %90'ında; son 24 sa 5 yeniden başlama." */
export function namespaceSentence(workloads: number, pods: readonly ClusterPodRow[], summary: ClusterNamespaceSummary | undefined, truncated: boolean, lang: Lang = 'tr'): string {
  const head: string[] = [];
  if (workloads > 0) head.push(tf('clusters.ns.workloads', lang, { n: fmtNum(workloads) }));
  if (pods.length > 0) {
    const running = pods.filter(p => p.phase === 'Running').length;
    head.push(tf('clusters.ns.podsRunningShort', lang, { atLeast: truncated ? t('clusters.ns.atLeast', lang) : '', running: fmtNum(running), total: fmtNum(pods.length) }));
  }
  const parts: string[] = head.length > 0 ? [head.join(', ')] : [];
  const hot = pods.filter(p => (p.memPct ?? 0) >= 90).length;
  if (hot > 0) parts.push(tf('clusters.ns.hot', lang, { n: fmtNum(hot) }));
  const waiting = Object.keys(summary?.waiting ?? {}).length;
  if (waiting > 0) parts.push(tf('clusters.ns.waiting', lang, { n: fmtNum(waiting) }));
  if ((summary?.restarts24h ?? 0) > 0) parts.push(tf('clusters.ns.restarts', lang, { n: fmtNum(summary!.restarts24h!) }));
  if (parts.length === 0) return '';
  const s = parts.join('; ');
  return `${s.charAt(0).toLocaleUpperCase(lang === 'tr' ? 'tr-TR' : 'en-US')}${s.slice(1)}.`;
}
