// fieldSurface.ts — v0.10.2143 (operator-reported: shadcn NativeSelect gri
// sayfada gri). Üretilen shadcn alanları `bg-transparent` (dark şemada
// `dark:bg-input/30`) taşır; v0.10.1423 "alanlar --field-bg" kararını
// çağıran ekler. Tailwind kaynağı OLMAYAN sayfalar (pages/Inbox.tsx …) yardımcı
// sınıf yazamaz (undefinedCssRefs) — bu sabiti kullanır; sınıflar burada
// (kaynak dizin) üretilir. `dark:` şart: yoksa dark şemada input/30 kazanır.
export const FIELD_SURFACE = 'bg-field dark:bg-field';
