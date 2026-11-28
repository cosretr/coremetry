// EntityHoverCard — v0.10.2488 (entity tutarlılık denetimi dilim 4, §3.5,
// Karar A5). `EntityLink`'in tembel hover kartı (ayrı parça; ilk hover'da
// yüklenir).
//
// v1 İSTEK ATMAZ: yalnız tür kaydı (ikon + tür adı), tam ad, satırın verdiği
// sağlık + en çok 3 ölçü ve saf pivot URL'leri (Log / Metrik sekmesi). Hiçbir
// `api.*` / `fetch` / React Query çağrısı yok — `EntityLink.test.tsx` hover
// boyunca ağ çağrısı sayısının 0 kaldığını kilitler. Özet ucu ayrı karar (A5).
//
// Yüzey: coremetry overlay Popover (beyaz `bg-popover` yüzey, mevcut sınıflar;
// yeni CSS yok); bağlantıya sanal çapa ile bağlanır, odak çalmaz.
import type { RefObject } from 'react';
import { Link } from 'react-router';
import {
  Box, Boxes, Cpu, Database, Globe, HardDrive, Layers, MessageSquare, Package, Server, Webhook,
  type LucideIcon,
} from 'lucide-react';
import { Popover, PopoverAnchor, PopoverContent } from '@/components/coremetry/overlay';
import { ENTITY_KINDS, entityPivots, type EntityIconName, type EntityKind, type EntityTarget } from '@/lib/entityKinds';
import { useT } from '@/lib/i18n';
import { HealthDot } from '../HealthDot';
import type { EntityCardData } from './EntityLink';

const ICONS: Record<EntityIconName, LucideIcon> = {
  Boxes, Webhook, Database, MessageSquare, Server, Cpu, Layers, Package, Box, Globe, HardDrive,
};

export default function EntityHoverCard({ anchor, kind, target, name, data, onHold, onLeave, onClose }: {
  anchor: RefObject<HTMLAnchorElement | null>;
  kind: EntityKind;
  target: EntityTarget;
  name: string;
  data?: EntityCardData;
  onHold(): void;
  onLeave(): void;
  onClose(): void;
}) {
  const t = useT();
  const spec = ENTITY_KINDS[kind];
  const Icon = ICONS[spec.icon];
  const metrics = (data?.metrics ?? []).slice(0, 3);
  const pivots = entityPivots(target);
  return (
    <Popover open onOpenChange={o => { if (!o) onClose(); }}>
      <PopoverAnchor virtualRef={anchor as RefObject<HTMLAnchorElement>} />
      <PopoverContent side="bottom" align="start" className="w-auto max-w-xs p-3 text-xs"
        data-testid="entity-hover-card"
        onOpenAutoFocus={e => e.preventDefault()} onCloseAutoFocus={e => e.preventDefault()}
        onMouseEnter={onHold} onMouseLeave={onLeave}>
        <div className="flex items-center gap-1 text-muted-foreground">
          <Icon size={12} strokeWidth={1.75} aria-hidden="true" />
          <span>{t(spec.labelKey)}</span>
        </div>
        <div className="font-medium break-all">{name}</div>
        {data?.health && (
          <div className="flex items-center gap-1 mt-1">
            <HealthDot level={data.health} title={data.healthTitle} />
            <span>{data.healthTitle || t(`hd.${data.health}`)}</span>
          </div>
        )}
        {metrics.length > 0 && (
          <dl className="mt-1">
            {metrics.map(m => (
              <div key={m.label} className="flex justify-between gap-3">
                <dt className="text-muted-foreground">{m.label}</dt>
                <dd className="tabular-nums">{m.value}</dd>
              </div>
            ))}
          </dl>
        )}
        {pivots.length > 0 && (
          <div className="flex gap-3 mt-1">
            {pivots.map(p => <Link key={p.key} to={p.href}>{t(`ek.pivot.${p.key}`)}</Link>)}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
