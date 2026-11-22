// traceCritView — v0.10.2459 (operatör 2026-11-22: "çubuklarda siyah
// şeritler istemiyorum"). Trace şelalesinin kritik yol boyası YALNIZ
// "Kritik yol" çipi açıkken verilir: kapalıyken çubuklar düz (sol şerit,
// kesit gölgesi, 0.62 soluklaştırma, "yalnız kritik" süzgeci yok).
// v0.10.2411'in "hep açık" varsayılanını geri alır.
import type { CriticalSegments } from '@/lib/criticalSegments';

export interface TraceCritView {
  criticalPathIds?: Set<string>;
  criticalSegments?: CriticalSegments;
  onlyIds?: Set<string>;
}

export function traceCritView(show: boolean, critSet: Set<string> | undefined,
  segments: CriticalSegments | undefined, critOnly: boolean): TraceCritView {
  if (!show) return {};
  return {
    criticalPathIds: critSet,
    criticalSegments: segments,
    onlyIds: critOnly && segments ? critSet : undefined,
  };
}
