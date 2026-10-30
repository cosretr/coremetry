// coremetry/StatusBadge — v0.10.1616 (shadcn Faz 1 R8, plan §6). shadcn/badge
// üzerinde durum rozeti; renk YALNIZ --status-* token'larından (Tailwind
// `bg-status-*-bg text-status-*`, tokens.css). Bugünkü `.badge .b-*`
// ölçüsünün aynısı: 11px / 700 / 2px 8px / --radius-sm; yoğunlukla
// compact 9px · 1px 5px, dense 8px · 1px 4px (globals.css [data-density]
// .badge kuralları). `size="sm"` en fazla compact ölçüsü. Prod tüketicisi
// yok (Faz 2 dilimleri `.badge .b-*` sitelerini sayfa sayfa buna çevirir).
import type { ReactNode } from 'react';
import { Badge } from '@/components/shadcn/badge';
import { useDensitySize } from '@/lib/useDensity';
import { effectiveSize, statusBadgeVariants } from './statusBadgeVariants';
import { useT } from '@/lib/i18n';
import type { Status } from '@/lib/statusTone';

export interface StatusBadgeProps {
  status: Status;
  /** Etiket; verilmezse durumun çevirisi (status.<s>). */
  children?: ReactNode;
  size?: 'sm' | 'md';
  dot?: boolean;
  title?: string;
}

export function StatusBadge({ status, children, size = 'md', dot = false, title }: StatusBadgeProps) {
  const t = useT();
  const eff = effectiveSize(useDensitySize(), size);
  return (
    <Badge variant="outline" data-status={status} title={title} className={statusBadgeVariants({ status, size: eff })}>
      {dot && <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />}
      {children ?? t(`status.${status}`)}
    </Badge>
  );
}
