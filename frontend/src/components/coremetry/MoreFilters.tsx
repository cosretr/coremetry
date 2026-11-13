import { useEffect, useState, type ReactNode } from 'react';
import { ChevronRight } from 'lucide-react';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';

// MoreFilters — v0.10.2124 (Problems sadeleştirme S4, mockup operatör onaylı):
// liste sayfasında ikincil süzgeçleri kapalı bir bölüme indirir. Önde yalnız
// sık kullanılan süzgeçler kalır; kalanlar bu bölümde.
//
// Sözleşme:
//   - `activeCount > 0` (varsayılan-dışı bir süzgeç etkin) → bölüm AÇIK gelir;
//     sonradan etkinleşirse (kayıtlı görünüm, geri tuşu) yeniden açılır.
//     Operatör kapatabilir; düğme sayıyı söyler, gizli süzgeç sessiz kalmaz.
//   - Durum URL'de DEĞİL: açık/kapalı bir görünüm seçimi değil, süzgeçler
//     zaten URL'de.
//   - Yapışkan değil, akışta.
export interface MoreFiltersProps {
  /** Bölümdeki varsayılan-dışı süzgeç sayısı. */
  activeCount: number;
  label?: string;
  children: ReactNode;
}

export function MoreFilters({ activeCount, label = 'Diğer süzgeçler', children }: MoreFiltersProps) {
  const [open, setOpen] = useState(activeCount > 0);
  const active = activeCount > 0;
  useEffect(() => {
    if (active) setOpen(true);
  }, [active]);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="mb-2" data-testid="more-filters">
      <CollapsibleTrigger className="group/mf inline-flex cursor-pointer items-center gap-1.5 rounded-(--radius-sm) border-0 bg-transparent px-1 py-0.5 text-[length:var(--fs-sm)] text-(--text2) hover:text-(--text) focus-visible:outline-2 focus-visible:outline-(--focus)">
        <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 transition-transform group-data-[state=open]/mf:rotate-90" />
        {label}
        {active && <span className="text-(--accent2)">{` (${activeCount} etkin)`}</span>}
      </CollapsibleTrigger>
      <CollapsibleContent className="pt-2">
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
}
