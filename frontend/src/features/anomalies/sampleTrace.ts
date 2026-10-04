// sampleTrace.ts — v0.10.1104 (operatör: "bazı traceidler de aslında coremetry
// üzerinde olmayabilir"). Exception örneğinin trace id'si için SAF kararlar;
// bileşenler ExceptionSampleRow.tsx'te (react-refresh: .tsx yalnız bileşen).
import type { MouseEvent } from 'react';
import type { ExceptionSample } from '@/lib/types';

export const TRACE_MISSING_TITLE = "Bu trace Coremetry'de yok — yalnız Oracle hata satırı taşıyor";
export const TRACE_MISSING_LABEL = "Coremetry'de yok";

/** Trace id'si var ve sunucu "Coremetry'de yok" demedi → link basılır.
 *  undefined = bilinmiyor (span grubu ya da sorgu düştü) → link, bugünkü gibi. */
export function sampleTraceLinkable(s: Pick<ExceptionSample, 'traceId' | 'traceInCoremetry'>): boolean {
  return !!s.traceId && s.traceInCoremetry !== false;
}

/** Satır içindeki Link'in tıkı satırın rowActivation'ına çıkmasın (çift gezinme).
 *  preventDefault YOK: Link düz tıkta router'la gider, Ctrl/⌘ tıkta tarayıcı yeni sekme. */
export function stopRowClick(e: MouseEvent): void {
  e.stopPropagation();
}
