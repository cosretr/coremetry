// HeatmapOverlayLayer — v0.10.1870 (öğrenme döngüsü Faz 2 parti B, audit
// §7.2 "Trace: ısı haritası + baseline çizgisi"). LatencyHeatmap canvas'ının
// üstüne mutlak konumlu katman: anomali penceresi gölgesi → kesikli referans
// çizgisi (+ etiket) → oynatma imleci. Canvas'a DOKUNMAZ (imleç her karede
// yeniden boyatma yapmaz); geometri SAF (lib/chart/heatmapOverlay.ts), stil
// Tailwind sınıfı, yalnız konum/boyut hesaplanan nesne.
import type { CSSProperties } from 'react';
import { heatmapCursorCol, heatmapCursorX } from '@/lib/chart/heatmapCursor';
import { heatmapRowPos, heatmapTimeFrac, type HeatmapOverlay } from '@/lib/chart/heatmapOverlay';

export interface HeatmapGrid { width: number; height: number; padL: number; padR: number; padT: number; padB: number }

export function HeatmapOverlayLayer({ times, bins, overlay, grid }: {
  times: readonly number[]; bins: readonly number[]; overlay: HeatmapOverlay; grid: HeatmapGrid;
}) {
  const plotW = Math.max(1, grid.width - grid.padL - grid.padR);
  const plotH = Math.max(1, grid.height - grid.padT - grid.padB);
  const rows = bins.length;
  const a = overlay.windowNs ? heatmapTimeFrac(times, overlay.windowNs[0]) : null;
  const b = overlay.windowNs ? heatmapTimeFrac(times, overlay.windowNs[1]) : null;
  const r = overlay.lineMs ? heatmapRowPos(bins, overlay.lineMs) : null;
  const col = overlay.cursorSec != null ? heatmapCursorCol([...times], overlay.cursorSec) : null;
  const cellW = times.length > 0 ? plotW / times.length : 0;
  const win: CSSProperties | null = a != null && b != null && b > a
    ? { left: grid.padL + a * plotW, width: (b - a) * plotW, top: grid.padT, height: plotH } : null;
  const line: CSSProperties | null = r != null && rows > 0
    ? { left: grid.padL, width: plotW, top: grid.padT + plotH * (1 - r / rows) } : null;
  const cur: CSSProperties | null = col != null
    ? { left: heatmapCursorX(col, grid.padL, cellW), top: grid.padT, height: plotH } : null;
  return (
    <>
      {win && <div data-testid="heatmap-overlay-window" className="pointer-events-none absolute z-[1] border-l border-[color:var(--err)] bg-[color:var(--err-bg)] opacity-70" style={win} />}
      {line && (
        <div data-testid="heatmap-overlay-line" className="pointer-events-none absolute z-[2] h-0 border-t-[1.5px] border-dashed border-[color:var(--text)]" style={line}>
          {overlay.lineLabel && (
            <span className="absolute bottom-0.5 left-1 rounded-[2px] bg-[color:var(--bg1)] px-[3px] text-[10px] leading-tight whitespace-nowrap text-[color:var(--text)]">{overlay.lineLabel}</span>
          )}
        </div>
      )}
      {cur && <div data-testid="heatmap-overlay-cursor" className="pointer-events-none absolute z-[3] w-0.5 bg-[color:var(--accent)]" style={cur} />}
    </>
  );
}
