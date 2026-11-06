// MLEventPlayer — v0.10.1846, öğrenme döngüsü Faz 2 (audit §15: replay =
// olayın kendi "oynat" gösterimi, rapor DEĞİL; §17 kararları; mockup
// anomaly-detail.html `.pc` denetimi). Olay anı kaydının grafiği + altında
// ▶/⏸, adım düğmeleri (ilk sapma · tespit · tepe · sürüyor | bitiş), zaman
// kaydırıcısı ve canlı cümle (aria-live polite). Durağan hâlde imleç son
// noktada ("şimdi / bitiş"): sayfa oynatmadan da eksiksiz okunur. Oynatma
// ilk sapmadan 6 kova önce başlar, kova başına ~0,25 sn, sonda durur (döngü
// yok); hareket azaltma açıksa adımlar arası animasyonsuz atlar; sekme
// gizlenince duraklar. Cümleler sunucuda (card.play.frames) — burada seçilir.
// Zamanlayıcı setTimeout zinciri (yoklama değil; ağ isteği yok).
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Pause, Play } from 'lucide-react';
import { TimeChart, type TimeChartSeries } from '@/components/charts/TimeChart';
import { Button } from '@/components/shadcn/button';
import { Slider } from '@/components/shadcn/slider';
import { replayChart } from '@/lib/mlReplay';
import {
  activeStep, frameAt, nextIndex, nextReduced, playStart, reducedSequence, stepIndices, PLAY_REDUCED_STEP_MS, PLAY_TICK_MS,
} from '@/lib/mlPlay';
import type { ExplainPlay, MLSnapshot } from '@/lib/types';

function prefersReducedMotion(): boolean {
  try {
    return !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

// above (v0.10.1870): ana grafiğin ÜSTÜNE çizilen sinyal katmanı (trace ısı
// haritası); imleç zamanını (unix sn) alır — oynatma iki görselde aynı anda.
// series (v0.10.1871): ana grafiğin serilerinin YERİNE (log yığılı çubukları);
// kayıt zamanlarıyla aynı uzunlukta değilse yok sayılır. Bölge, yayın, imleç
// aynen; lejant çağıranın (grafiğin üstünde).
export function MLEventPlayer({ snap, play, autoPlay = false, above, series }: {
  snap: MLSnapshot; play?: ExplainPlay | null; autoPlay?: boolean; above?: (cursorSec: number | null) => ReactNode;
  series?: TimeChartSeries[] | null;
}) {
  const ch = useMemo(() => replayChart(snap), [snap]);
  const times = ch.times;
  const override = series && series.length > 0 && series.every(x => x.data.length === times.length) ? series : null;
  const last = Math.max(0, times.length - 1);
  const [i, setI] = useState(last);
  const [playing, setPlaying] = useState(false);
  const reducedRef = useRef(false);
  const stepIdx = useMemo(() => (play ? stepIndices(times, play.steps) : []), [times, play]);
  const seq = useMemo(() => reducedSequence(stepIdx), [stepIdx]);
  const canPlay = !!play && play.frames.length > 0 && times.length > 1;

  const start = () => {
    reducedRef.current = prefersReducedMotion();
    if (reducedRef.current) setI(seq[0] ?? last);
    else if (i >= last) setI(playStart(times, play?.steps ?? []));
    setPlaying(true);
  };
  const toggle = () => { if (playing) setPlaying(false); else start(); };

  // ?play=1 (listedeki ▶): veri gelince bir kez başlar.
  const autoDone = useRef(false);
  useEffect(() => {
    if (autoPlay && canPlay && !autoDone.current) { autoDone.current = true; start(); }
    // start bilinçli dep dışı: yalnız ilk uygun çizimde bir kez
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoPlay, canPlay]);

  useEffect(() => {
    if (!playing) return;
    const id = window.setTimeout(() => {
      if (document.hidden) { setPlaying(false); return; }
      const n = reducedRef.current ? nextReduced(seq, i) : nextIndex(i, times.length);
      if (n == null) setPlaying(false); else setI(n);
    }, reducedRef.current ? PLAY_REDUCED_STEP_MS : PLAY_TICK_MS);
    return () => window.clearTimeout(id);
  }, [playing, i, seq, times.length]);

  useEffect(() => {
    const onVis = () => { if (document.hidden) setPlaying(false); };
    document.addEventListener('visibilitychange', onVis);
    return () => document.removeEventListener('visibilitychange', onVis);
  }, []);

  const frame = play ? frameAt(times, play.frames, i) : null;
  const act = activeStep(stepIdx, i);
  const cursorSec = times[i];
  return (
    <div data-testid="ml-player">
      {above?.(cursorSec ?? null)}
      {times.length > 0 ? (
        <TimeChart height={220} times={times} deployMarkers={ch.deploys}
          regions={ch.regions.map(r => ({ ...r, color: r.label === 'anomali' ? 'var(--err)' : 'var(--warn)' }))}
          playhead={canPlay && cursorSec != null ? { cursorSec, fromSec: play?.from } : null}
          hideLegend={!!override} leftInteger={!!override}
          series={override ?? [
            { key: 'v', label: snap.part || 'değer', data: ch.value, color: 'var(--accent)', type: 'line', width: 2 },
            { key: 'hi', label: ch.banded ? 'beklenen aralık üst' : 'alarm sınırı üst', data: ch.upper, color: 'var(--text3)', type: 'line', width: 1 },
            { key: 'lo', label: ch.banded ? 'beklenen aralık alt' : 'alarm sınırı alt', data: ch.lower, color: 'var(--text3)', type: 'line', width: 1 },
            ...(ch.banded ? [{ key: 'alarm', label: 'alarm sınırı', data: ch.alarm, color: 'var(--warn)', type: 'line' as const, width: 1 }] : []),
          ]} />
      ) : <p className="text-sm text-muted-foreground">{snap.part ? 'Seri boş (ml_series bu pencereyi taşımıyor).' : 'Bu dedektör seri tutmaz (kesitsel karar); kanıt aşağıda.'}</p>}
      {canPlay && play && (
        <div className="mt-2 flex flex-wrap items-center gap-x-2.5 gap-y-2 border-t border-[color:var(--divider)] pt-2" data-testid="ml-play-controls">
          <Button size="sm" onClick={toggle} aria-label={playing ? 'Oynatmayı duraklat' : 'Olayı oynat'} data-testid="ml-play-toggle">
            {playing ? <Pause aria-hidden="true" /> : <Play aria-hidden="true" />}{playing ? 'Duraklat' : 'Oynat'}
          </Button>
          <div className="inline-flex flex-wrap gap-1" role="group" aria-label="Olay adımları">
            {play.steps.map((s, j) => (
              <Button key={s.key} size="sm" variant="outline" aria-pressed={j === act} data-testid={`ml-step-${s.key}`}
                className={j === act ? 'border-[color:var(--accent)] bg-[color:var(--accent-bg)] text-[color:var(--accent2)]' : ''}
                onClick={() => { setPlaying(false); setI(stepIdx[j]); }}>{s.label}</Button>
            ))}
          </div>
          <Slider className="min-w-[120px] flex-[1_1_160px]" min={0} max={last} step={1} value={[i]} thumbLabel="Zaman çizgisinde gez"
            onValueChange={v => { setPlaying(false); setI(v[0] ?? last); }} />
          <p className="m-0 basis-full text-sm tabular-nums" aria-live="polite" data-testid="ml-play-say">
            {frame && <><b>{frame.clock}</b> — {frame.text}</>}
          </p>
        </div>
      )}
    </div>
  );
}
