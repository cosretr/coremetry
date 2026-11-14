import type { CSSProperties } from 'react';
import { pctColor } from './thresholds';

// Gauge — radial kullanım göstergesi (v0.9.31, design handoff
// "Utilization card / 3 radial gauges"). Prototype ölçüleri: size
// 110, stroke 9, track var(--border), arc pctColor eşiğiyle,
// stroke-linecap round, -90° döndürülmüş, dashoffset .6s geçiş.
// Renk override edilebilir (pod-health gauge'u pctColor değil kendi
// eşiğini kullanır). pct null → "—" (veri yok, gauge boş track).
// Clusters S2 — statik stil `.cl-gauge*` / `.cl-dial*` sınıflarında
// (globals.css); yalnız `size`tan hesaplanan kutu ölçüsü stil nesnesi.
const squareBox = (size: number): CSSProperties => ({ width: size, height: size });

export function Gauge({ pct, label, sub, color, size = 110, stroke = 9 }: {
  pct: number | null;
  label: string;
  sub?: string;
  color?: string;
  size?: number;
  stroke?: number;
}) {
  const r = (size - stroke) / 2;
  const circ = 2 * Math.PI * r;
  const shown = pct == null ? 0 : pct;
  const offset = circ * (1 - shown / 100);
  const arcColor = color ?? pctColor(shown);
  return (
    <div className="cl-gauge">
      <div className="cl-dial" style={squareBox(size)}>
        <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
          <circle cx={size / 2} cy={size / 2} r={r} fill="none"
            stroke="var(--border)" strokeWidth={stroke} />
          {pct != null && (
            <circle cx={size / 2} cy={size / 2} r={r} fill="none"
              stroke={arcColor} strokeWidth={stroke} strokeLinecap="round"
              strokeDasharray={circ} strokeDashoffset={offset}
              className="cl-dial__arc--offset" />
          )}
        </svg>
        <div className="cl-dial__center cl-gauge__val">
          {pct == null ? '—' : `${Math.round(pct)}%`}
        </div>
      </div>
      <div className="cl-gauge__label">{label}</div>
      {sub && <div className="cl-gauge__sub">{sub}</div>}
    </div>
  );
}
