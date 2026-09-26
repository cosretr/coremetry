import type { ReactNode, TdHTMLAttributes } from 'react';
import { Link } from 'react-router-dom';
import { MiddleEllipsis } from './MiddleEllipsis';
import type { DataTable } from './DataTable';

// DataTableCell — v0.10.939 (tablo standardı dilim 2): kolon bayraklarını
// ve satır linkini TEK hücrede birleştirir. Sayfa sınıf yazmaz:
//
//   <DataTableCell dt={dt} col="service" row={r} value={r.service} />
//
//   • sınıf  — `dt.cellProps(row, col, value)`: num / mono / cell-err|warn|
//              muted|faint / cell-wrap / col-actions (T4/T5/T8/T9/T11)
//              + link çizildiyse `row-cell` (dolgu linke — yalnız BURADA
//              basılır, cellProps basmaz: linksiz hücre dolgusunu korur)
//   • title  — kırpılan dizge değerin TAMAMI (T11; sayı kolonunda yalnız
//              `truncate` açıkça verilmişse); sayfanın `title`ı kazanır
//   • içerik — `children` verilmişse o; yoksa `value` (truncate 'middle' ve
//              dizge → <MiddleEllipsis>); değer yoksa (null / undefined / '')
//              soluk "—" (`span.cell-empty`, KeyValue'nun boş glifiyle aynı —
//              T4: boş hücre boşluk değil, "değer yok" okunur)
//   • link   — getRowHref'li tabloda düz hücre `<Link class="row-link">`
//              ile sarılır (`dt.rowLink(row, col)`); yalnız ilk link hücresi
//              Tab durağı (T7). ownLink / actions kolonu sarılmaz.
//
// Sabit kolon (sticky-left/right) sınıfı + `left` ofseti çağıranın
// `className`/`style`ıyla gelir (leading hücre genişliğini primitif bilmez).

export interface DataTableCellProps<T> extends TdHTMLAttributes<HTMLTableCellElement> {
  dt: DataTable<T>;
  /** Kolon kimliği (ColumnDef.id). */
  col: string;
  row: T;
  /** Görüntülenen değer; dizge ise kırpılınca ipucu olur. Boşsa (null /
   *  undefined / '') ve `children` yoksa soluk "—". */
  value?: string | number | null;
  children?: ReactNode;
}

export function DataTableCell<T>({ dt, col, row, value, children, className, title, ...rest }: DataTableCellProps<T>) {
  const cp = dt.cellProps(row, col, value);
  const link = dt.rowLink(row, col);
  const column = dt.allColumns.find(c => c.id === col);
  const content: ReactNode = children !== undefined
    ? children
    : value == null || value === ''
      // v0.10.939 (tablo standardı T4) — boş değer soluk "—" (KeyValue ile aynı glif).
      ? <span className="cell-empty">—</span>
      : column?.truncate === 'middle' && typeof value === 'string'
        ? <MiddleEllipsis text={value} title={title ?? cp.title} />
        : value;
  // v0.10.939 (tablo standardı T7) — `row-cell` yalnız link GERÇEKTEN çizildiğinde.
  const cls = [cp.className, link ? 'row-cell' : '', className].filter(Boolean).join(' ') || undefined;
  return (
    <td {...rest} className={cls} title={title ?? cp.title}>
      {link ? <Link {...link}>{content}</Link> : content}
    </td>
  );
}
