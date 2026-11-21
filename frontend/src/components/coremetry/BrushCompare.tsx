// BrushCompare — dalga 3 (2026-11-21, Honeycomb BubbleUp "brush to compare").
// Grafiklerde sürükleme zaten yakınlaştırıyor (usePageZoomRange: brush
// öncesi görünüm yığının TABANI = zoomBase, URL'deki aralık = brush). Bu
// şerit yakınlaştırılmışken "Karşılaştır" sunar: seçili dilimdeki span'ler
// vs operatörün penceresi (zoomBase) — aynı süzgeç, iki pencere — mevcut
// BubbleUpPanel'in zaman kipiyle (baseWindow → /api/spans/bubbleup/compare).
// Yeni panel yok; BubbleUp genişletildi.
import { useMemo, useState } from 'react';
import { BubbleUpPanel } from '@/components/BubbleUpPanel';
import { Chip } from '@/components/ui/Chip';
import { timeRangeToNs, fmtNs } from '@/lib/utils';
import { useT } from '@/lib/i18n';
import type { FilterExpr, TimeRange } from '@/lib/types';

/** Taban penceresi ≤ 24 sa (uç sınırı): daha genişse seçimin sonuna hizalı son 24 sa. */
export function compareWindows(sel: { from: number; to: number }, base: { from: number; to: number }) {
  const DAY = 24 * 3600e9;
  const lo = Math.min(sel.from, base.from), hi = Math.max(sel.to, base.to);
  if (hi - lo <= DAY) return { sel, base };
  const bTo = Math.max(sel.to, Math.min(base.to, sel.from + DAY));
  return { sel, base: { from: Math.max(base.from, bTo - DAY), to: bTo } };
}

export function BrushCompare({ range, zoomBase, baseline }: {
  range: TimeRange;
  /** usePageZoomRange.zoomBase — null = yakınlaştırma yok → şerit çizilmez. */
  zoomBase: TimeRange | null;
  baseline: FilterExpr[];
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const win = useMemo(() => (zoomBase
    ? compareWindows(timeRangeToNs(range), timeRangeToNs(zoomBase)) : null), [range, zoomBase]);
  if (!win) return null;
  return (
    <div data-testid="brush-compare" className="my-2">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{t('brush.compare.sel').replace('{d}', fmtNs(win.sel.to - win.sel.from))}</span>
        <Chip pill size="xs" active={open} data-testid="brush-compare-toggle"
          title={t('brush.compare.tip')} onClick={() => setOpen(v => !v)}>
          {t('brush.compare.btn')}
        </Chip>
      </div>
      {open && (
        <BubbleUpPanel baseline={baseline} selection={[]}
          from={win.sel.from} to={win.sel.to} baseWindow={win.base} />
      )}
    </div>
  );
}
