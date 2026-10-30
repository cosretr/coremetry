// coremetry/FilterBar — v0.10.1617 (shadcn Faz 1 R9, plan §6). DONDURULMUŞ
// API; coremetry/PageControls'ü BİRLEŞTİRİR (karar k). Faz 1 gövdesi:
// sorgu kutusu (shadcn input + bg-field, plan §2.2), çip listesi
// (lib/filterChips biçimiyle, kaldırılabilir), saved / actions / children
// yuvaları. Çip DÜZENLEYİCİ açılır penceresi ve suggestKeys/suggestValues
// (sunucu tarafı, gecikmeli — asla katalog) Logs diliminde gelir; tipleri
// şimdi donuk.
import type { ReactNode } from 'react';
import { X } from 'lucide-react';
import { PageControls } from '@/components/coremetry/PageControls';
import { Input } from '@/components/shadcn/input';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { formatChip, type FilterChip } from '@/lib/filterChips';
import { CM_STRINGS } from '../strings';

export type { FilterChip, FilterOp } from '@/lib/filterChips';

export interface FilterBarProps {
  chips: FilterChip[];
  onChange(chips: FilterChip[]): void;
  /** Sunucu araması (gecikmeli) — tam katalog DEĞİL (hard constraint). */
  suggestKeys?(q: string): Promise<string[]>;
  suggestValues?(key: string, q: string): Promise<string[]>;
  query?: { value: string; onChange(v: string): void; placeholder?: string };
  /** SavedViewsBar yuvası. */
  saved?: ReactNode;
  sticky?: boolean;
  actions?: ReactNode;
  children?: ReactNode;
  /** Çip kaldırma düğmesinin erişilebilir adı (çipin metni sona eklenir). */
  removeLabel?: string;
}

export function FilterBar({ chips, onChange, query, saved, sticky, actions, children, removeLabel = CM_STRINGS.removeFilter }: FilterBarProps) {
  return (
    <PageControls sticky={sticky} actions={actions}>
      {query && (
        <Input className="h-8 w-64 bg-field" value={query.value} placeholder={query.placeholder}
          aria-label={query.placeholder ?? CM_STRINGS.filter} onChange={(e) => query.onChange(e.target.value)} />
      )}
      {chips.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap items-center gap-1 p-0" aria-label={CM_STRINGS.filters}>
          {chips.map((c, i) => {
            const text = formatChip(c);
            return (
              <li key={`${i}:${text}`}>
                <Badge variant="outline" className="font-mono">
                  {text}
                  <Button size="icon-xs" variant="ghost" aria-label={`${removeLabel}: ${text}`}
                    onClick={() => onChange(chips.filter((_, j) => j !== i))}>
                    <X />
                  </Button>
                </Badge>
              </li>
            );
          })}
        </ul>
      )}
      {children}
      {saved}
    </PageControls>
  );
}
