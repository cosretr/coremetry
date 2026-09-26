// ui/DataTable — v0.10.246 DataTable dilim 1 (audit §9, §12 dilim 1).
//
// Tek giriş noktası. v0.10.317 (dilim 7): DataTable.tsx / VirtualTable.tsx
// FİZİKSEL olarak bu klasörde; eski yollar YOK (shim yok — dataTableBarrel
// kapısı eski importu kırmızıya çevirir). Tüm sayfalar buradan içe aktarır.

export { useDataTable, DataTableHead, DataTableColgroup, ColResizeHandle, resolveInitialSort, headKeyDown, HEAD_RESIZE_STEP_PX } from './DataTable';
export type { DataTable, DataTableSelection, DataTableServer, CellProps, RowLinkProps, RowProps } from './DataTable';
// v0.10.939 (tablo standardı dilim 2) — hücre sözleşmesi + ortadan kırpma +
// başlık ⋯ menüsü (S8; ResetLayoutButton'ın yerine, DataTableHead basar).
export { DataTableCell } from './DataTableCell';
export type { DataTableCellProps } from './DataTableCell';
export { MiddleEllipsis } from './MiddleEllipsis';
export { splitMiddle, MIDDLE_ELLIPSIS_TAIL } from './middleSplit';
export type { MiddleEllipsisProps } from './MiddleEllipsis';
export { VirtualTable } from './VirtualTable';
export { ROW_H } from './rowHeight';
export type { VirtualTableProps } from './VirtualTable';
export type { DataTableColumn, SortState, SortDir } from '@/lib/dataTable';
export { columnLayoutSig, visibleColumns, nextSort, parseSortParam, formatSortParam } from '@/lib/dataTable';
export type { ColumnDef, CellTone, CellTruncate, ColumnKind, ColumnModel, ColumnSource, ColumnSpec, ColumnModelBinding, SelectionBinding, ServerBinding } from './types';
export {
  defaultColumnModel, reconcileColumnModel, visibleColumnIds, toggleHidden, moveColumnTo,
  parseColsParam, modelFromVisible, resolveColumnModel, serializeColumnModel, parseColumnModel, orderColumnsByModel,
} from '@/lib/columnModel';
export { EMPTY_SELECTION, toggleRow, rangeSelect, selectAll, pruneSelection } from '@/lib/rowSelection';
export type { SelectionState } from '@/lib/rowSelection';

// v0.10.939 (tablo standardı T12 + T8) — tablo içi durum satırı + toplu işlem çubuğu.
export { DataTableState } from './DataTableState';
export { DATA_TABLE_STATE_TEXT } from './dataTableStateText';
export type { DataTableStateKind, DataTableStateProps } from './DataTableState';
export { BulkBar } from './BulkBar';
export type { BulkBarAction, BulkBarProps } from './BulkBar';
