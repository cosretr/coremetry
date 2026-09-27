// resolveVar — bir `var(--token)` CSS değişkenini canvas stroke/fill için
// somut hex/rgb'ye çözer. uPlot 2D canvas'a çizer ve CSS var'larını
// doğrudan okuyamaz, o yüzden token'lar draw zamanında çözülmeli.
//
// v0.9.75 (chart-consolidation Adım 0) — dört chart bileşeninde
// birbirinin AYNISI olan çözümleyicinin TEK kopyası:
//   OverviewChart.cssVar + TimeChart.cssVar (byte-identical) +
//   TimeSeriesPanel.resolveColor (aynı regex, aynı fallback).
// Token değilse (ham renk) olduğu gibi geçer; çözülemezse girdi döner.
export function resolveVar(c: string): string {
  const m = /^var\((--[\w-]+)\)$/.exec(c.trim());
  if (!m) return c;
  return getComputedStyle(document.documentElement).getPropertyValue(m[1]).trim() || c;
}

// chartMonoFont — canvas `font` dizgisi, aile TEK yığından (`--font-mono`,
// globals.css). v0.10.980 (tablo standardı T5 artığı): uPlot eksen `font`u
// ve ctx.font `var(--font-mono)`yu çözmez; eksenler kendi `ui-monospace,
// monospace` kopyasını taşıyordu (inlineMonoStack 4 → 0; eşik/bölge
// katmanı ve LatencyHeatmap ctx.font'u da aynı kopyaydı). Build/draw anında
// çağrılır — tema flip'te motor yeniden kurar, aile tazelenir. Token
// çözülemezse (test ortamı, eksik stil) genel `monospace`: geçersiz bir
// canvas fontu sessizce 10px sans-serif'e düşerdi. DOM yoksa (node
// ortamındaki çizim çekirdeği testleri) aynı yedek.
export function chartMonoFont(px: number): string {
  if (typeof document === 'undefined') return `${px}px monospace`;
  const fam = resolveVar('var(--font-mono)');
  return `${px}px ${fam.startsWith('var(') ? 'monospace' : fam}`;
}
