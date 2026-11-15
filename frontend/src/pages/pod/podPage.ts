// podPage.ts — v0.10.160: /pod sayfasının saf çekirdekleri (A anatomisi;
// mockup scratchpad/pod-detail/option-A.html, brief §3-§4). Sözleşme
// podPage.test.ts'te pinli. Hiçbiri fetch etmez; sayfa bileşenleri bunları
// mevcut uçlara (spanMetricBatch, /api/traces, /api/entity, /api/clusters/pods)
// bağlar — YENİ uç yok.
import type { TracesParams } from '@/lib/api';
import type { EntityRecord, ClusterPodRow, PodTraceScope } from '@/lib/types';

/** ?spans= — trace listesi süzgeci. errors varsayılan ve URL'de YAZILMAZ. */
export type SpansMode = 'errors' | 'all';

export function parseSpansParam(raw: string | null): SpansMode {
  return raw === 'all' ? 'all' : 'errors';
}

/**
 * v0.10.2195 — URL'de çip yokken: penceredeki hatalı trace sayısı biliniyor
 * ve sıfırsa «Tümü» (boş liste yerine); bilinmiyorsa / varsa «Hatalı».
 */
export function effectiveSpansMode(raw: string | null, errorRowsFirstPage: number | null): SpansMode {
  if (raw != null) return parseSpansParam(raw);
  return errorRowsFirstPage === 0 ? 'all' : 'errors';
}

/** Yerinde yazar (replace:true ile kullanılır); yabancı paramlara dokunmaz. */
export function writeSpansParam(p: URLSearchParams, mode: SpansMode): URLSearchParams {
  if (mode === 'errors') p.delete('spans'); else p.set('spans', mode);
  return p;
}

export const POD_TRACE_PAGE = 50;

export interface PodTraceCtx {
  pod: string;
  from: number;
  to: number;
  /** span tarafı cluster değeri (?cluster= — boşsa anahtar yazılmaz) */
  cluster: string;
  service: string;
  /** v0.10.2195 — false = pod'a ait span yok, servis geneli (k8s.pod.name süzgeci yok) */
  podExact: boolean;
  mode: SpansMode;
}


/**
 * v0.10.2195 — trace kapsamı entity modelinden. Kapsam yüklenmeden / yoksa:
 * kaydın tek span cluster değeri (çoksa süzgeç yok), pod süzgeci. URL'deki
 * ?service= pod kipinde daraltıcı kalır; servis kipinde iş yükünün servisi.
 */
export function traceScopeCtx(scope: PodTraceScope | undefined, spanClusters: string[], urlService: string):
  { cluster: string; service: string; podExact: boolean } {
  const single = spanClusters.length === 1 ? spanClusters[0] : '';
  if (scope?.mode === 'service' && scope.service) {
    return { cluster: scope.clusterValue ?? single, service: scope.service, podExact: false };
  }
  return { cluster: scope?.clusterValue || single, service: urlService, podExact: true };
}

/**
 * /api/traces parametreleri. ExceptionPodsPanel'in k8s.pod.name süzgeciyle
 * aynı sözleşme (features/anomalies/ExceptionPodsPanel.tsx:65-66). Sayfa 50,
 * count=skip (toplam sayım yok — «daha fazla var» hasMore'dan). Servis
 * geneli kipte (podExact=false) k8s.pod.name süzgeci yazılmaz.
 */
export function podTraceParams(ctx: PodTraceCtx, offset: number): TracesParams {
  const p: TracesParams = {
    from: ctx.from, to: ctx.to,
    limit: POD_TRACE_PAGE, offset, count: 'skip',
  };
  if (ctx.podExact) p.filters = JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [ctx.pod] }]);
  if (ctx.service) p.service = ctx.service;
  if (ctx.cluster) p.cluster = ctx.cluster;
  if (ctx.mode === 'errors') p.hasError = true;
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
