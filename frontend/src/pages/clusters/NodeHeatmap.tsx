import type { CSSProperties } from 'react';
import { MiniBar } from './MiniBar';
import type { ClusterNodeRow } from '@/lib/types';

// NodeHeatmap — node kullanım ısı-ızgarası (v0.9.32, design handoff
// "Node utilization heatmap"). Full-width, auto-fill minmax(150px).
// Her tile: role-dot (master --purple, worker --teal; role yoksa
// nötr — B4'te doldurulacak) + node adı (mono 11) + CPU/Mem
// mini-bar. clusterNodes verisini (mevcut) tüketir.
// Clusters S2 — statik stil `.cl-heat*` sınıflarında (globals.css); rol
// noktasının rengi hesaplanan değer.
const dotFill = (color: string): CSSProperties => ({ background: color });

export function NodeHeatmap({ nodes }: { nodes: ClusterNodeRow[] }) {
  if (nodes.length === 0) return null;
  return (
    <div className="cl-heat">
      {nodes.map(n => {
        const roleColor = n.role === 'master' || n.role === 'control-plane'
          ? 'var(--purple)'
          : n.role === 'worker'
            ? 'var(--teal)'
            : 'var(--text3)';
        return (
          <div key={n.node} className="cl-heat__tile">
            <div className="cl-heat__head">
              <span className="cl-heat__dot" style={dotFill(roleColor)}
                title={n.role || 'node'} />
              <span className="cl-heat__name" title={n.node}>{n.node}</span>
            </div>
            <div className="cl-heat__bars">
              <div className="cl-heat__bar">
                <span>CPU</span>
                <MiniBar pct={n.cpuPct ?? null} />
              </div>
              <div className="cl-heat__bar">
                <span>Mem</span>
                <MiniBar pct={n.memPct ?? null} />
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
