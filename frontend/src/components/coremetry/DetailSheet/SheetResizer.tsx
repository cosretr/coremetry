// SheetResizer — v0.10.1669 (etkileşim dilimi S1, plan §2.1 / Q9). DetailSheet'in
// sol kenar tutamağı; `ui/Drawer resizeKey` davranışının ikizi: sola sürükle →
// genişler (sağa sabit), bırakınca `storageKey`e yazılır, çift tık varsayılana
// döner ve anahtarı boşaltır. Kelepçe lib/drawerWidth. <640px gizli (tam
// genişlik). Görünüm Drawer'ın `.drawer-resizer`ı; inline stil yok.
import { useEffect, useRef } from 'react';
import { clampDrawerWidth } from '@/lib/drawerWidth';
import { setRaw } from '@/lib/storage';
import { CM_STRINGS } from '../strings';

export interface SheetResizerProps {
  storageKey: string;
  width: number;
  onWidth(w: number): void;
  onReset(): void;
}

export function SheetResizer({ storageKey, width, onWidth, onReset }: SheetResizerProps) {
  const drag = useRef<{ startX: number; startW: number; w: number } | null>(null);
  const cb = useRef(onWidth);
  useEffect(() => { cb.current = onWidth; }, [onWidth]);
  useEffect(() => {
    const onMove = (e: PointerEvent) => {
      const d = drag.current;
      if (!d) return;
      d.w = clampDrawerWidth(d.startW + (d.startX - e.clientX), window.innerWidth);
      cb.current(d.w);
    };
    const onUp = () => {
      const d = drag.current;
      if (!d) return;
      drag.current = null;
      document.body.classList.remove('cm-col-resizing');
      setRaw(storageKey, String(d.w));
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
    };
  }, [storageKey]);
  return (
    <div className="drawer-resizer max-sm:hidden" role="separator" aria-orientation="vertical"
      aria-label={CM_STRINGS.sheetWidth} title={CM_STRINGS.sheetResizeHint}
      data-sheet-resize=""
      onPointerDown={e => {
        e.preventDefault();
        drag.current = { startX: e.clientX, startW: width, w: width };
        document.body.classList.add('cm-col-resizing');
      }}
      onDoubleClick={() => { setRaw(storageKey, ''); onReset(); }} />
  );
}
