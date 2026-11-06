// mlLogCourse — v0.10.1871, öğrenme döngüsü Faz 2 parti B (audit §7.2 "Log:
// yığılmış hacim + baseline gölge çubuk", §17 "varsayılan mesaj kalıbına
// göre yığılır; Seviye ikinci seçenek"; mockup görünüm 4). Sunucunun
// course.log'u (ml.BuildLogCourse) → TimeChart serileri. SAF; etiketler
// sunucudan (FE cümle kurmaz).
import type { TimeChartSeries } from '@/components/charts/TimeChart';
import type { LogCourse, LogStack } from '@/lib/types';

export type LogStackView = 'pattern' | 'level';

/** SAF: ?stack= çözümü; Seviye yalnız sunucu verdiyse. */
export function resolveLogStackView(raw: string | null, lc: LogCourse): LogStackView {
  return raw === 'level' && (lc.byLevel?.length ?? 0) > 0 ? 'level' : 'pattern';
}

/** Yığın renkleri (mockup .ta/.tb/.tc): ilk iki ad dolgu tonları, kalan nötr. */
const NAMED = ['var(--err-solid)', 'var(--warn-solid)'];
export const LOG_REST_COLOR = 'var(--border-strong)';
export const LOG_SHADOW_COLOR = 'var(--text3)';

export function logStackColor(s: LogStack, i: number): string {
  if (s.key.startsWith('_') && s.key !== '_error') return LOG_REST_COLOR;
  return NAMED[i] ?? LOG_REST_COLOR;
}

/** SAF: görünüm → seriler (gölge önce = arkada; yığın ilk = en alt). */
export function logCourseSeries(lc: LogCourse, view: LogStackView): TimeChartSeries[] {
  const stacks = view === 'level' ? lc.byLevel ?? [] : lc.byPattern;
  const out: TimeChartSeries[] = [];
  if (lc.shadow) out.push({ key: '_shadow', label: 'beklenen', data: lc.shadow, color: LOG_SHADOW_COLOR, type: 'bar', faint: true });
  stacks.forEach((s, i) => out.push({ key: s.key, label: s.label, data: s.values, color: logStackColor(s, i), type: 'bar', stack: 'log' }));
  return out;
}
