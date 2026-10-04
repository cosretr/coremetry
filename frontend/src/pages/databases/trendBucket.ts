// trendBucket — v0.10.1095 — /database detay grafiklerinin kova etiketi.
//
// Sunucu kaynağı pencereye göre seçiyor (≤ 3 sa → db_summary_1m, aksi
// db_summary_5m; chstore/db_detail_trend.go) ve yük bucketSec'i taşıyor.
// Operatör hangi grenliğe baktığını panel başlığında görür: "Calls / s ·
// 1 dk". Bilinmiyorsa (yükleniyor / eski sunucu) ek YOK — tahmin basılmaz.

/** Kova genişliği → "1 dk" / "5 dk" (dakikaya bölünmüyorsa "N sn"); bilinmiyor → ''. */
export function dbTrendBucketLabel(bucketSec: number | undefined): string {
  if (!bucketSec || bucketSec <= 0) return '';
  return bucketSec % 60 === 0 ? `${bucketSec / 60} dk` : `${bucketSec} sn`;
}

/** Panel başlığı: taban + kova etiketi (varsa). */
export function dbTrendPanelTitle(base: string, bucketSec: number | undefined): string {
  const b = dbTrendBucketLabel(bucketSec);
  return b ? `${base} · ${b}` : base;
}
