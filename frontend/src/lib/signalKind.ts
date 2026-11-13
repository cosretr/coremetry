// signalKind.ts — v0.10.1844, öğrenme döngüsü Faz 2 dilim 2.4 (audit
// docs/anomaly/learning-loop-audit.md §7.2, §17; mockup anomaly-detail.html).
//
// Sinyal türü kimliği TEK yerde: tür → etiket + ikon + renk token'ı, ve
// dedektör → tür eşlemesi. Liste, detay başlığı (ve sonraki partide CoSRE
// kartı) buradan okur. Renk hiçbir yerde tek taşıyıcı değildir: rozet her
// zaman ikon + metin çizer (WCAG 1.4.1). Token'lar üç temada
// `styles/globals.css` (`--sig-*`), kontrast `contrastTokens.test.ts`.
import { Layers, ListTree, ScrollText, TrendingUp, type LucideIcon } from 'lucide-react';

export type SignalKind = 'metric' | 'trace' | 'log' | 'group';

export interface SignalKindSpec {
  label: string;
  /** CSS değişkeni — renk yalnız buradan (Tailwind `text-[color:var(…)]`). */
  token: string;
  icon: LucideIcon;
}

export const SIGNAL_KINDS: Record<SignalKind, SignalKindSpec> = {
  metric: { label: 'Metrik', token: 'var(--sig-metric)', icon: TrendingUp },
  trace: { label: 'Trace', token: 'var(--sig-trace)', icon: ListTree },
  log: { label: 'Log', token: 'var(--sig-log)', icon: ScrollText },
  group: { label: 'Grup', token: 'var(--text2)', icon: Layers },
};

// Dedektör → tür. Sunucu sözlüğünün (chstore.MLLogDetectorNames /
// MLTraceDetectorNames / MLMetricDetectorNames) aynası; metric_* öneki
// ailedir. Bilinmeyen dedektör sunucunun `signal` alanına düşer.
const TRACE_DETECTORS = new Set(['op_latency', 'op_error_rate', 'throughput_drop', 'dependency_latency', 'trace_pod_outlier', 'trace_change_point', 'trace_error_count', 'dependency_error_rate', 'svc_error_rate']);
const LOG_DETECTORS = new Set(['log_volume', 'error_rate', 'service_silent', 'template_spike', 'rare_template', 'rare_error_code', 'pod_outlier', 'change_point', 'new_template']);

/** SAF: dedektör (+ sunucunun signal alanı) → sinyal türü. */
export function signalKindOf(detector: string, signal?: string): SignalKind {
  if (detector.startsWith('metric_')) return 'metric';
  if (TRACE_DETECTORS.has(detector)) return 'trace';
  if (LOG_DETECTORS.has(detector)) return 'log';
  return signal === 'metric' || signal === 'trace' ? signal : 'log';
}
