import type { RuntimeAlertConfig } from '@/lib/types';

// runtimeThresholds — v0.10.2443 (v0.10.2436'nın bıraktığı ödün: "eşikler
// ayardaki override'ı değil varsayılanı okur"). Runtime kartlarının "eşik
// üstü pod" seçimi operatörün runtime_alerts ayarını okur
// (/api/services/{name}/runtime-signals → runtimeAlerts; sunucu zero-patch'li
// GetRuntimeAlerts döner). Alan yoksa / 0 / sonlu değilse varsayılan —
// chstore.DefaultRuntimeAlerts ile aynı sayılar. SAF.

export const RUNTIME_ALERT_DEFAULTS = {
  gcPauseMs: 2000,  // GCPauseWarnMs
  gcSharePct: 10,   // GCShareWarnPct
  heapRawPct: 92,   // HeapRawWarnPct (ham heap, GC sonrası değil)
} as const;

const pos = (v: number | undefined, def: number) => (v != null && Number.isFinite(v) && v > 0 ? v : def);

export function runtimeThresholds(c?: Partial<RuntimeAlertConfig> | null): { gcPauseMs: number; gcSharePct: number; heapRawPct: number } {
  return {
    gcPauseMs: pos(c?.gcPauseWarnMs, RUNTIME_ALERT_DEFAULTS.gcPauseMs),
    gcSharePct: pos(c?.gcShareWarnPct, RUNTIME_ALERT_DEFAULTS.gcSharePct),
    heapRawPct: pos(c?.heapRawWarnPct, RUNTIME_ALERT_DEFAULTS.heapRawPct),
  };
}
