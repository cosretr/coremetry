// components/coremetry — v0.10.1617 (shadcn Faz 1 R9). Bileşiklerin barrel'ı;
// API'ler DONDURULMUŞ (plan §6) — Faz 2 dilimleri buradan içe aktarır.
// Kabuk dosyaları bu barrel'ı içe AKTARAMAZ (overlay → Radix ilk yüke
// binerdi; eslint kabuk bloğu): kabuk PageShell/PageControls'ü doğrudan
// dosya yolundan alır.
export { PageShell } from './PageShell';
export { PageControls } from './PageControls';
export type { PageControlsProps } from './PageControls';
export { StatusBadge } from './StatusBadge';
export type { StatusBadgeProps } from './StatusBadge';
export * from './overlay';
export * from './DataTable';
export * from './TimeRangePicker';
export * from './FilterBar';
export * from './CommandPalette';
export * from './Field';
export * from './Confirm';
// Faz 2 dilim 1 R4 (G5/G6/G7): ui/ dizininden değişmeden taşındı (R8 emsali).
export { KeyValue, KeyValueRow } from './KeyValue';
export type { KeyValueProps, KeyValueRowProps, KeyValueItem, KeyValueLabelWidth } from './KeyValue';
export { SectionHead } from './SectionHead';
export type { SectionHeadProps } from './SectionHead';
export { ActionRow } from './ActionRow';
export type { ActionRowProps } from './ActionRow';
// Faz 2 dilim 1 R6 (G8): radiogroup anlamlı toggle-group sarmalayıcısı.
export { SegmentedControl } from './SegmentedControl';
export type { SegmentedControlProps, SegmentedOption } from './SegmentedControl';
