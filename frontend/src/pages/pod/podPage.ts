// podPage.ts — v0.10.160: /pod sayfasının saf çekirdekleri (A anatomisi;
// mockup scratchpad/pod-detail/option-A.html, brief §3-§4). Sözleşme
// podPage.test.ts'te pinli. Hiçbiri fetch etmez; sayfa bileşenleri bunları
// mevcut uçlara (spanMetricBatch, /api/traces, /api/entity, /api/clusters/pods)
// bağlar — YENİ uç yok.
import type { TracesParams } from '@/lib/api';
import type { EntityRecord, ClusterPodRow } from '@/lib/types';

/** ?spans= — trace listesi süzgeci. errors varsayılan ve URL'de YAZILMAZ. */
export type SpansMode = 'errors' | 'slow' | 'all';

export function parseSpansParam(raw: string | null): SpansMode {
  return raw === 'slow' || raw === 'all' ? raw : 'errors';
}

/** Yerinde yazar (replace:true ile kullanılır); yabancı paramlara dokunmaz. */
export function writeSpansParam(p: URLSearchParams, mode: SpansMode): URLSearchParams {
  if (mode === 'errors') p.delete('spans'); else p.set('spans', mode);
  return p;
}

type Pt = { time: number; value: number };

// Zamana göre eşle (inceleme #3): avg/p95 AYRI bir batch'ten gelir
// (messaging.system != kafka) ve kovalar seyrek — indeksle eşlemek kafka
// taşıyan pod'da yanlış kovaları çiftlerdi.
function byTime(points: Pt[]): Map<number, number> {
  const m = new Map<number, number>();
  for (const p of points) m.set(p.time, p.value);
  return m;
}

// v0.10.2178 (Clusters S10) — windowTotals / bucketStep KPI şeridiyle birlikte
// kalktı (şerit RED serilerinden türüyordu; RED kapalı bölümde).

/** «Yavaş» çipinin eşiği — p95 serisinin rate-ağırlıklı (zamana göre eşlenmiş) ortalaması; rate yoksa düz ortalama. */
export function windowP95(p95: Pt[], rate: Pt[]): number | null {
  if (p95.length === 0) return null;
  const rt = byTime(rate);
  let w = 0, s = 0;
  for (const p of p95) {
    const r = rt.get(p.time);
    const wt = r !== undefined && r > 0 ? r : 0;
    w += wt; s += wt * p.value;
  }
  if (w > 0) return s / w;
  return p95.reduce((a, p) => a + p.value, 0) / p95.length;
}

export const POD_TRACE_PAGE = 50;

export interface PodTraceCtx {
  pod: string;
  from: number;
  to: number;
  /** span tarafı cluster değeri (?cluster= — boşsa anahtar yazılmaz) */
  cluster: string;
  service: string;
  mode: SpansMode;
  /** «Yavaş» eşiği (ms); null = henüz bilinmiyor → yavaş modu SORGU ÜRETMEZ */
  p95Ms: number | null;
}

/**
 * /api/traces parametreleri. ExceptionPodsPanel'in k8s.pod.name süzgeciyle
 * aynı sözleşme (features/anomalies/ExceptionPodsPanel.tsx:65-66). Sayfa 50,
 * count=skip (toplam sayım yok — «daha fazla var» hasMore'dan). Yavaş modu
 * p95 gelmeden null döner: sabit bir ms'e düşmek yanlış kapsam olurdu.
 */
export function podTraceParams(ctx: PodTraceCtx, offset: number): TracesParams | null {
  if (ctx.mode === 'slow' && (ctx.p95Ms === null || !(ctx.p95Ms > 0))) return null;
  const p: TracesParams = {
    filters: JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [ctx.pod] }]),
    from: ctx.from, to: ctx.to,
    limit: POD_TRACE_PAGE, offset, count: 'skip',
  };
  if (ctx.service) p.service = ctx.service;
  if (ctx.cluster) p.cluster = ctx.cluster;
  if (ctx.mode === 'errors') p.hasError = true;
  if (ctx.mode === 'slow') p.minMs = Math.round(ctx.p95Ms as number);
  return p;
}

export interface SiblingRow {
  rec: EntityRecord;
  name: string;
  /** clusterPods (topk 500) listesinde bulundu mu — bulunmadıysa faz/restart/cpu/mem BİLİNMİYOR */
  known: boolean;
  phase: string | null;
  restarts: number | null;
  cpuCores: number | null;
  memBytes: number | null;
  lastTermReason: string | null;
}

/**
 * Entity kardeşleri (sunucu ≤50) × /api/clusters/pods satırları — namespace+ad
 * ile (pod adı cluster-benzersiz değil; liste zaten tek cluster'ın). Listede
 * olmayan pod'da alanlar null: kesik liste (topk 500) «yok»u kanıtlamaz.
 * restartsUnknown → restarts null (0 değil; v0.9.371 sözleşmesi).
 */
export function joinSiblings(siblings: EntityRecord[], pods: ClusterPodRow[] | null | undefined): SiblingRow[] {
  const byKey = new Map<string, ClusterPodRow>();
  for (const r of pods ?? []) byKey.set(`${r.namespace}/${r.pod}`, r);
  return siblings.map(rec => {
    const r = byKey.get(`${rec.namespace ?? ''}/${rec.name}`);
    if (!r) return { rec, name: rec.name, known: false, phase: null, restarts: null, cpuCores: null, memBytes: null, lastTermReason: null };
    return {
      rec, name: rec.name, known: true,
      phase: r.phase ?? null,
      restarts: r.restartsUnknown ? null : (r.restarts ?? null),
      cpuCores: r.cpuCores, memBytes: r.memBytes,
      lastTermReason: r.lastTermReason ?? null,
    };
  });
}
