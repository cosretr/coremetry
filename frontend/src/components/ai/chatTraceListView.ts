// chatTraceListView — v0.10.2213 (operatör onayı 2026-11-15, endpoint cevabı):
// sohbetin TEK trace tablosunun saf kararları (ChatTraceList çizer).
//   • Kolonlar: Başlangıç · Trace (kısa kimlik, link) · [Servis] · [İşlem] ·
//     Süre · Durum. Bütün satırlarda AYNI olan Servis / İşlem kolonu çizilmez;
//     ortak değer tablo başlığında bir kez yazılır ("POST /x · svc-a").
//   • Kısa kimlik: ilk 12 hex + "…" (tam kimlik title'da).
//   • Operasyon kartının trace'leri (ChatOperationPayload.traces) aynı yük
//     şekline çevrilir: tablo görünümü tek.
import type { ChatOperationPayload, ChatTraceListPayload, ChatTraceListRow } from '@/lib/types';

export interface TraceTableShape {
  showService: boolean;
  showOp: boolean;
  /** Sabit kolonların ortak değeri ("İşlem · Servis"); hiç yoksa ''. */
  caption: string;
}

/** SAF: hangi kolon çizilir + ortak başlık. Tek satırda da sabit sayılır. */
export function traceTableShape(rows: readonly Pick<ChatTraceListRow, 'serviceName' | 'rootName'>[]): TraceTableShape {
  const same = (k: 'serviceName' | 'rootName') => rows.length > 0 && rows.every(r => r[k] === rows[0][k]);
  const svcSame = same('serviceName');
  const opSame = same('rootName');
  const parts = [opSame ? rows[0].rootName : '', svcSame ? rows[0].serviceName : ''].filter(Boolean);
  return { showService: !svcSame, showOp: !opSame, caption: parts.join(' · ') };
}

/** SAF: kısa trace kimliği. */
export function shortTraceId(id: string): string {
  return id.length > 12 ? `${id.slice(0, 12)}…` : id;
}

/** SAF: operasyon kartının trace'leri → paylaşılan trace_list yükü. Boşsa null. */
export function opTraceListPayload(op: ChatOperationPayload): ChatTraceListPayload | null {
  const tr = op.traces ?? [];
  if (tr.length === 0) return null;
  return {
    query: '', window: { fromNs: 0, toNs: 0 }, deepLink: op.tracesHref, truncated: false,
    traces: tr.map(t => ({
      traceId: t.traceId, startTime: t.startTime ?? 0, serviceName: t.service, rootName: t.rootName ?? '',
      durationMs: t.durationMs, spanCount: 0, hasError: t.error,
    })),
  };
}
