// v0.10.1643 — where a waterfall bar's duration label goes. Pure: a
// function of track width, bar %, and label length (mono font, so a
// char-count estimate is exact enough) — no per-row DOM measuring.

/** .wf-bar horizontal padding (globals.css). border-box → a bar is never painted narrower than 2×this. */
export const WF_BAR_PAD_PX = 5;
/** .wf-bar-label: 10px var(--font-mono) ≈ 0.6em per glyph, rounded up. */
export const WF_LABEL_CHAR_PX = 6.2;
/** Gap between the bar edge and an outside label. */
export const WF_LABEL_GAP_PX = 4;

export type WfLabelPlacement = 'inside' | 'right' | 'left';

/** The bar's painted width: CSS width% floored by its own padding. */
export function wfBarVisualPx(barAreaPx: number, widthPct: number): number {
  return Math.max((barAreaPx * widthPct) / 100, 2 * WF_BAR_PAD_PX);
}

export function wfLabelPlacement(p: {
  barAreaPx: number; startPct: number; widthPct: number; label: string;
}): WfLabelPlacement {
  const { barAreaPx, startPct, widthPct, label } = p;
  // ResizeObserver hasn't fired yet: keep the legacy % rule for that paint.
  if (barAreaPx <= 0) return widthPct > 6 ? 'inside' : 'right';
  const labelPx = label.length * WF_LABEL_CHAR_PX;
  const barPx = wfBarVisualPx(barAreaPx, widthPct);
  if (barPx >= labelPx + 2 * WF_BAR_PAD_PX) return 'inside';
  const startPx = (barAreaPx * startPct) / 100;
  const spaceRight = barAreaPx - startPx - barPx;
  const spaceLeft = startPx;
  const need = labelPx + WF_LABEL_GAP_PX;
  return spaceRight < need && spaceLeft > spaceRight ? 'left' : 'right';
}

/** CSS `left` for an outside-right label: past the painted edge, not the % edge. */
export function wfOutsideRightLeft(startPct: string, widthPct: string): string {
  return `calc(${startPct}% + max(${widthPct}%, ${2 * WF_BAR_PAD_PX}px) + ${WF_LABEL_GAP_PX}px)`;
}
