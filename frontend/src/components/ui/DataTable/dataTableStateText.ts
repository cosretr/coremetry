// dataTableStateText — v0.10.939 (tablo standardı T12): DataTableState'in
// varsayılan metinleri. Ayrı dosyada, çünkü bileşen dosyasından sabit
// dışa aktarmak react-refresh/only-export-components uyarısı üretiyor
// (lib/pagerPosition.ts emsali). Benimseme süpürmesi (dilim 4) ve testler
// metni buradan okur — "boş", "eşleşme yok" ve "hata" üç AYRI cümle kalır.
export const DATA_TABLE_STATE_TEXT = {
  empty: 'Bu aralıkta veri yok',
  noMatch: 'Eşleşme yok',
  clearFilters: 'Filtreleri temizle',
  loading: 'Yükleniyor',
  error: 'Veri okunamadı — bu bir hata, boş sonuç değil.',
} as const;
