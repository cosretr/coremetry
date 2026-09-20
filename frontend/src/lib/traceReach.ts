import type { SortColumn, SortOrder } from './types';

// v0.9.645 — trace listesinin sayfalamayla ULAŞABİLDİĞİ tavan.
//
// MV hızlı yolu aşama-1'de topladığı trace kimliklerini aşama-2'ye bir
// IN listesiyle veriyor ve o liste sınırlı: clickhouse-go bind
// argümanlarını istemci tarafında yerleştirdiği için her kimlik ~35
// bayt sorgu metni tutuyor ve sunucunun max_query_size'ı (256 KiB) var
// (v0.8.363'te kod 62 "Syntax error at position 262126" ile bulundu).
//
// Bu yüzden "Son sayfa" düğmesi HER ZAMAN sunulamaz: toplam sayı bilinse
// bile ötesindeki sayfalar SUNULAMIYOR. v0.9.638 tam bu yüzden total'ı
// Pager'dan kesmişti — sayı asla sayfalama sınırı değil.
//
// Sabit backend'den KOPYA ve bu tehlikeli: ayrışırsa Last düğmesi
// sunulamayan bir sayfaya götürür. traceReach.test.ts Go kaynağını
// okuyup iki tarafın eşleştiğini doğruluyor.
export const TRACE_STAGE2_MAX_IDS = 6000;

/**
 * lastReachablePage — "Son sayfa" düğmesi hangi sayfaya götürsün?
 *
 * undefined = düğme ÇİZİLMEZ. Üç durumda:
 *   · sayı yok (sayım reddedildi ya da operatör istemedi)
 *   · sayı TAVANLI ("10.000+") — gerçek son sayfa bilinmiyor
 *   · toplam, sunulabilir tavanın ÖTESİNDE — düğme boşluğa götürürdü
 *
 * SAF — tablo testli.
 */
export function lastReachablePage(
  total: number | undefined,
  atLeast: boolean,
  pageSize: number,
): number | undefined {
  if (total === undefined || atLeast || pageSize <= 0) return undefined;
  if (total <= 0) return undefined;
  if (total > TRACE_STAGE2_MAX_IDS) return undefined;
  return Math.max(0, Math.ceil(total / pageSize) - 1);
}

// ── "Listenin sonu" = ters sıranın ilk sayfası ──────────────────────────────
//
// reverseEndSort — v0.10.827 (operatör-bildirimli, 2026-09-20).
//
// SEMPTOM: /traces'te "Last ⇥"e basınca sayfa göstergesi hâlâ "1" diyor,
// operatör hangi sayfada olduğunu anlayamıyor.
//
// KÖK NEDEN: tık ile GÖSTERGE farklı kaynaklar okuyordu. Tık
// `dt.setSort(...)` yazıyordu; göstergenin etiketi (`pageLabel`, v0.10.727)
// ise sayfanın `order` state'ini okuyor. Aradaki TEK köprü `dt.sort`u sunucu
// sırasına çeviren efektti ve o efektin iki sessiz erken-dönüşü var
// (`if (!id) return;` / `if (!server) return;`). Tık `dt.sort.id ?? 'startTime'`
// yazıyordu — 'startTime' bir KOLON KİMLİĞİ DEĞİL (kolon `time`), SortColumn
// birleşiminde de yok, SERVER_SORTABLE'da da. O dal seçildiğinde sıra terse
// DÖNMÜYOR, `order` DEĞİŞMİYOR, etiket "Page" kalıyor ve gösterge 1'de
// duruyor — v0.10.727'nin düzelttiği yanılgı geri geliyor. Üstelik yanlış
// kimlik `?s_traces-list=startTime.asc` olarak URL'e yazıldığı için hâl
// YAPIŞKAN: sonraki her "Last ⇥" de sessizce yutulur.
//
// Çözüm sınıfı kapatıyor: hedef sıra SAF ve TİP DÜZEYİNDE `SortColumn` —
// sunucunun sıralayabildiği kolonlar dışında bir kimlik ÜRETİLEMEZ (Pager'ın
// `CountDecl` felsefesi: statik tarama tahmin eder, `tsc` ZORLAR). Çağıran
// dönüşü hem tabloya hem `order`a yazıyor, yani göstergenin okuduğu kaynak
// tıkla AYNI turda güncelleniyor — efektin erken-dönüşlerine bağlı değil.
//
// SAF — tablo testli (her SortColumn × her yön).
export function reverseEndSort(
  sort: SortColumn, order: SortOrder,
): { id: SortColumn; dir: SortOrder } {
  return { id: sort, dir: order === 'desc' ? 'asc' : 'desc' };
}
