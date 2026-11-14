// PhaseDonut — pod fazı halkası (v0.9.31, design handoff "Pod phase
// card"). Üç yay (Running --ok, Pending --warn, Failed --err),
// stroke 13, yuvarlak uçlar + küçük boşluklar, merkez = toplam.
// Toplam 0 → "—" (veri yok). Yaylar SVG dasharray ile; -90°'den
// saat yönünde dizilir.
// Clusters S2 — statik stil `.cl-donut*` / `.cl-dial*` sınıflarında
// (globals.css); `size` kutusu ve dilim rengi hesaplanan değer.
import type { CSSProperties } from 'react';

const squareBox = (size: number): CSSProperties => ({ width: size, height: size });
const swatch = (color: string): CSSProperties => ({ background: color });

const SEGMENTS = [
  { key: 'running', label: 'Running', color: 'var(--text3)' }, // v0.10.922 (K5) — normal faz nötr
  { key: 'pending', label: 'Pending', color: 'var(--warn)' },
  { key: 'failed', label: 'Failed', color: 'var(--err)' },
] as const;

export function PhaseDonut({ running, pending, failed, size = 130, stroke = 13 }: {
  running: number;
  pending: number;
  failed: number;
  size?: number;
  stroke?: number;
}) {
  const counts = { running, pending, failed };
  const total = running + pending + failed;
  const r = (size - stroke) / 2;
  const circ = 2 * Math.PI * r;
  const gap = total > 1 ? 2 : 0; // segmentler arası küçük boşluk (px)

  let acc = 0;
  const arcs = SEGMENTS.map(seg => {
    const v = counts[seg.key];
    const frac = total > 0 ? v / total : 0;
    const len = Math.max(0, frac * circ - gap);
    const dash = `${len} ${circ - len}`;
    const offset = -acc; // dönüş yönü
    acc += frac * circ;
    return { seg, v, dash, offset, show: v > 0 };
  });

  return (
    <div className="cl-donut">
      <div className="cl-dial cl-donut__dial" style={squareBox(size)}>
        <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
          <circle cx={size / 2} cy={size / 2} r={r} fill="none"
            stroke="var(--border)" strokeWidth={stroke} />
          {total > 0 && arcs.filter(a => a.show).map(a => (
            <circle key={a.seg.key} cx={size / 2} cy={size / 2} r={r} fill="none"
              stroke={a.seg.color} strokeWidth={stroke} strokeLinecap="round"
              strokeDasharray={a.dash} strokeDashoffset={a.offset}
              className="cl-dial__arc--array" />
          ))}
        </svg>
        <div className="cl-dial__center cl-dial__center--stack">
          <span className="cl-donut__total">
            {total > 0 ? total : '—'}
          </span>
          <span className="cl-donut__unit">pods</span>
        </div>
      </div>
      <div className="cl-donut__legend">
        {SEGMENTS.map(seg => (
          <div key={seg.key} className="cl-donut__row">
            <span className="cl-donut__swatch" style={swatch(seg.color)} />
            <span className="cl-donut__name">{seg.label}</span>
            <span className="mono cl-donut__n">{counts[seg.key]}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
