// pagerPosition — sayfalama şeridinin KONUM metni (v0.10.831).
//
// Neden `components/Pager.tsx`te DEĞİL: o dosya bir bileşen modülü ve her
// saf yardımcı `react-refresh/only-export-components` uyarısı üretiyor.
// Depo uyarı tabanını (238) koruyor, yani yeni saf çekirdek lib/'e iner —
// lib/dataTable.ts ve lib/traceReach.ts ile aynı desen.
//
// KURAL (operatör onayı 2026-09-20, operatör-bildirimli iki kez): ters
// sıralı bir listede sayfa numarası kutusu YANILTIYOR — "1" operatöre
// "listenin başındayım" diye okunuyor. v0.10.727 kutunun yanına etiket
// koymayı denedi, yetmedi. Ters kipte konum SONDAN yazılır.
//
// İleri kipte metin YOK (null) — şerit numara kutusunu çizer.

/**
 * pagePositionLabel — 0 tabanlı `page` → operatörün okuduğu konum.
 *
 * reverse=false → null (numara kutusu çizilir; ileri kip değişmedi)
 * reverse=true  → konum SONDAN: 0 → "Son sayfa", 1 → "Sondan 2.", …
 *
 * Ters kipte liste doğal sıranın TERSİ olduğu için sayfa 0, gerçek listenin
 * SON sayfasıdır. Savunmacı: negatif/kesirli sayfa metni bozmaz.
 *
 * SAF — tablo testli (lib/pagerPosition.test.ts), her iki kip ve kipin
 * döndüğü sınır dahil.
 */
export function pagePositionLabel(page: number, reverse: boolean): string | null {
  if (!reverse) return null;
  const pos = Math.max(0, Math.floor(page)) + 1;
  return pos === 1 ? 'Son sayfa' : `Sondan ${pos}.`;
}
