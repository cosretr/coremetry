// SpanK8sSection — v0.10.137 (DETAY SAYFALARI adım 3). Span detayının
// "Kubernetes" bölümü: cluster › namespace › pod › node zinciri, her biri
// entityHref ile (at = span başlangıcı → o an geçerli kayıt; range korunur;
// pod linki servis bağlamını taşır). Link-yok durumları açıkça ilan edilir
// (bağlam yok / cluster değeri yok / cluster eşlenmemiş). v0.10.2233 (Entity
// S3): yol sunucudan (POST /api/entity/paths); pod → iş yükü → servis çipleri.
// Gösterim kararı (bayrak + uygulama-içi link izni) çağıranda.
import { Link } from 'react-router';
import { Row, Badge } from '@/components/ui';
import { spanK8sNote, type SpanK8sContext } from '@/lib/spanK8s';

export function SpanK8sSection({ ctx }: { ctx: SpanK8sContext }) {
  const note = spanK8sNote(ctx);
  // Yol çipleri: pod → iş yükü → servis (yalnız çözülenler).
  const chain = (
    <>
      {ctx.workloadHref && <><span className="field-hint">→</span><Link to={ctx.workloadHref} className="sec" title={`İş yükü · ${ctx.workloadKind ?? ''}`}>{ctx.workloadName}</Link></>}
      {ctx.serviceHref && <><span className="field-hint">→</span><Link to={ctx.serviceHref} className="sec" title="Servis">{ctx.serviceName}</Link></>}
    </>
  );
  if (ctx.reason !== 'ok') {
    // v0.10.150 — 'no-namespace': cluster/node linkleri VAR; v0.10.190 — pod
    // linki ham /pod yolu (namespace'i sayfa Thanos'tan çözmeye çalışır).
    return (
      <Row gap={2} wrap>
        <Badge tone="neutral">{ctx.reason === 'no-k8s' ? 'bağlam yok' : ctx.reason === 'no-namespace' ? 'namespace\'siz' : ctx.reason === 'pending' ? 'çözülüyor' : 'link yok'}</Badge>
        {ctx.clusterHref && <Link to={ctx.clusterHref} className="sec cluster-name" title={ctx.clusterId}>{ctx.clusterName}</Link>}
        {/* v0.10.190 — namespace'siz span: ham /pod linki (sayfa namespace'i Thanos'tan çözer) */}
        {ctx.pod && (ctx.podHref
          ? <Link to={ctx.podHref} className="sec mono" title={`Pod sayfası · ${ctx.clusterName ?? ctx.clusterValue ?? ''} / ${ctx.pod} · namespace Thanos'tan çözülmeye çalışılır (topk 500 dışındaysa çözülemez)`}>{ctx.pod}</Link>
          : <span className="mono">{ctx.pod}</span>)}
        {ctx.podHref && chain}
        {ctx.nodeHref && <><span className="field-hint">on</span><Link to={ctx.nodeHref} className="sec" title="Node detayı">{ctx.node}</Link></>}
        {note && <span className="field-hint">{note}</span>}
      </Row>
    );
  }
  return (
    <Row gap={2} wrap>
      {ctx.clusterHref && <Link to={ctx.clusterHref} className="sec cluster-name" title={ctx.clusterId}>{ctx.clusterName}</Link>}
      {ctx.namespaceHref && <><span className="field-hint">›</span><Link to={ctx.namespaceHref} className="sec">{ctx.namespace}</Link></>}
      <span className="field-hint">›</span>
      <Link to={ctx.podHref!} className="sec" title={`Pod detayı · ${ctx.clusterName} / ${ctx.namespace} / ${ctx.pod} · span anı`}>{ctx.pod}</Link>
      {chain}
      {ctx.nodeHref && <><span className="field-hint">on</span><Link to={ctx.nodeHref} className="sec" title="Node detayı">{ctx.node}</Link></>}
    </Row>
  );
}
