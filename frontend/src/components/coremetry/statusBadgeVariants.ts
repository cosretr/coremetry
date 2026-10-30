// statusBadgeVariants — v0.10.1616 (shadcn Faz 1 R8). StatusBadge'in cva
// tablosu + yoğunluk boyutu; bileşen dosyası yalnız bileşen dışa aktarsın
// (react-refresh).
import { cva } from 'class-variance-authority';
import type { DensitySize } from '@/lib/useDensity';

export const statusBadgeVariants = cva(
  'rounded-sm border-transparent font-bold tabular-nums',
  {
    variants: {
      status: {
        ok: 'bg-status-ok-bg text-status-ok',
        warn: 'bg-status-warn-bg text-status-warn',
        critical: 'bg-status-critical-bg text-status-critical',
        info: 'bg-status-info-bg text-status-info',
        unknown: 'bg-status-unknown-bg text-status-unknown',
      },
      size: {
        md: 'gap-[5px] px-2 py-0.5 text-[11px]',
        sm: 'gap-1 px-[5px] py-px text-[9px]',
        xs: 'gap-1 px-1 py-px text-[8px]',
      },
    },
    defaultVariants: { status: 'unknown', size: 'md' },
  },
);

const ORDER: DensitySize[] = ['md', 'sm', 'xs'];
/** Yoğunluk ve istenen boyuttan küçük olanı (sm, md'yi asla büyütmez). */
export function effectiveSize(density: DensitySize, size: 'sm' | 'md'): DensitySize {
  return ORDER[Math.max(ORDER.indexOf(density), ORDER.indexOf(size))];
}

