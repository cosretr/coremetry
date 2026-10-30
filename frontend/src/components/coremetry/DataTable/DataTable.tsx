// coremetry/DataTable — v0.10.1617 (shadcn Faz 1 R9, plan §6). Dondurulmuş
// API'nin (types.ts) Faz 1 gövdesi: bugünkü useDataTable + DataTableColgroup
// + DataTableHead + DataTableCell + DataTableState (ui/DataTable), `virtual`
// verilince VirtualTable. Kararı Traces dilimi verir (TanStack mı, bu mu);
// o gün yalnız bu dosya değişir. Prod tüketicisi yok.
import { Fragment, useRef, useState, type ReactNode } from 'react';
import { Ellipsis } from 'lucide-react';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, VirtualTable,
  type DataTableStateStaticProps,
} from '@/components/ui/DataTable';
import { Button } from '@/components/shadcn/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/coremetry/overlay/DropdownMenu';
import type { ColumnDef, DataTableProps } from './types';

const ACTIONS_W = 40;

export function DataTable<T>(props: DataTableProps<T>) {
  const { storageKey, columns, rows, sort, columnModel, selection, getRowHref, rowLinkReplace, onOpen, state, virtual, density, renderCell, rowActions, getRowKey, detail } = props;
  const dt = useDataTable<T>({
    storageKey, columns, rows,
    initialSort: sort?.initial,
    sortIds: sort?.ids,
    sortUrlKey: sort?.urlKey,
    serverSort: !!sort?.server,
    server: sort?.server,
    onSortChange: sort?.onChange,
    columnModel,
    selection,
    getRowHref: getRowHref ? (r: T) => getRowHref(r) ?? null : undefined,
    rowLinkReplace,
    onOpen: onOpen ? (r: T) => onOpen(r) : undefined,
  });

  // Paylaşılan ⋯ menüsü: tek kök, çapa tıklanan satır düğmesinin konumuna
  // CSS değişkeniyle taşınır (inline style yok — inlineStyleRatchet).
  const anchorRef = useRef<HTMLSpanElement>(null);
  const returnTo = useRef<HTMLElement | null>(null);
  const [active, setActive] = useState<T | null>(null);
  const openActions = (row: T, el: HTMLElement) => {
    const r = el.getBoundingClientRect();
    anchorRef.current?.style.setProperty('--dt-menu-x', `${r.left}px`);
    anchorRef.current?.style.setProperty('--dt-menu-y', `${r.bottom}px`);
    returnTo.current = el;
    setActive(row);
  };
  const trailing = rowActions ? [ACTIONS_W] : undefined;

  const cell = (row: T, col: ColumnDef<T>): ReactNode => {
    if (renderCell) return <DataTableCell key={col.id} dt={dt} col={col.id} row={row}>{renderCell(row, col)}</DataTableCell>;
    const v = col.sortValue?.(row);
    return <DataTableCell key={col.id} dt={dt} col={col.id} row={row} value={v == null ? null : v} />;
  };
  const actionsCell = (row: T) => rowActions && (
    <td className="col-actions">
      <Button size="icon-xs" variant="ghost" aria-label={rowActions.label} aria-haspopup="menu"
        onClick={(e) => { e.stopPropagation(); openActions(row, e.currentTarget); }}>
        <Ellipsis />
      </Button>
    </td>
  );
  const keyOf = (row: T, i: number) => getRowKey?.(row, i) ?? selection?.getRowId(row) ?? String(i);

  return (
    <div data-density={density}>
      {virtual ? (
        // Faz 1 sınırı: VirtualTable'ın trailing kolonu yok → `virtual` kipinde
        // rowActions ÇİZİLMEZ (colgroup kayardı). Gövde kararı Traces diliminde.
        <VirtualTable dt={dt} height={virtual.height ?? 480} rowHeight={virtual.rowHeight} overscan={virtual.overscan}
          getRowKey={keyOf}
          renderRow={(row) => <>{dt.shownColumns.map(c => cell(row, c))}</>}
          state={state} />
      ) : (
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} trailing={trailing} />
          <DataTableHead dt={dt} trailing={rowActions ? <th className="col-actions" aria-label={rowActions.label} /> : undefined} />
          <tbody>
            {dt.sortedRows.length === 0
              ? <DataTableState dt={dt} trailing={trailing} {...state} />
              : dt.sortedRows.map((row, i) => (
                <Fragment key={keyOf(row, i)}>
                  <tr {...dt.rowProps(i, row)}>
                    {dt.shownColumns.map(c => cell(row, c))}
                    {actionsCell(row)}
                  </tr>
                  {/* R5 (G4): ayrıntı satırı — veri satırı DEĞİL (rowProps yok:
                      tıklanmaz, seçilmez, sıralanmaz); tek hücre tüm genişlik. */}
                  {detail?.isOpen(row) && (
                    <tr data-dt-detail="">
                      <td colSpan={dt.shownColumns.length + (rowActions ? 1 : 0)}>{detail.render(row)}</td>
                    </tr>
                  )}
                </Fragment>
              ))}
          </tbody>
        </table>
      )}
      {rowActions && !virtual && (
        <DropdownMenu open={active !== null} onOpenChange={(o) => { if (!o) setActive(null); }}>
          <DropdownMenuTrigger asChild>
            <span ref={anchorRef} aria-hidden="true" tabIndex={-1}
              className="pointer-events-none fixed top-(--dt-menu-y) left-(--dt-menu-x) size-0" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" onCloseAutoFocus={(e) => { e.preventDefault(); returnTo.current?.focus(); }}>
            {active !== null && rowActions.items(active).map(it => (
              <DropdownMenuItem key={it.id} disabled={it.disabled} variant={it.danger ? 'destructive' : 'default'}
                onSelect={() => it.onSelect()}>
                {it.icon && <it.icon />}{it.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );
}

/** Faz 2 dilim 1 R5 (G3): gerekçeli STATİK tablonun (shadcn `Table`, T1)
 *  durum satırı — loading / error / empty, `colSpan` çağırandan. Bugünkü
 *  `ui/DataTable` `DataTableState`'in statik kipinin aynısı (aynı metinler,
 *  aynı odak inişi); geçirilen sayfa ui/ içe aktarmadan kullanır. */
export function DataTableStateRow(props: DataTableStateStaticProps) {
  return <DataTableState {...props} />;
}
