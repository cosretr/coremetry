// paletteOracle — v0.10.1002 (operatör: "operasyon ismiyle trace bulabilir
// miyim" → "yap"). Komut paletinin Oracle OPERASYON ADI sonuçları.
//
// Operasyon adı (TRANSFER_CONFIRM_SERVICE gibi) trace'lerde yok;
// köprü Oracle hata satırlarında (GET /api/oracle/operations). Her isabet en
// çok iki sonuca açılır:
//
//   1. operasyon → TRACE'LER: satırlardaki fonksiyon kodu span'lerde
//      FUNCTION_CODE attribute'u — Traces o kodla süzülür (başarılı + hatalı
//      tüm trace'ler). Fonksiyon kodu yoksa ama öğrenilmiş servis varsa
//      servisin HATALI trace'leri (daha geniş; ipucu bunu söyler). İkisi de
//      yoksa bu sonuç üretilmez — boş listeye giden link verilmez.
//   2. operasyon → SON HATA TRACE'İ: hata satırındaki en yeni trace kimliği
//      (kesin eşleşme), Logs sekmesinde Oracle satırıyla açılır.
//
// SAF: girdi cevap + pencere, çıktı sonuç listesi (paletteOracle.test.ts).

import type { OracleOperationHit, OracleOperationsResponse } from '@/lib/types';
import { functionCodeTracesHref, tracesPivotHref, type TracesPivot } from '@/lib/pivotHref';
import { traceHref } from '@/lib/traceHref';

export interface PaletteOracleResult {
  kind: 'operation' | 'trace';
  label: string;
  hint: string;
  to: string;
}

/** Sorgu Oracle araması için yeterli mi (sunucu < 3 karakteri zaten boş döner). */
export function oracleOperationQuery(rawQuery: string): string | null {
  const q = rawQuery.trim();
  return q.length >= 3 && q.length <= 80 ? q : null;
}

function codesLabel(codes: string[]): string {
  if (codes.length <= 2) return codes.join(', ');
  return `${codes.slice(0, 2).join(', ')} +${codes.length - 2}`;
}

function hitResults(h: OracleOperationHit, attrKey: string, window: TracesPivot['window']): PaletteOracleResult[] {
  const out: PaletteOracleResult[] = [];
  if (h.functionCodes.length > 0) {
    out.push({
      kind: 'operation', label: h.operation,
      hint: `Oracle operasyonu · fonksiyon kodu ${codesLabel(h.functionCodes)} → trace'ler`,
      to: functionCodeTracesHref({ window, attrKey, codes: h.functionCodes }),
    });
  } else if (h.service) {
    out.push({
      kind: 'operation', label: h.operation,
      hint: `Oracle operasyonu · fonksiyon kodu yok → ${h.service} hatalı trace'leri`,
      to: tracesPivotHref({ window, service: h.service, hasError: true, view: 'list', rootOnly: false }),
    });
  }
  if (h.lastTraceId) {
    out.push({
      kind: 'trace', label: h.operation,
      hint: 'Oracle · son hata trace\'i',
      to: traceHref(h.lastTraceId, { tab: 'logs' }),
    });
  }
  return out;
}

/** Cevap → palet sonuçları (isabet sırası korunur: en çok satırlı önce). */
export function oraclePaletteResults(resp: OracleOperationsResponse | null | undefined, window: TracesPivot['window']): PaletteOracleResult[] {
  if (!resp?.enabled) return [];
  const attrKey = resp.spanAttrKey || 'FUNCTION_CODE';
  return (resp.operations ?? []).flatMap(h => hitResults(h, attrKey, window));
}
