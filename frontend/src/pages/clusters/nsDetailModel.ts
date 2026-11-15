// nsDetailModel — Clusters namespace detayı görünümünün (D7) saf çekirdeği
// (Clusters S9; plan docs/frontend/clusters-shadcn-plan.md §9, onaylı
// "Namespace" mockup'ı). Girdi zaten çekilen veriler: /pods satırları,
// /deployments satırları, /namespaces satırı ve /namespaces/detail özeti
// (S8). Model yok, ek sorgu yok; alan yoksa parça düşer.
import type { ClusterDeploymentRow, ClusterNamespaceSummary, ClusterPodRow } from '@/lib/types';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { fmtCores, safePct } from './thresholds';

export type Tone = 'err' | 'warn' | null;

// Bekleme sebebi → düz sözcük (§10.2 ile aynı sözlük; "Sürekli çöküyor" mockup).
const WAITING_WORD: Record<string, { word: string; tone: Tone }> = {
  CrashLoopBackOff: { word: 'Sürekli çöküyor', tone: 'err' },
  ImagePullBackOff: { word: 'Bekliyor · imaj çekilemedi', tone: 'err' },
  ErrImagePull: { word: 'Bekliyor · imaj çekilemedi', tone: 'err' },
  InvalidImageName: { word: 'Bekliyor · imaj çekilemedi', tone: 'err' },
  CreateContainerConfigError: { word: 'Bekliyor · yapılandırma eksik', tone: 'err' },
  ContainerCreating: { word: 'Başlatılıyor', tone: null },
  PodInitializing: { word: 'Başlatılıyor', tone: null },
};

const PHASE_WORD: Record<string, { word: string; tone: Tone }> = {
  Running: { word: 'Çalışıyor', tone: null },
  Pending: { word: 'Bekliyor', tone: 'warn' },
  Failed: { word: 'Başarısız', tone: 'err' },
  Succeeded: { word: 'Tamamlandı', tone: null },
  Unknown: { word: 'Bilinmiyor', tone: 'warn' },
};

/** Pod durumu tek ifade: bekleme sebebi fazı ezer. Faz yoksa ''. */
export function podStatusWord(r: Pick<ClusterPodRow, 'phase' | 'unschedulable'>, waiting?: string): { word: string; tone: Tone } {
  if (waiting) return WAITING_WORD[waiting] ?? { word: `Bekliyor · ${waiting}`, tone: 'warn' };
  // v0.10.2182 (Clusters S13) — kube_pod_status_unschedulable (probe): mockup "Bekliyor · yer yok".
  if (r.unschedulable) return { word: 'Bekliyor · yer yok', tone: 'warn' };
  return PHASE_WORD[r.phase ?? ''] ?? { word: r.phase ?? '', tone: null };
}

// Son sonlanma sebebi → düz sözcük (yeniden başlama hücresinin eki).
export function termReasonWord(reason?: string): string {
  if (!reason || reason === 'Completed') return '';
  if (reason === 'OOMKilled') return 'bellek bitti';
  if (reason === 'Error') return 'hata';
  return reason;
}

/** "7 · bellek bitti"; bilinmiyorsa ''. */
export function podRestartText(r: Pick<ClusterPodRow, 'restarts' | 'restartsUnknown' | 'lastTermReason'>): string {
  if (r.restartsUnknown) return '';
  const n = r.restarts ?? 0;
  const why = n > 0 ? termReasonWord(r.lastTermReason) : '';
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

/** İş yükünün servisi: pod'larının çoğunluk servisi (eşit ise alfabetik ilk); yoksa ''. */
export function workloadService(dep: Pick<ClusterDeploymentRow, 'podNames'>, podsByName: ReadonlyMap<string, ClusterPodRow>): string {
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
export function workloadCpuText(used: number, req: number): string {
  return req > 0 ? `${coreCount(used)} / ${coreCount(req)} çekirdek` : `${coreCount(used)} çekirdek`;
}

/** "31 / 38 GiB · %82"; limit yoksa yalnız kullanılan. */
export function workloadMemText(used: number, lim: number): { text: string; pct: number | null } {
  const pct = safePct(used, lim);
  if (!lim || pct == null) return { text: fmtBytes(used), pct: null };
  const u = fmtBytes(used), l = fmtBytes(lim);
  const [uv, uu] = u.split(' '), [, lu] = l.split(' ');
  return { text: `${uu === lu ? uv : u} / ${l} · %${Math.round(pct)}`, pct };
}

export const KIND_WORD: Record<string, string> = {
  Deployment: 'Deployment', StatefulSet: 'StatefulSet', DaemonSet: 'DaemonSet', Job: 'Job', ReplicaSet: 'ReplicaSet',
};

/** İş yükü durumu: Available → "Hazır", Progressing → "Eksik replika", Degraded → "Kullanılamıyor". */
export function workloadStatusWord(status?: string): { word: string; tone: Tone } {
  switch (status) {
    case 'Available': return { word: 'Hazır', tone: null };
    case 'Progressing': return { word: 'Eksik replika', tone: 'warn' };
    case 'Degraded': return { word: 'Kullanılamıyor', tone: 'err' };
    default: return { word: '', tone: null };
  }
}

export interface NsBox { label: string; value: string; sub: string; tone: Tone; pct: number | null }

/** Dört kutu: Pods · CPU · Memory · Yeniden başlama (24 sa). */
export function namespaceBoxes(pods: readonly ClusterPodRow[], summary: ClusterNamespaceSummary | undefined,
  used: { cpu?: number; mem?: number }, truncated: boolean): NsBox[] {
  const out: NsBox[] = [];
  if (pods.length > 0) {
    const running = pods.filter(p => p.phase === 'Running').length;
    const pending = pods.filter(p => p.phase === 'Pending').length;
    const failed = pods.filter(p => p.phase === 'Failed').length;
    out.push({
      label: 'Pods', value: `${truncated ? 'en az ' : ''}${fmtNum(running)} / ${fmtNum(pods.length)} çalışıyor`,
      sub: [pending ? `${fmtNum(pending)} bekliyor` : '', failed ? `${fmtNum(failed)} başarısız` : ''].filter(Boolean).join(' · '),
      tone: failed > 0 ? 'err' : pending > 0 ? 'warn' : null, pct: null,
    });
  }
  const s = summary ?? {};
  if (used.cpu != null) {
    const pct = safePct(used.cpu, s.cpuLimitCores);
    out.push({
      label: 'CPU', value: `${fmtCores(used.cpu)} çekirdek`,
      sub: [s.cpuRequestCores ? `istek ${fmtCores(s.cpuRequestCores)}` : '', s.cpuLimitCores ? `limit ${fmtCores(s.cpuLimitCores)}` : ''].filter(Boolean).join(' · '),
      tone: pct != null && pct > 85 ? 'err' : null, pct,
    });
  }
  if (used.mem != null) {
    const pct = safePct(used.mem, s.memLimitBytes);
    const over = !!s.memRequestBytes && used.mem > s.memRequestBytes;
    out.push({
      label: 'Memory', value: fmtBytes(used.mem),
      sub: [s.memRequestBytes ? `istek ${fmtBytes(s.memRequestBytes)}${over ? ' (üstünde)' : ''}` : '', s.memLimitBytes ? `limit ${fmtBytes(s.memLimitBytes)}` : ''].filter(Boolean).join(' · '),
      tone: pct != null && pct > 85 ? 'err' : over ? 'warn' : null, pct,
    });
  }
  if (s.restarts24h != null) {
    out.push({ label: 'Yeniden başlama (24 sa)', value: fmtNum(s.restarts24h), sub: '', tone: s.restarts24h > 0 ? 'warn' : null, pct: null });
  }
  return out;
}

/** "6 iş yükü, 36/38 pod çalışıyor; 2 pod bellek limitinin %90'ında; son 24 sa 5 yeniden başlama." */
export function namespaceSentence(workloads: number, pods: readonly ClusterPodRow[], summary: ClusterNamespaceSummary | undefined, truncated: boolean): string {
  const head: string[] = [];
  if (workloads > 0) head.push(`${fmtNum(workloads)} iş yükü`);
  if (pods.length > 0) {
    const running = pods.filter(p => p.phase === 'Running').length;
    head.push(`${truncated ? 'en az ' : ''}${fmtNum(running)}/${fmtNum(pods.length)} pod çalışıyor`);
  }
  const parts: string[] = head.length > 0 ? [head.join(', ')] : [];
  const hot = pods.filter(p => (p.memPct ?? 0) >= 90).length;
  if (hot > 0) parts.push(`${fmtNum(hot)} pod bellek limitinin %90'ında`);
  const waiting = Object.keys(summary?.waiting ?? {}).length;
  if (waiting > 0) parts.push(`${fmtNum(waiting)} pod bekliyor`);
  if ((summary?.restarts24h ?? 0) > 0) parts.push(`son 24 sa ${fmtNum(summary!.restarts24h!)} yeniden başlama`);
  if (parts.length === 0) return '';
  const s = parts.join('; ');
  return `${s.charAt(0).toLocaleUpperCase('tr-TR')}${s.slice(1)}.`;
}
