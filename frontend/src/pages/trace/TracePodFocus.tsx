// TracePodFocus — v0.10.968 — Trace › Metrics pod odak görünümü (`mview=pod`;
// onaylı mockup PodDetail.dc.html, operatör "3 onay" 2026-09-27).
//
// v0.10.968 — Tablo + panel yerine tek pod: araç çubuğu ("← Tüm pod'lar",
// konum, önceki/sonraki İŞARETLİ pod — `[` / `]` kısayolları yalnız bu görünüm
// bağlıyken), solda 280px işaretli pod rayı (trace ilgisi sırası), sağda
// panelin bölümleri iki sütunda + pod'un span zaman çizelgesi + dört grafik
// (Bellek, CPU, JVM heap, GC) + iki sütunlu "Şu an". Panel bölümleri
// TracePodPanel'den paylaşılır (tek kaynak); saf kararlar tracePodPanelModel.ts.
//
// Mockup'taki "huni" çizimi süs — atlandı; cümlesi açıklama metni oldu:
// "Metrik grafiklerinde bu {süre} tek bir {adım} sn'lik adıma düşer."
// (süre adımı aşarsa "… yaklaşık N adıma yayılır" — yanlış iddia yazılmaz).
//
// v0.10.968 — odak yönetimi: görünüm "Genişlet" ile açılınca tablo + panel
// DOM'dan kalkar ve odak <body>'ye düşerdi; bağlanırken odak sahipsizse
// "← Tüm pod'lar"a konur (soğuk derin linkte başka yerdeki odağı çalmaz).
// Panel duyuruları burada da çiplerin altında görünür not olur.
import { useEffect, useMemo, useRef } from 'react';
import { Minimize2, X } from 'lucide-react';
import { Button, IconButton, OptionRow, Tooltip } from '@/components/ui';
import { MiddleEllipsis } from '@/components/ui/DataTable/MiddleEllipsis';
import { useShortcuts } from '@/lib/keyboard';
import type { TracePodInfo, TracePodPanelProps } from './traceMetricsModel';
import { fmtPct } from './traceMetricsFmt';
import { TracePodCharts } from './TracePodCharts';
import { TracePodSpanTimeline } from './TracePodSpanTimeline';
import {
  CompareSection, CopyPodButton, JvmSection, MomentSection, NowSection, PodBadges, PodPageLink, StateBlock, TraceFacts,
} from './TracePodPanel';
import { usePanelNote } from './usePanelNote';
import { flaggedNav, funnelCaption, podSuffix, subLine } from './tracePodPanelModel';

function NavButton({ dir, target, onSelect }: { dir: 'prev' | 'next'; target: TracePodInfo | null; onSelect: (pod: string) => void }) {
  const label = dir === 'prev' ? '‹ Önceki işaretli' : 'Sonraki işaretli ›';
  const key = dir === 'prev' ? '[' : ']';
  const btn = (
    <Button variant="ghost" size="sm" disabled={!target}
      title={target ? undefined : dir === 'prev' ? 'Bu, ilk işaretli pod' : 'Bu, son işaretli pod'}
      onClick={() => target && onSelect(target.pod)}>
      {label} <span className="cell-faint">{key}</span>
    </Button>
  );
  return target ? <Tooltip content={`${podSuffix(target)} · ${target.service}`}>{btn}</Tooltip> : btn;
}

export function TracePodFocus(props: TracePodPanelProps) {
  const {
    model, selected: p, compare, metrics, window: w, siblingLines,
    onCompareChange, onSelect, onClose, onToggleFocus, onToggleSiblingLines, onShowSpans, onOpenSpan, onRetry, announce,
  } = props;
  const nav = useMemo(() => flaggedNav(model, p.pod), [model, p.pod]);
  const prevPod = nav.prev?.pod ?? '';
  const nextPod = nav.next?.pod ?? '';
  useShortcuts([
    { keys: '[', label: 'Önceki işaretli pod', group: 'Trace › Metrics', handler: () => { if (prevPod) onSelect(prevPod); } },
    { keys: ']', label: 'Sonraki işaretli pod', group: 'Trace › Metrics', handler: () => { if (nextPod) onSelect(nextPod); } },
  ], [prevPod, nextPod, onSelect]);

  const state = metrics(p.pod);
  const sub = subLine(p, state);
  const stepSec = (state.kind === 'ok' ? state.data.stepSec : null) ?? w.stepSec;
  const funnel = funnelCaption(w.endNs - w.startNs, stepSec);
  const { note, setNote, say } = usePanelNote(p.pod, announce);
  const backRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const a = document.activeElement;
    if (!a || a === document.body) backRef.current?.focus();
  }, []);

  return (
    <div className="tpp" data-view="pod-focus">
      <div className="tpp-focus-bar">
        <Button ref={backRef} variant="secondary" size="sm" onClick={onToggleFocus}>← Tüm pod'lar <span className="cell-faint">Esc</span></Button>
        <nav aria-label="Konum" className="tpp-sub">
          <span>{p.service}</span><span aria-hidden="true">›</span><span className="mono">{podSuffix(p)}</span>
        </nav>
        <span className="row-grow" />
        <NavButton dir="prev" target={nav.prev} onSelect={onSelect} />
        <NavButton dir="next" target={nav.next} onSelect={onSelect} />
      </div>
      <div className="tpp-focus">
        <aside className="tpp-rail" aria-label="İşaretli pod'lar">
          <div className="tpp-sec-title"><span>İşaretli pod'lar {nav.list.length}</span><span>trace ilgisi sırası</span></div>
          <div role="list">
            {nav.list.map(r => {
              const cur = r.pod === p.pod;
              return (
                <div role="listitem" key={r.pod}>
                  <OptionRow selected={cur} className={cur ? 'tpp-rail-item is-current' : 'tpp-rail-item'} title={r.pod} onClick={() => onSelect(r.pod)}>
                    <span className="mono">…{podSuffix(r)}</span>
                    <span className="cell-muted">{r.service}</span>
                    {r.errors > 0 && <span className="cell-err">⚠{r.errors}</span>}
                    <span className="cell-faint">{fmtPct(r.critShare)}</span>
                  </OptionRow>
                </div>
              );
            })}
          </div>
          <Button variant="ghost" size="sm" onClick={onToggleFocus}>Tüm {model.pods.length} pod'u tabloda göster</Button>
        </aside>
        <section className="tpp" aria-label="Pod odak görünümü">
          <div className="tpp-head">
            <span className="tpp-name"><MiddleEllipsis text={p.pod} /></span>
            <CopyPodButton pod={p.pod} announce={say} />
            <IconButton aria-label="Daralt" tooltip="Tablo + panel görünümüne dön" icon={<Minimize2 size={14} />} onClick={onToggleFocus} />
            <IconButton aria-label="Kapat" tooltip="Kapat (Esc)" icon={<X size={14} />} onClick={onClose} />
          </div>
          <div className="tpp-sub">
            {sub.map((s, i) => <span key={i}>{i > 0 ? '· ' : ''}{s}</span>)}
            {p.replicaSet && <span>· ReplicaSet {p.replicaSet}</span>}
          </div>
          <div className="tpp-actions">
            <PodPageLink p={p} state={state} window={w} />
            <Button variant="ghost" size="sm" onClick={() => onShowSpans(p.pod)}>Span'ları Trace'te göster ({p.spans})</Button>
          </div>
          <PodBadges p={p} />
          <div className="tpp-cols">
            <TraceFacts model={model} p={p} onOpenSpan={onOpenSpan} />
            {state.kind === 'ok'
              ? <MomentSection model={model} p={p} data={state.data} metrics={metrics} />
              : <StateBlock state={state} pod={p.pod} onRetry={onRetry} height={80} />}
          </div>
          {state.kind === 'ok' && (
            <CompareSection model={model} p={p} compare={compare} metrics={metrics} siblingLines={siblingLines}
              onCompareChange={onCompareChange} onToggleSiblingLines={onToggleSiblingLines} announce={say}
              note={note} setNote={setNote} />
          )}
          <TracePodSpanTimeline model={model} pod={p} />
          {funnel && <p className="tpp-caption">{funnel}</p>}
          {state.kind === 'ok' && (
            <TracePodCharts model={model} selected={p} compare={compare} metrics={metrics} window={w}
              siblingLines={siblingLines} height={168} focus />
          )}
          <JvmSection model={model} p={p} compare={compare} window={w} />
          <NowSection state={state} traceStartNs={model.traceStartNs} columns />
        </section>
      </div>
    </div>
  );
}
