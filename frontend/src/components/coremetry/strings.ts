// coremetry/strings — v0.10.1619 (shadcn Faz 1 R10, operatör 2026-10-30).
// shadcn/sonner'ın SABİT İngilizce metinlerinin (Close, No results,
// Search…, Notifications …) TEK Türkçe varsayılanı. Üretilen
// components/shadcn dosyaları olduğu gibi kalır: metin coremetry
// sarmalayıcısından prop/çocuk olarak geçer. Sayfa başına çeviri yok —
// bir sayfa farklı metin isterse sarmalayıcının prop'unu verir.
// strings.pin.test.ts: sarmalayıcıların bu varsayılanları kullandığını ve
// üretilen dosyalardaki İngilizce metnin sarmalayıcıda kapatıldığını çiviler.
export const CM_STRINGS = {
  close: 'Kapat',
  cancel: 'Vazgeç',
  noResults: 'Sonuç yok',
  suggestions: 'Öneriler',
  search: 'Ara…',
  filter: 'Süz',
  filters: 'Filtreler',
  removeFilter: 'Filtreyi kaldır',
  window: 'Pencere',
  rowActions: 'Satır eylemleri',
  commandPaletteTitle: 'Komut paleti',
  commandPaletteDescription: 'Çalıştırılacak komutu arayın…',
  selectPlaceholder: 'Seçin…',
  notifications: 'Bildirimler',
  closeToast: 'Bildirimi kapat',
  detailSheet: 'Detay çekmecesi',
  openPage: 'Sayfayı aç ↗',
  sheetWidth: 'Çekmece genişliği',
  sheetResizeHint: 'Sürükle: genişlet · çift tık: varsayılan genişlik',
} as const;

export type CmStringKey = keyof typeof CM_STRINGS;
