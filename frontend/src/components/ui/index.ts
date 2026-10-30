// Barrel export for the design-system primitives. Page / feature
// components should import from `@/components/ui` rather than
// reaching into individual files — keeps the surface stable when
// a primitive's internal file split changes.
//
// Why a barrel here but not elsewhere? The ui/ dir is the closed
// design-system boundary. Other component dirs are loose
// collections that should be imported directly so unused ones
// don't drag dependencies into the bundle.

export { Button } from './Button';
export type { ButtonProps } from './Button';

export { IconButton } from './IconButton';
export type { IconButtonProps } from './IconButton';

export { LinkButton } from './LinkButton';
export type { LinkButtonProps } from './LinkButton';

export { MenuItem } from './Menu';
export type { MenuItemProps } from './Menu';

export { OptionRow } from './OptionRow'; // v0.10.927
export type { OptionRowProps } from './OptionRow';

export { TileButton } from './TileButton'; // v0.10.927
export type { TileButtonProps } from './TileButton';

export { Chip } from './Chip';
export type { ChipProps } from './Chip';

export { DisclosureButton } from './DisclosureButton';
export { TabStrip } from './TabStrip';
export { SegmentedControl } from './SegmentedControl'; // v0.10.914
export type { SegmentedControlProps, SegmentedOption } from './SegmentedControl';
export type { TabItem } from './TabStrip';
export type { DisclosureButtonProps } from './DisclosureButton';

export { Card, CardLink } from './Card'; // CardLink: v0.10.928 (Y1)
export type { CardProps, CardLinkProps } from './Card';

export { StatTile } from './StatTile';
export type { StatTileProps } from './StatTile';

export { Badge } from './Badge';
export type { BadgeProps, Tone } from './Badge';

// v0.10.1425 — HTTP method rozeti; renk eşlemesi (GET mavi … DELETE kırmızı) tek yerde.
export { MethodBadge, methodTone, normalizeMethod } from './MethodBadge';
export type { MethodBadgeProps, MethodTone } from './MethodBadge';

export { Drawer, DrawerSection, DrawerTrendRow } from './Drawer';

export { Modal } from './Modal';
export type { ModalProps } from './Modal';

export { ConfirmProvider, useConfirm } from './ConfirmDialog';
export type { ConfirmOptions } from './ConfirmDialog';

export { SearchField } from './SearchField';
export { EnterHint } from './EnterHint'; // v0.10.1332
export type { EnterHintProps } from './EnterHint';
export type { SearchFieldProps } from './SearchField';

export { ActionRow } from './ActionRow';
export type { ActionRowProps } from './ActionRow';

// v0.10.919 (buton bütünlüğü, Seçenek B) — eş eylem kümesi + ipucu.
export { ButtonGroup } from './ButtonGroup';
export type { ButtonGroupProps } from './ButtonGroup';
export type { ButtonGroupSize } from './buttonGroupContext';

export { Tooltip } from './Tooltip';
export type { TooltipProps } from './Tooltip';

// v0.10.1516 — Sütunlar ▾ (sırala/kaldır/sıfırla); /logs + /traces.
export { ColumnsPicker } from './ColumnsPicker';
export type { ColumnsPickerItem, ColumnsPickerLabels, ColumnsPickerProps } from './ColumnsPicker';

// Tabs — REMOVED v0.9.904. A typed `.tab-strip` wrapper existed here
// from the design-system push but never gained a single consumer: all
// 11 `.tab-strip` surfaces write the plain `<div className="tab-strip">`
// markup directly. Rather than keep an unused primitive alive (and keep
// paying for its a11y semantics in review — see v0.9.900's dangling
// `aria-controls`), the file is gone. The CSS pattern itself IS the
// contract: `.tab-strip` in globals.css stays and is what pages target.

export { Field, SelectField, TextareaField } from './Field';
export type { FieldProps, SelectFieldProps, TextareaFieldProps } from './Field';

export { Stack, Row } from './Stack';
export type { StackProps, RowProps } from './Stack';

export { VirtualList } from './VirtualList';
export type { VirtualListProps } from './VirtualList';

// mK7 (v0.9.921) — dört primitif barrel'da EKSİKTİ. Çağrı yerleri
// bugün dosyaya doğrudan uzanıyor, yani "ui/ kapalı tasarım-sistemi
// sınırı" kuralı bu dördü için fiilen uygulanmıyordu. Ekleme SALT
// EKLEME: mevcut doğrudan import'lar taşınmıyor (14 dosyalık bir
// süpürme, kendi kararı), ama yeni çağrı yeri artık barrel'ı bulur.
export { VirtualTable } from './DataTable/VirtualTable';
export { FacetMultiSelect } from './FacetMultiSelect';
export { RouteSkeleton } from './RouteSkeleton';

// PageShell + PageControls — v0.10.1616 (shadcn Faz 1 R8, karar k, §12 Q6)
// components/coremetry/'e TAŞINDI (yerleşim, atom değil); burada yeniden
// dışa aktarım YOK (şim yasak). İçe aktar: @/components/coremetry/PageShell,
// @/components/coremetry/PageControls.

// v0.9.1364 — bölüm-içi yokluk satırı. İki BAYT BAYT aynı yerel nüshanın
// (endpoints/detailSections + slowqueries/StmtDetailDrawer) tek eve
// çekilmişi; `sectionAtomsGate.test.ts` üçüncüsünün doğmasını engelliyor.
export { SectionUnavailable } from './SectionUnavailable';

// v0.9.1365 — kart başlığı + dürüstlük yuvası. İki nüsha BAYT BAYT DEĞİLDİ
// (sayfa kopyası `right` yuvasını kaybetmişti); terfi `right`lı sürüme.
export { PanelTitle } from './PanelTitle';
export { PriorityBadge } from './PriorityBadge';

export { SectionHead } from './SectionHead'; // v0.10.715 — bölüm başlığı atomu
export type { SectionHeadProps } from './SectionHead';

// v0.10.939 (tablo standardı T1) — öznitelik paneli atomu (dl/dt/dd); 21 yer dilim 3'te göçer.
export { KeyValue, KeyValueRow } from './KeyValue';
export type { KeyValueProps, KeyValueRowProps, KeyValueItem, KeyValueLabelWidth } from './KeyValue';

export { Popover, PopoverLinkItem, PopoverHrefItem, type PopoverProps } from './Popover'; // v0.10.968 — çapaya bağlı menü/diyalog (satır ⋯, "+N" çip menüsü, kapsam)
// v0.10.1089 — seçicilerin ortak açılır listesi (Combobox → Service/Operation/MetricName picker).
export { PickerPopover } from './PickerPopover';
export type { PickerPopoverProps, PickerItem, PickerStatus } from './PickerPopover';

// v0.10.1514 — iş kimliği çipi (etiket · değer · ipucu · kopyala); Trace başlığı satır 2.
export { IdentityChip } from './IdentityChip';
export type { IdentityChipProps } from './IdentityChip';
