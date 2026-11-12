// coremetry/dashboard/DashboardListFilters — Dashboards Faz 3 dilim 3.1
// (audit §5.2, mockup görünüm 4): etiket çipleri + "Yalnız favoriler".
//
// Seçim dolu birincil (aria-pressed, buton Öneri B). Değerler URL'de
// (?tags / ?fav); bileşen yalnız çizer ve yamayı yukarı verir. Klasör yok:
// etiket aynı işi şemasız görür (audit §5.2).
import { Star } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import type { TagCount } from '@/lib/dashboardList';

export interface DashboardListFiltersProps {
  chips: TagCount[];
  selected: readonly string[];
  fav: boolean;
  onToggleTag(tag: string): void;
  onFav(next: boolean): void;
}

export function DashboardListFilters({ chips, selected, fav, onToggleTag, onFav }: DashboardListFiltersProps) {
  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Etiket süzgeci">
      {chips.map(c => {
        const on = selected.includes(c.tag);
        return (
          <Button key={c.tag} type="button" variant="outline" size="xs" aria-pressed={on}
            title={on ? `"${c.tag}" süzgecini kaldır` : `Yalnız "${c.tag}" etiketli panolar (${c.n})`}
            onClick={() => onToggleTag(c.tag)}>
            {c.tag}
          </Button>
        );
      })}
      <Button type="button" variant="outline" size="xs" aria-pressed={fav} onClick={() => onFav(!fav)}>
        <Star aria-hidden="true" />Yalnız favoriler
      </Button>
    </div>
  );
}
