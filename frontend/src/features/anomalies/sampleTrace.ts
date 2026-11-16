// sampleTrace.ts — v0.10.1104 (operatör: "bazı traceidler de aslında coremetry
// üzerinde olmayabilir"). Exception örneğinin trace id'si için SAF kararlar;
// bileşenler ExceptionSampleRow.tsx'te (react-refresh: .tsx yalnız bileşen).
import type { ExceptionSample } from '@/lib/types';
import { t } from '@/lib/i18n';

// v0.10.1105 — stopRowClick diğer listelerde de (Shapes, Explore) gerektiği
// için lib/a11y.ts'e taşındı; buradan yeniden dışa açılır.
export { stopRowClick } from '@/lib/a11y';

// v0.10.1369 — eski metin ("yalnız Oracle hata satırı taşıyor") Tempo araması
// gelince eksik kaldı: rozet artık "CH'de yok VE (tanımlıysa) Tempo'da da yok"
// demek; tam id kopyalanıp başka araçta aranabilir.
// v0.10.2287 — metinler katalogda (exc.trace.*); bileşen arayüz dilinde okur,
// buradaki sabitler TR değerdir (testler pinler).
export const TRACE_MISSING_TITLE = t('exc.trace.missingTitle', 'tr');
// v0.10.1369 — CH'de yok ama Tempo'da bulundu (sunucu traceSource="tempo").
export const TRACE_TEMPO_TITLE = t('exc.trace.tempoTitle', 'tr');

/** Trace id'si var ve sunucu "Coremetry'de yok" demedi → link basılır.
 *  undefined = bilinmiyor (span grubu ya da sorgu düştü) → link, bugünkü gibi.
 *  v0.10.1369 — Tempo'da bulunan id sunucudan traceInCoremetry=true gelir. */
export function sampleTraceLinkable(s: Pick<ExceptionSample, 'traceId' | 'traceInCoremetry'>): boolean {
  return !!s.traceId && s.traceInCoremetry !== false;
}

/** v0.10.1369 — trace sayfası bu id'yi Grafana Tempo'dan açacak (CH örneklemedi). */
export function sampleTraceFromTempo(s: Pick<ExceptionSample, 'traceSource'>): boolean {
  return s.traceSource === 'tempo';
}
