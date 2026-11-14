import type { CSSProperties } from 'react';
import { pctColor } from './thresholds';

// MiniBar — eşik-renkli ince kullanım çubuğu (v0.9.32, design
// handoff — cluster kartı / node heatmap / tablo hücreleri paylaşır).
// Track var(--border), fill pctColor (override edilebilir). pct null
// → boş track (veri yok). Tema token'ı, hardcoded hex yok.
// Clusters S2 — statik stil `.cl-bar*` sınıflarında (globals.css); ölçü ve
// doluluk hesaplanan değer, stil nesnesi olarak kalır.
const trackBox = (width: number | string | undefined, height: number): CSSProperties =>
  ({ width: width ?? '100%', height });
const fillBox = (pct: number, color: string): CSSProperties => ({ width: `${pct}%`, background: color });

export function MiniBar({ pct, color, height = 5, width }: {
  pct: number | null;
  color?: string;
  height?: number;
  width?: number | string;
}) {
  const shown = pct == null ? 0 : Math.max(0, Math.min(100, pct));
  return (
    <div className="cl-bar" style={trackBox(width, height)}>
      {pct != null && (
        <div className="cl-bar__fill" style={fillBox(shown, color ?? pctColor(shown))} />
      )}
    </div>
  );
}
