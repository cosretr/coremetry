// PodRing — pod sayısı halka içinde (v0.10.1161, operatör: "OpenShift'te
// olduğu gibi daire içinde"). PhaseDonut'un satır / karo boyu kardeşi: merkez
// = toplam pod, halka = faz dağılımı. Palet PhaseDonut ile aynı (v0.10.922
// K5): Running nötr, yalnız sapan faz renkli. Faz bilinmiyorsa (KSM yok)
// halka boş iz olarak kalır — "hepsi sağlıklı" iddia edilmez.
// Kök öğe <span>: StatTile düğmesinin içine de girer (düğmeye div konamaz).

import type { CSSProperties } from 'react';
import { podRingSegments, podRingLabel } from './podRingSegments';

// Clusters S2 — statik stil `.cl-podring*` sınıflarında (globals.css);
// `size`tan hesaplanan ölçü ve yazı boyu stil nesnesi.
const squareBox = (size: number): CSSProperties => ({ width: size, height: size });
const labelSize = (size: number, chars: number): CSSProperties =>
  ({ fontSize: Math.round(size * (chars <= 2 ? 0.38 : 0.3)) });

export function PodRing({ total, running, failing, phaseKnown, size = 30 }: {
  total: number;
  running: number;
  failing: number;
  phaseKnown: boolean;
  size?: number;
}) {
  const stroke = Math.max(3, Math.round(size / 9));
  const r = (size - stroke) / 2;
  const circ = 2 * Math.PI * r;
  const segs = podRingSegments(total, running, failing, phaseKnown);
  const label = podRingLabel(total);
  const tip = phaseKnown ? `${running} / ${total} pod Running` : `${total} pod — faz bilinmiyor`;
  const offsets = segs.map((_, i) => segs.slice(0, i).reduce((a, s) => a + (s.n / total) * circ, 0));
  return (
    <span role="img" aria-label={tip} title={tip}
      className="cl-podring" style={squareBox(size)}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--border)" strokeWidth={stroke} />
        {segs.map((s, i) => {
          const len = (s.n / total) * circ;
          return (
            <circle key={s.key} cx={size / 2} cy={size / 2} r={r} fill="none" stroke={s.color} strokeWidth={stroke}
              strokeDasharray={`${len} ${circ - len}`} strokeDashoffset={-offsets[i]} />
          );
        })}
      </svg>
      <span className="mono cl-podring__label" style={labelSize(size, label.length)}>{label}</span>
    </span>
  );
}
