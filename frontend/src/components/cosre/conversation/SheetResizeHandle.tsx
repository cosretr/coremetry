// SheetResizeHandle — v0.10.1664 (CoSRE v2 Faz 2b B6, plan §4.1 / Q5). v2
// Sheet'inin sol kenar tutamağı; v1 `<Drawer resizeKey>` davranışının ikizi:
// sola sürükle → genişler (sağa sabit), bırakınca AYNI localStorage anahtarına
// (COSRE_DRAWER_WIDTH_KEY) yazılır, çift tık varsayılana döner ve anahtarı
// boşaltır. Kelepçe lib/drawerWidth (v1 ile aynı). <640px gizli (tam genişlik).
// Sınıf v1'in `.drawer-resizer`ı (aynı görünüm); inline style yok.
import { useEffect, useRef } from 'react';
import { clampDrawerWidth } from '@/lib/drawerWidth';
import { setRaw } from '@/lib/storage';
import { COSRE_DRAWER_WIDTH_KEY } from '@/lib/cosre/sheetWidth';

export interface SheetResizeHandleProps {
  width: number;
  /** Sürükleme sırasında canlı genişlik. */
  onWidth(w: number): void;
  /** Çift tık: varsayılan genişlik. */
  onReset(): void;
}

export function SheetResizeHandle({ width, onWidth, onReset }: SheetResizeHandleProps) {
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
      setRaw(COSRE_DRAWER_WIDTH_KEY, String(d.w));
    };
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    window.addEventListener('pointercancel', onUp);
    return () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      window.removeEventListener('pointercancel', onUp);
    };
  }, []);
  return (
    <div className="drawer-resizer max-sm:hidden" role="separator" aria-orientation="vertical"
      aria-label="Çekmece genişliği" title="Sürükle: genişlet · çift tık: varsayılan genişlik"
      data-cosre-resize=""
      onPointerDown={e => {
        e.preventDefault();
        drag.current = { startX: e.clientX, startW: width, w: width };
        document.body.classList.add('cm-col-resizing');
      }}
      onDoubleClick={() => { setRaw(COSRE_DRAWER_WIDTH_KEY, ''); onReset(); }} />
  );
}
