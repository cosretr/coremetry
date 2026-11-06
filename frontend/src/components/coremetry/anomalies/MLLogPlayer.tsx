// MLLogPlayer — v0.10.1871, öğrenme döngüsü Faz 2 parti B (audit §7.2 "Log:
// yığılmış hacim + baseline gölge çubuk", §17; mockup görünüm 4). Log
// olayının "Olayın seyri": aynı oynatıcı (MLEventPlayer), çizgi yerine yığılı
// çubuklar — varsayılan mesaj kalıbına göre, "Seviye" ikinci seçenek (URL
// `?stack=level`, replace) — + arkada gölge çubuk (bu kovada beklenen üst).
// Veri sunucudan (course.log; ml_series, ES'e gitmez); etiketler sunucudan.
import { useMemo, type CSSProperties } from 'react';
import { useSearchParams } from 'react-router-dom';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { replayChart } from '@/lib/mlReplay';
import type { ExplainPlay, LogCourse, MLSnapshot } from '@/lib/types';
import { MLEventPlayer } from './MLEventPlayer';
import { LOG_SHADOW_COLOR, logCourseSeries, logStackColor, resolveLogStackView, type LogStackView } from './mlLogCourse';

/** SAF (test): sunucu yığını kaydın zaman ekseniyle birebir mi (değilse çizgi grafiğe düşülür). */
export function logCourseAligned(lc: LogCourse, times: readonly number[]): boolean {
  return lc.times.length === times.length && lc.times.every((t, i) => t === times[i]);
}

export function MLLogPlayer({ snap, play, autoPlay, lc }: { snap: MLSnapshot; play?: ExplainPlay | null; autoPlay?: boolean; lc: LogCourse }) {
  const [sp, setSp] = useSearchParams();
  const view = resolveLogStackView(sp.get('stack'), lc);
  const times = useMemo(() => replayChart(snap).times, [snap]);
  const aligned = logCourseAligned(lc, times);
  const series = useMemo(() => (aligned ? logCourseSeries(lc, view) : null), [aligned, lc, view]);
  const stacks = view === 'level' ? lc.byLevel ?? [] : lc.byPattern;
  const setView = (v: LogStackView) => setSp(prev => {
    const n = new URLSearchParams(prev);
    if (v === 'level') n.set('stack', 'level'); else n.delete('stack');
    return n;
  }, { replace: true });
  return (
    <div data-testid="ml-log-player">
      {aligned && (
        <div className="mb-1.5 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[11px] text-muted-foreground" data-testid="ml-log-legend">
          {(lc.byLevel?.length ?? 0) > 0 && (
            <SegmentedControl<LogStackView> size="sm" value={view} onChange={setView} aria-label="Yığın türü"
              options={[{ value: 'pattern', label: 'Mesaj kalıbı' }, { value: 'level', label: 'Seviye' }]} />
          )}
          {stacks.map((s, i) => (
            <span key={s.key} className="inline-flex max-w-[320px] items-center gap-1" title={s.label}>
              <i aria-hidden="true" className="inline-block h-2 w-3 shrink-0 rounded-[2px]" style={swatch(logStackColor(s, i))} />
              <span className="truncate">{s.label}</span>{s.new && <span className="shrink-0 font-medium text-[color:var(--err)]">(yeni)</span>}
            </span>
          ))}
          {lc.shadow && (
            <span className="inline-flex items-center gap-1">
              <i aria-hidden="true" className="inline-block h-2 w-3 rounded-[2px] opacity-40" style={swatch(LOG_SHADOW_COLOR)} />gölge çubuk: bu kovada beklenen
            </span>
          )}
        </div>
      )}
      <MLEventPlayer snap={snap} play={play} autoPlay={autoPlay} series={series} />
    </div>
  );
}

/** Lejant karesinin rengi (seri rengiyle aynı token; sınıf sabit, renk hesaplanan). */
function swatch(color: string): CSSProperties {
  return { background: color };
}
