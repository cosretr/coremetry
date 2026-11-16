// ProblemGraphDiagram — v0.10.2279 (entity grafı ile Problem birleştirme S2,
// docs/problems/entity-graph-merge-design.md §4): "Neden tek Problem"
// bölümündeki küçük bağımlılık yolu çizimi. Satır içi SVG; yerleşim SAF
// (lib/problemGraph graphDiagramLayout, en çok 25 düğüm): köken sağda
// vurgulu, etkilenen üyeler ortada (sayı = başlangıç sırası), sapması
// olmayan çağıranlar solda soluk ve kesikli. Renkler tema token'larından
// (her iki temada okunur); dar ekranda kendi kutusunda yatay kayar.
// ProblemGraphSection tembel yükler (ilk paket baytı yok).
import { useId } from 'react';
import { DIA, graphDiagramLayout, graphTypeLabel, type DiaNode, type GraphT, type ProblemGraphMember, type ProblemGraphOrigin } from '@/lib/problemGraph';

const NODE_STYLE: Record<DiaNode['kind'], { fill: string; stroke: string; dash?: string; sw: number }> = {
  origin: { fill: 'var(--err-bg)', stroke: 'var(--err)', sw: 1.5 },
  member: { fill: 'var(--bg1)', stroke: 'var(--border-control)', sw: 1 },
  calm: { fill: 'var(--bg2)', stroke: 'var(--border)', dash: '3 3', sw: 1 },
};

function short(s: string, n = 22) {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

export default function ProblemGraphDiagram({ members, origin, t, clock }: {
  members: ProblemGraphMember[]; origin?: ProblemGraphOrigin; t: GraphT; clock: (ns: number) => string;
}) {
  const uid = useId().replace(/:/g, '');
  const lay = graphDiagramLayout(members, origin);
  if (lay.nodes.length < 2) return null;
  const at = new Map(lay.nodes.map(n => [n.name, n]));
  const mk = `pg-arrow-${uid}`;
  const mkCalm = `pg-arrow-calm-${uid}`;
  return (
    <div className="flex flex-col gap-1" data-testid="pg-diagram">
      <div className="min-w-0 overflow-x-auto">
        <svg role="img" aria-label={t('pg.dia.aria')} viewBox={`0 0 ${lay.width} ${lay.height}`}
          width={lay.width} height={lay.height} style={{ display: 'block', maxWidth: 'none' }}>
          <defs>
            <marker id={mk} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L8,4 L0,8 z" style={{ fill: 'var(--text2)' }} />
            </marker>
            <marker id={mkCalm} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L8,4 L0,8 z" style={{ fill: 'var(--border-control)' }} />
            </marker>
          </defs>
          {lay.edges.map(e => {
            const a = at.get(e.from)!, b = at.get(e.to)!;
            let d: string;
            if (a.x < b.x) {
              const x1 = a.x + DIA.nodeW, y1 = a.y + DIA.nodeH / 2, x2 = b.x, y2 = b.y + DIA.nodeH / 2;
              const mx = (x1 + x2) / 2;
              d = `M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2},${y2}`;
            } else {
              // aynı sütun (iş yükü / node / host bağı ya da sütun içi çağrı): solda yay
              const x1 = a.x, y1 = a.y + DIA.nodeH / 2, x2 = b.x, y2 = b.y + DIA.nodeH / 2;
              const bend = Math.min(a.x, b.x) - 14;
              d = `M${x1},${y1} C${bend},${y1} ${bend},${y2} ${x2},${y2}`;
            }
            const calm = e.kind === 'calm';
            const style = calm
              ? { stroke: 'var(--border-control)', strokeWidth: 1, fill: 'none' }
              : e.kind === 'link'
                ? { stroke: 'var(--text2)', strokeWidth: 1.25, strokeDasharray: '4 3', fill: 'none' }
                : { stroke: 'var(--text2)', strokeWidth: 1.5, fill: 'none' };
            const lx = (a.x < b.x ? (a.x + DIA.nodeW + b.x) / 2 : Math.min(a.x, b.x) - 12);
            const ly = (a.y + b.y) / 2 + DIA.nodeH / 2 - 4;
            return (
              <g key={`${e.from}|${e.to}|${e.kind}`} data-testid={`pg-edge-${e.kind}`}>
                <path d={d} style={style} markerEnd={e.kind === 'link' ? undefined : `url(#${calm ? mkCalm : mk})`} />
                {e.label && (
                  <text x={lx} y={ly} textAnchor="middle"
                    style={{ font: '500 10px var(--font-mono)', fill: 'var(--text2)', paintOrder: 'stroke', stroke: 'var(--bg1)', strokeWidth: 3 }}>
                    {short(e.label, 18)}
                  </text>
                )}
              </g>
            );
          })}
          {lay.nodes.map(n => {
            const st = NODE_STYLE[n.kind];
            const calm = n.kind === 'calm';
            const meta = n.kind === 'origin'
              ? t('pg.dia.origin') + (n.onset ? ` · ${clock(n.onset)}` : '')
              : calm ? t('pg.dia.calm') : `${graphTypeLabel(n.type, t)}${n.onset ? ` · ${clock(n.onset)}` : ''}`;
            return (
              <g key={n.name} data-testid={`pg-node-${n.kind}`}>
                <title>{n.name}</title>
                <rect x={n.x} y={n.y} width={DIA.nodeW} height={DIA.nodeH} rx={6}
                  style={{ fill: st.fill, stroke: st.stroke, strokeWidth: st.sw, strokeDasharray: st.dash }} />
                <text x={n.x + 10} y={n.y + 16} style={{ font: '500 12px var(--font-mono)', fill: calm ? 'var(--text3)' : 'var(--text)' }}>
                  {short(n.name, n.order ? 17 : 20)}
                </text>
                <text x={n.x + 10} y={n.y + 31}
                  style={{ font: `${n.kind === 'origin' ? 600 : 400} 10.5px var(--font-sans)`, fill: n.kind === 'origin' ? 'var(--err)' : calm ? 'var(--text3)' : 'var(--text2)' }}>
                  {short(meta, 26)}
                </text>
                {n.order !== undefined && (
                  <g>
                    <circle cx={n.x + DIA.nodeW - 12} cy={n.y + 12} r={8} style={{ fill: 'var(--text)' }} />
                    <text x={n.x + DIA.nodeW - 12} y={n.y + 15.5} textAnchor="middle" style={{ font: '600 10px var(--font-sans)', fill: 'var(--bg1)' }}>
                      {n.order}
                    </text>
                  </g>
                )}
              </g>
            );
          })}
        </svg>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" data-testid="pg-legend">
        <span className="inline-flex items-center gap-1.5"><i className="inline-block h-2.5 w-3.5 rounded-sm border" style={{ background: 'var(--err-bg)', borderColor: 'var(--err)' }} />{t('pg.dia.origin')}</span>
        <span className="inline-flex items-center gap-1.5"><i className="inline-block h-2.5 w-3.5 rounded-sm border" style={{ background: 'var(--bg1)', borderColor: 'var(--border-control)' }} />{t('pg.dia.member')}</span>
        <span className="inline-flex items-center gap-1.5"><i className="inline-block h-2.5 w-3.5 rounded-sm border border-dashed" style={{ background: 'var(--bg2)', borderColor: 'var(--border-control)' }} />{t('pg.dia.calm')}</span>
        <span className="inline-flex items-center gap-1.5"><i className="inline-block w-3.5 border-t-[1.5px]" style={{ borderColor: 'var(--text2)' }} />{t('pg.dia.call')}</span>
        <span className="inline-flex items-center gap-1.5"><i className="inline-block w-3.5 border-t-[1.5px] border-dashed" style={{ borderColor: 'var(--text2)' }} />{t('pg.dia.link')}</span>
        <span>{t('pg.dia.order')}</span>
      </div>
      {lay.truncated && <p className="m-0 text-xs text-muted-foreground">{t('pg.dia.truncated')}</p>}
    </div>
  );
}
