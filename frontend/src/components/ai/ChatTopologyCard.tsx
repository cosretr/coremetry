// ChatTopologyCard — v0.10.1388 (CoSRE topoloji; operatör: "Servis topolojisini
// CoSRE'den yönlendir ya da örnek bir trace'in topolojisini çiz"). Sunucunun
// `topology` bloğu (internal/api/copilot_service_topology.go): servis komşuluğu
// (topology_edges_5m) ya da tek bir örnek trace'in servis grafiği. Düğüme tık →
// düğümün detay sayfası (nodeDetailHref). Otomatik açılış yok. Hrefler
// lib/chatBlocks.ts topologyBlocks'ta isSafeAppPath'ten geçti.
//
// v0.10.1392 — operatör: "Bunu galiba eski service map'ten getiriyor. Onun yerine
// 1 hop topology getirebilir." Çizim artık servis sayfasının Topology sekmesinin
// grafiği (TopologyFlowGraph, odak sütun yerleşimi) KOMPAKT kipte: salt-okur
// (zoom/pan/sayfa kontrolü yok), içerik boyutunda, sabit genişlikli pillerde
// uzun ad elipsli + title; kart kendi kabında kayar (sayfa yatay kaymaz).
// Sütun tavanı sunucuda (sekmenin MAX_PER_COL'u); "+N more" tıklanamaz etiket.
import { useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import type { ChatTopologyPayload } from '@/lib/types';
import { TopologyFlowGraph } from '@/components/TopologyFlowGraph';
import { nodeDetailHref } from '@/components/topology/nodeDetailHref';
import { isSafeAppPath } from '@/lib/safeAppPath';
import { fmtRangeTR } from '@/lib/chatEvidence';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';
import { chatTopologyColumns, chatTopologyLayoutFocus, chatTopologyMap } from './chatTopologyView';

/** Kesim notu: servis komşuluğunda yön kırılımı, trace'te düğüm/kenar. "" = kesim yok. */
export function chatTopologyCutText(topo: ChatTopologyPayload): string {
  const t = topo.truncated;
  if (!(t.nodes > 0 || t.edges > 0)) return '';
  const parts: string[] = [];
  if (t.callers) parts.push(`+${t.callers} çağıran`);
  if (t.callees) parts.push(`+${t.callees} bağımlılık`);
  if (parts.length > 0) return `${parts.join(', ')} (+${t.edges} kenar) "+N more" altında — tümü Topology'de`;
  return `+${t.nodes} düğüm, +${t.edges} kenar gösterilmedi`;
}

export function ChatTopologyCard({ topo }: { topo: ChatTopologyPayload }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const navigate = useNavigate();
  const [hover, setHover] = useState<string | null>(null);
  const data = useMemo(() => chatTopologyMap(topo), [topo]);
  const columns = useMemo(() => chatTopologyColumns(topo), [topo]);
  const layoutFocus = useMemo(() => chatTopologyLayoutFocus(topo), [topo]);
  const byId = useMemo(() => new Map(topo.nodes.map(n => [n.id, n])), [topo]);
  const onSelect = (id: string) => {
    const n = byId.get(id);
    const href = n ? nodeDetailHref(n) : null;
    if (href && isSafeAppPath(href)) navigate(href);
  };
  const trace = topo.scope === 'trace';
  const scopeLabel = trace
    ? `örnek trace${topo.operation ? ` (${topo.operation})` : ''}`
    : `${topo.hops} hop${topo.errorsOnly ? ' · yalnız hatalı' : ''}`;
  const cut = chatTopologyCutText(topo);
  return (
    <section className="ev-card cm-topo-card" aria-label={`Topoloji · ${topo.focus}`}>
      <div className="ev-head">
        Topoloji · <span className="mono">{topo.focus}</span> · {scopeLabel} · {fmtRangeTR(topo.rangeS)}
      </div>
      <div className="cm-topo-flow">
        {/* trace kipinde tek trace'in her çağrısı çizilir (sütun atlayan dahil);
            servis komşuluğunda sunucu zaten yalnız akış kenarlarını yollar. */}
        <TopologyFlowGraph compact data={data} focus={layoutFocus} columns={columns}
          hoverNode={hover} onHoverNode={setHover} onSelectNode={onSelect}
          dropMessaging={false} animate="hover" showSecondary={trace} />
      </div>
      <div className="ev-foot">
        <Link to={topo.deepLink} {...linkProps}>Topology&apos;de aç</Link>
        {topo.traceHref && <Link to={topo.traceHref} {...linkProps}>Trace&apos;i aç</Link>}
        {cut && <span>{cut}</span>}
        {topo.note && <span>{topo.note}</span>}
      </div>
    </section>
  );
}
