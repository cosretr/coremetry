// coremetry/dashboard/PanelFrameMenu — v0.10.1998 (Dashboards Faz 3 dilim
// 3.4; audit §5.3 "Panel başlığı: tek ⋯").
//
// Pano panel çerçevesinin ⋯'ü. Grafik çizen panelde (metric / spanmetric /
// promql) aynı eylemler CorePanel'in kendi ⋯'üne iner ve bu menü ÇİZİLMEZ
// (components/dashboard/panelMenuHost); grafik yokken (yükleniyor / hata /
// veri yok) ve grafik olmayan panellerde (stat, gauge, top-N, heatmap,
// markdown) panelin tek menüsü budur. Eylem şekli CorePanel'in menuExtra'sı
// ile AYNI (lib/chart/panelMenu PanelMenuAction): iki yüzeyde tek liste.
import { useState } from 'react';
import { Ellipsis } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/coremetry/overlay/DropdownMenu';
import type { PanelMenuAction } from '@/lib/chart/panelMenu';

export function PanelFrameMenu({ actions, label }: { actions: PanelMenuAction[]; label: string }) {
  const [open, setOpen] = useState(false);
  if (actions.length === 0) return null;
  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button size="icon-xs" variant="ghost" aria-label={label} draggable={false}
          onMouseDown={e => e.stopPropagation()}>
          <Ellipsis />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {actions.map(a => (
          <DropdownMenuItem key={a.key} disabled={a.disabled}
            onSelect={e => { if (a.keepOpen) e.preventDefault(); a.onClick(); }}>
            {a.label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
