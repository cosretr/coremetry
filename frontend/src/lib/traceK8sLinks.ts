// traceK8sLinks — v0.10.143 (DETAY SAYFALARI adım 6: traces listesi).
// Trace listesinin Kubernetes kolon seti. v0.10.330'dan beri hücreler düz
// metin; v0.10.2233 (Entity S3, A9) — FE'deki cluster eşlemesi
// (traceK8sHref) silindi: span → entity yolu tek yerden, POST
// /api/entity/paths (span detayı çipleri). Saf; vitest'li.

// Sıra ÖNEMLİ: `cluster` önce — link için tek zorunlu anahtar; 8 kolon tavanı
// kuyruğu düşürür (inceleme). Değer terfi `cluster` kolonundan (coalesce zinciri).
export const TRACE_K8S_COLS = ['cluster', 'k8s.namespace.name', 'k8s.pod.name', 'k8s.node.name'] as const;
export type TraceK8sCol = typeof TRACE_K8S_COLS[number];

export function isTraceK8sCol(id: string): id is TraceK8sCol {
  return (TRACE_K8S_COLS as readonly string[]).includes(id);
}

/** Kubernetes kolon seti: 8 kolon tavanına uyacak biçimde eklenir (mevcutlar atlanır; cluster önce). */
export function withK8sColumns(extraCols: string[], cap = 8): string[] {
  const out = [...extraCols];
  for (const k of TRACE_K8S_COLS) {
    if (out.length >= cap) break;
    if (!out.includes(k)) out.push(k);
  }
  return out;
}

/** Düğme yalnız gerçekten bir şey ekleyecekse görünür (tavan dolu / hepsi ekli → gizli). */
export function canAddK8sColumns(extraCols: string[], cap = 8): boolean {
  return withK8sColumns(extraCols, cap).length > extraCols.length;
}
