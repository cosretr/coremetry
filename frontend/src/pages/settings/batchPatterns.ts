// batchPatterns.ts — v0.10.1039: "Batch servis ad kalıpları" alanının saf
// metin ↔ liste dönüşümü (Settings → Anomaly → Dedektör hassasiyeti).
//
// Operatör (prod): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
//
// SÖZLEŞME (backend chstore.AnomalySensitivityConfig.BatchServicePatterns,
// *[]string): alan YOK = varsayılan ['-batch']; BOŞ liste = kural KAPALI.
// Bu yüzden boş metin `[]` olarak GÖNDERİLİR (alanı atlamak varsayılanı
// geri getirirdi). Normalizasyon (küçük harf, tekrar, <3 karakter, tavan 10)
// sunucuda; ekran kaydın ardından sunucunun döndürdüğü listeyi gösterir.

/** Alan yokken sunucunun uyguladığı liste (chstore.DefaultBatchServicePatterns). */
export const DEFAULT_BATCH_SERVICE_PATTERNS: readonly string[] = ['-batch'];

/** Etkin listeyi alan metnine çevirir. undefined (eski sunucu/alan yok) → varsayılan. */
export function formatBatchPatterns(list: string[] | undefined): string {
  return (list ?? DEFAULT_BATCH_SERVICE_PATTERNS).join(', ');
}

/**
 * Alan metnini PUT gövdesine çevirir: virgül VE boşlukla böl, boşları at.
 * Servis adları ikisini de taşımaz; yalnız virgülle bölmek "-batch -cron"u
 * hiçbir şeyle eşleşmeyen TEK kalıp olarak kaydederdi (inceleme bulgusu).
 * Boş metin → [] (kural kapalı).
 */
export function parseBatchPatterns(text: string): string[] {
  return text.split(/[\s,]+/).filter(s => s !== '');
}

/**
 * Sunucu normalizasyonunun düşürdüğü giriş sayısı (3 karakterden kısa, 10'dan
 * fazla, tekrar). Sunucunun kuralını kopyalamaz — gönderilenle dönenin farkı.
 */
export function droppedPatternCount(sent: string[], saved: string[] | undefined): number {
  return Math.max(0, sent.length - (saved ?? DEFAULT_BATCH_SERVICE_PATTERNS).length);
}
