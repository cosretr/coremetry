import type { ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';

// TableViewBar — v0.10.2141 (servis sekmeleri sadeleştirme S3,
// docs/frontend/service-tabs-plan.md; mockup operatör onayı 2026-11-13).
// Sadeleştirilmiş kayıt tablosunun ALT satırı: katlanan normal satırların
// cümlesi ("N daha, hepsi normal · hepsini göster"), "Tüm kolonlar" düğmesi
// ve küçük "kaynak" satırı (eski başlık rozeti — "bu sayı nereden" sorusu
// kaybolmaz, yalnız yer değiştirir; CollapsedSection ile aynı dil).
//
// Hiçbir şey çekmez, hiçbir şey saklamaz: katlama (lib/normalFold) ve kolon
// modeli (useDataTable columnModel.hidden) çağıranda; kalıcılık çağıranda.
export interface TableViewBarProps {
  /** Katlanabilir (normal) satır sayısı; 0 ise satır cümlesi çizilmez. */
  foldable: number;
  /** Katlanan satırlar şu an görünüyor mu. */
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  /** "Tüm kolonlar" açık mı; verilmezse düğme çizilmez. */
  allColumns?: boolean;
  onAllColumnsChange?: (all: boolean) => void;
  /** Okunan kaynak — tablonun altındaki "kaynak" satırı. */
  source?: ReactNode;
}

export function TableViewBar({ foldable, expanded, onExpandedChange, allColumns, onAllColumnsChange, source }: TableViewBarProps) {
  const hasCols = allColumns !== undefined && !!onAllColumnsChange;
  if (foldable <= 0 && !hasCols && !source) return null;
  return (
    <div className="mt-1.5 flex flex-col gap-1 text-[length:var(--fs-xs)] text-(--text3)">
      {(foldable > 0 || hasCols) && (
        <div className="flex min-h-6 items-center gap-1">
          {foldable > 0 && (expanded ? (
            <Button variant="link" size="xs" className="px-0" onClick={() => onExpandedChange(false)}>
              {foldable} normal satırı gizle
            </Button>
          ) : (
            <>
              <span>{foldable} daha, hepsi normal ·</span>
              <Button variant="link" size="xs" className="px-0" onClick={() => onExpandedChange(true)}>
                hepsini göster
              </Button>
            </>
          ))}
          <span className="flex-1" />
          {hasCols && (
            <Button variant="outline" size="xs" aria-pressed={allColumns} onClick={() => onAllColumnsChange(!allColumns)}>
              Tüm kolonlar
            </Button>
          )}
        </div>
      )}
      {source && <p className="m-0">kaynak: {source}</p>}
    </div>
  );
}
