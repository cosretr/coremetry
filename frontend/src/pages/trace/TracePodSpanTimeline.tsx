// TracePodSpanTimeline — v0.10.968 — pod odak görünümünde "Bu pod'un span'ları"
// şeridi (mockup PodDetail.dc.html). Grafik kütüphanesi DEĞİL, düz SVG: bir
// zaman çizelgesi (Gantt şeridi), seri çizimi değil — uPlot kuralının
// kapsamı dışında (uPlot = zaman serisi grafikleri).
//
// v0.10.968 — Düzen SAF çekirdekten (tracePodPanelModel.timelineModel): span'ler
// açgözlü şerit paketlemesiyle (en az şerit, deterministik), hatalı span
// var(--err-solid), kritik yoldaki span altında 2px çizgi, etkin aralık
// kesikli dikdörtgen, eksen düzgün adımlarla (0 → trace süresi). Üst sırada
// TRACE'in tüm hataları ▼: bu pod'unkiler var(--err), diğerleri var(--text3).
// İpuçları `<title>` (span: "op · süre · durum · t0 → t1"; işaret:
// "<svc …ek> · op · durum · +1,802 sn"). Erişilebilirlik: role="img" +
// özet aria-label.
import { useMemo } from 'react';
import type { TraceMetricsModel, TracePodInfo } from './traceMetricsModel';
import { fmtClockNs, fmtDurNs } from './traceMetricsFmt';
import { timelineModel } from './tracePodPanelModel';

const W = 836;
const TOP = 18;     // üst sıra: hata işaretleri
const LANE_H = 14;
const BAR_H = 4;
const MAX_LANES = 10;

function Glyph({ kind }: { kind: 'bar' | 'err' | 'crit' | 'active' | 'own' | 'other' }) {
  switch (kind) {
    case 'bar': return <svg width="14" height="6" aria-hidden="true"><rect y="1" width="14" height="4" rx="1" fill="var(--border-strong)" /></svg>;
    case 'err': return <svg width="14" height="6" aria-hidden="true"><rect y="1" width="14" height="4" rx="1" fill="var(--err-solid)" /></svg>;
    case 'crit': return <svg width="14" height="6" aria-hidden="true"><rect y="2" width="14" height="2" fill="var(--text)" /></svg>;
    case 'active': return <svg width="6" height="12" aria-hidden="true"><line x1="3" x2="3" y1="0" y2="12" stroke="var(--border-strong)" strokeDasharray="3 2" /></svg>;
    case 'own': return <svg width="8" height="7" aria-hidden="true"><path d="M0 0.5h8L4 6.5z" fill="var(--err)" /></svg>;
    case 'other': return <svg width="8" height="7" aria-hidden="true"><path d="M0 0.5h8L4 6.5z" fill="var(--text3)" /></svg>;
  }
}

export function TracePodSpanTimeline({ model, pod }: { model: TraceMetricsModel; pod: TracePodInfo }) {
  const tl = useMemo(() => timelineModel(model, pod, MAX_LANES), [model, pod]);
  const lanesH = tl.lanes * LANE_H + 4;
  const critY = TOP + lanesH + 2;
  const axisY = critY + 6;
  const H = axisY + 16;
  const X = (f: number) => Math.round(f * W * 10) / 10;
  const ax0 = X(tl.active.x0);
  const label = `Bu pod'un span'ları: ${tl.summary}; etkin aralık ${fmtClockNs(pod.activeFromNs)} → ${fmtClockNs(pod.activeToNs)}; trace'teki hatalar: ${tl.marks.length}`;
  return (
    <section className="tpp-sec" aria-label="Bu pod'un span'ları">
      <div className="tpp-sec-title">
        <span>Bu pod'un span'ları · trace içinde (0 → {fmtDurNs(tl.durNs)})</span>
        <span>{tl.summary}</span>
      </div>
      <div className="tpp-tl">
        <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label}>
          <text x={0} y={10} fontSize={11} fill="var(--text3)">Trace'teki hatalar ({tl.marks.length})</text>
          {tl.marks.map(m => {
            const x = X(m.x);
            return (
              <path key={`m:${m.spanId}`} d={`M${x - 4} 3h8l-4 6z`} fill={m.own ? 'var(--err)' : 'var(--text3)'} data-own={m.own ? '1' : '0'}>
                <title>{m.title}</title>
              </path>
            );
          })}
          <rect x={0} y={TOP} width={W} height={lanesH} rx={4} fill="var(--bg2)" />
          <rect x={ax0} y={TOP + 0.5} width={Math.max(1, X(tl.active.x1) - ax0)} height={lanesH - 1}
            fill="none" stroke="var(--border-strong)" strokeDasharray="4 3" />
          {tl.bars.map(b => {
            const x = X(b.x0);
            return (
              <rect key={`b:${b.id}`} x={x} y={TOP + 2 + b.lane * LANE_H + (LANE_H - BAR_H) / 2} width={Math.max(1.2, X(b.x1) - x)} height={BAR_H} rx={1}
                fill={b.error ? 'var(--err-solid)' : 'var(--border-strong)'} data-err={b.error ? '1' : '0'}>
                <title>{`${b.name} · ${fmtDurNs(b.endNs - b.startNs)} · ${b.status} · ${fmtClockNs(b.startNs)} → ${fmtClockNs(b.endNs)}`}</title>
              </rect>
            );
          })}
          {tl.bars.filter(b => b.critical).map(b => {
            const x = X(b.x0);
            return <rect key={`k:${b.id}`} x={x} y={critY} width={Math.max(1.2, X(b.x1) - x)} height={2} fill="var(--text)" data-crit="1" />;
          })}
          {tl.ticks.map((t, i) => {
            const x = X(t.x);
            const anchor = i === 0 ? 'start' : t.end ? 'end' : 'middle';
            return (
              <g key={`t:${i}`}>
                <line x1={x} x2={x} y1={axisY - 3} y2={axisY} stroke="var(--border-strong)" />
                <text x={x} y={axisY + 12} fontSize={11} textAnchor={anchor} fill="var(--text3)">{t.label}</text>
              </g>
            );
          })}
        </svg>
      </div>
      {tl.overflow > 0 && (
        <div className="tpp-note">{tl.overflow} span üst üste çizildi (en çok {MAX_LANES} şerit).</div>
      )}
      <div className="tpp-tl-legend">
        <span><Glyph kind="bar" /> span</span>
        <span><Glyph kind="err" /> hatalı span</span>
        <span><Glyph kind="crit" /> kritik yolda</span>
        <span><Glyph kind="active" /> etkin aralık</span>
        <span><Glyph kind="own" /> bu pod'un hatası</span>
        <span><Glyph kind="other" /> trace'teki diğer hatalar</span>
      </div>
    </section>
  );
}
