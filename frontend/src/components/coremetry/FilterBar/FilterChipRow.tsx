// coremetry/FilterBar/FilterChipRow — v0.10.1854 (Log Search Faz 2 dilim 2.3;
// audit §6 "Filtre hapları (sabit / ters / kapalı)", mockup görünüm 1 `.pill`).
// FilterBar ailesinin hap satırı: her hap `etiket? · anahtar: · değer` + ✎ ≠ ◐
// ⊙ ✕. ✎ FilterChipEditor açılır penceresini açar (coremetry/overlay Popover —
// Esc lib/escLayer'a ait). Bayraklar (`pinned`, `disabled`) FilterChip üstünde;
// her eylem `onChange(yeni liste)` — durum çağıranda (URL).
//   · sabit (⊙): etiket "⊙ sabit" (vurgu); "servis/sorgu değişince kalır" —
//     anlamı çağıranın (Logs: sıfırlama/temizle/URL içe aktarma sabitleri korur).
//   · ters (≠): `=`↔`!=`, `~`↔`!~`, `exists`↔`!exists`, `in`↔`!in`; aralık
//     (>= / <=) ters çevrilmez (≥ yerine ≤ seçilir) — düğme pasif.
//   · kapalı (◐): sorguya girmez, hap kesik çizgili + üstü çizili kalır.
import { useState, type ReactNode } from 'react';
import { CircleDot, Contrast, Pencil, X } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/coremetry/overlay';
import type { FilterChip, FilterOp } from '@/lib/filterChips';
import { FilterChipEditor } from './FilterChipEditor';
import { CM_STRINGS } from '../strings';

const NEGATE: Partial<Record<FilterOp, FilterOp>> = {
  '=': '!=', '!=': '=', '~': '!~', '!~': '~', exists: '!exists', '!exists': 'exists', in: '!in', '!in': 'in',
};
export const isNegatedOp = (op: FilterOp) => op === '!=' || op === '!~' || op === '!exists' || op === '!in';

/** Hap metni (anahtar + değer kısmı) — ekran ve erişilebilir ad aynı kaynaktan. */
export function chipParts(c: FilterChip): { k: string; v: string } {
  switch (c.op) {
    case 'exists': case '!exists': return { k: `∃ ${c.key}`, v: '' };
    case '>=': return { k: `${c.key} ≥`, v: c.value ?? '' };
    case '<=': return { k: `${c.key} ≤`, v: c.value ?? '' };
    case 'in': case '!in': return { k: `${c.key} ∈`, v: `(${(c.values ?? []).join(', ')})` };
    case '~': case '!~': return { k: `${c.key} ~`, v: c.value ?? '' };
    default: return { k: `${c.key}:`, v: c.value ?? '' };
  }
}

export interface FilterChipRowProps {
  chips: FilterChip[];
  onChange(chips: FilterChip[]): void;
  /** Düzenleyicinin işleçleri. */
  ops?: readonly FilterOp[];
  suggestValues?(key: string, q: string, signal: AbortSignal): Promise<string[]>;
  /** Hapların ÖNÜNE (ör. trace kilidi, desen, takım çipleri). */
  leading?: ReactNode;
  /** Hapların ARKASINA (ör. + Süzgeç, Tümünü temizle). */
  trailing?: ReactNode;
  ariaLabel?: string;
  className?: string;
}

const ICON_BTN = 'h-full rounded-none border-l px-1.5 text-(--text3) hover:text-(--text)';

export function FilterChipRow({ chips, onChange, ops, suggestValues, leading, trailing, ariaLabel = CM_STRINGS.filters, className }: FilterChipRowProps) {
  const [editing, setEditing] = useState<number | null>(null);
  const patch = (i: number, p: Partial<FilterChip> | null) =>
    onChange(p === null ? chips.filter((_, j) => j !== i) : chips.map((c, j) => (j === i ? { ...c, ...p } : c)));
  return (
    <div role="group" aria-label={ariaLabel} className={'flex flex-wrap items-center gap-1.5 ' + (className ?? '')}>
      {leading}
      {chips.map((c, i) => {
        const neg = isNegatedOp(c.op);
        const { k, v } = chipParts(c);
        const text = `${neg ? 'DEĞİL ' : ''}${k}${v ? ' ' + v : ''}`;
        const tag = c.pinned ? '⊙ sabit' : neg ? 'DEĞİL' : c.disabled ? 'kapalı' : '';
        const tone = c.pinned ? 'bg-(--accent-bg) text-(--accent2)' : neg ? 'bg-status-critical-bg text-status-critical' : 'text-(--text3)';
        const flip = NEGATE[c.op];
        return (
          <Popover key={`${i}:${c.key}:${c.op}`} open={editing === i} onOpenChange={o => setEditing(o ? i : null)}>
            <PopoverAnchor asChild>
              <span data-testid="filter-chip" data-pinned={c.pinned ? '1' : undefined} data-off={c.disabled ? '1' : undefined}
                className={'inline-flex h-6 items-stretch overflow-hidden rounded-sm border text-xs '
                  + (neg ? 'border-status-critical ' : 'border-(--border-control) ')
                  + (c.disabled ? 'border-dashed bg-transparent' : 'bg-(--bg1)')}>
                {tag && <span className={'inline-flex items-center border-r px-1.5 text-[10px] font-bold ' + tone}
                  title={c.pinned ? 'Sabitlendi: servis/sorgu değişince kalır' : undefined}>{tag}</span>}
                <span className={'inline-flex max-w-[28rem] items-center gap-1 px-2 ' + (c.disabled ? 'text-(--text-faint) line-through' : '')}
                  title={text}>
                  <span className="text-(--text2)">{k}</span>
                  {v && <span className="truncate font-mono font-medium">{v}</span>}
                </span>
                <Button size="icon-xs" variant="ghost" className={ICON_BTN} aria-label={`Düzenle: ${text}`} title="Düzenle"
                  onClick={() => setEditing(editing === i ? null : i)}><Pencil /></Button>
                <Button size="icon-xs" variant="ghost" className={ICON_BTN} disabled={!flip}
                  aria-label={`Ters çevir: ${text}`} title={flip ? 'Ters çevir' : 'Aralık süzgeci ters çevrilmez — ≥ yerine ≤ seçin'}
                  onClick={() => flip && patch(i, { op: flip, disabled: false })}><span aria-hidden="true">≠</span></Button>
                <Button size="icon-xs" variant="ghost" className={ICON_BTN} aria-pressed={!!c.disabled}
                  aria-label={`${c.disabled ? 'Aç' : 'Kapat'}: ${text}`} title={c.disabled ? 'Aç (sorguya geri al)' : 'Kapat (hap kalır, sorguya girmez)'}
                  onClick={() => patch(i, { disabled: !c.disabled })}><Contrast /></Button>
                <Button size="icon-xs" variant="ghost" className={ICON_BTN} aria-pressed={!!c.pinned}
                  aria-label={`${c.pinned ? 'Sabitlemeyi kaldır' : 'Sabitle'}: ${text}`}
                  title={c.pinned ? 'Sabitlemeyi kaldır' : 'Sabitle — servis/sorgu değişince ve temizlerken kalır'}
                  onClick={() => patch(i, { pinned: !c.pinned })}><CircleDot /></Button>
                <Button size="icon-xs" variant="ghost" className={ICON_BTN} aria-label={`${CM_STRINGS.removeFilter}: ${text}`} title="Sil"
                  onClick={() => { setEditing(null); patch(i, null); }}><X /></Button>
              </span>
            </PopoverAnchor>
            <PopoverContent align="start" className="w-auto p-3">
              <FilterChipEditor chip={c} ops={ops} suggestValues={suggestValues}
                onApply={next => { setEditing(null); patch(i, next.key.trim() ? next : null); }}
                onCancel={() => setEditing(null)} />
            </PopoverContent>
          </Popover>
        );
      })}
      {trailing}
    </div>
  );
}
