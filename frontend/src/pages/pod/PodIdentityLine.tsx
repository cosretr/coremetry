// PodIdentityLine — v0.10.160 (A anatomisi, mockup option-A §1). /pod'un TEK
// kimlik satırı: cluster › node › namespace › workload/kind › pod linkleri +
// live/stale/gone + faz + name-stable rozetleri; altında ömür/uid/kaynak satırı,
// atMatch=false uyarısı ve ömür tarihçesi. Sağda pivotlar (geri, servis
// sayfası, Trace'ler, Loglar). v0.10.135 PodEntityPanel'in zinciri + v0.9.151
// KPI şeridinin Cluster/Namespace/faz tekrarı (F1) BURADA birleşti — ikinci
// satır yok. Entity katmanı kapalı/kayıt yok → düz metin zincir (link yok).
import { Link } from 'react-router';
import { Stack, Row, Badge } from '@/components/ui';
import { fmtDateTime } from '@/lib/utils';
import { t, tf, useLang } from '@/lib/i18n';
import { entityLiveness } from '@/lib/entityHref';
import { podPhaseBadge } from '@/pages/clusters/thresholds';
import type { EntityDetailResponse, ClusterPodRow, TimeRange } from '@/lib/types';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

function short(id: string): string {
  const i = id.lastIndexOf('/');
  return i >= 0 ? id.slice(i + 1) : id.replace(/^[a-z]+:/, '');
}

export interface PodPivot { href: string; label: string; title?: string }

export function PodIdentityLine({ detail, clusterName, namespace, pod, row, at, pageRange, pivots }: {
  /** /api/entity cevabı — bayrak kapalı ya da kayıt yoksa undefined (düz zincir) */
  detail?: EntityDetailResponse;
  /** Thanos kayıt adı (cluster çözülemediyse boş) */
  clusterName: string;
  namespace: string;
  pod: string;
  /** /api/clusters/pods satırı — faz rozeti buradan (KSM yoksa rozet yok) */
  row?: ClusterPodRow;
  /** ms — tarihsel bağlam (?at=) */
  at?: number;
  pageRange: TimeRange;
  pivots: PodPivot[];
}) {
  const lang = useLang(); // v0.10.2246 — metinler arayüz dilinde (podpage.id.*)
  const live = detail ? entityLiveness(detail.entity) : null;
  const hrefOpts = { range: pageRange, at: at || undefined, clusterName: detail?.cluster?.name ?? clusterName };
  const chain = detail ? [...detail.parents].reverse().filter(p => p.type !== 'cluster') : [];
  const nodeRec = detail?.node
    ? { type: 'node' as const, id: detail.node, name: short(detail.node), clusterId: detail.entity.clusterId }
    : null;
  return (
    <div className="pod-head">
      <Stack gap={2}>
        <Row gap={2} wrap>
          {detail?.cluster ? (
            <EntityLink to={{ kind: 'record', rec: { type: 'cluster', id: `cluster:${detail.cluster.id}`, name: detail.cluster.name, clusterId: detail.cluster.id }, opts: hrefOpts }} className="sec" title={detail.cluster.id}>
              {detail.cluster.name}
            </EntityLink>
          ) : (
            <span className="mono" title="cluster">{clusterName || '—'}</span>
          )}
          {nodeRec && <><span className="field-hint">›</span><EntityLink to={{ kind: 'record', rec: nodeRec, opts: hrefOpts }} className="sec" title={detail?.node}>{nodeRec.name}</EntityLink></>}
          {detail ? chain.map(p => (
            <Row key={p.id} gap={2}>
              <span className="field-hint">›</span>
              <EntityLink to={{ kind: 'record', rec: p, opts: hrefOpts }} className="sec" title={p.id}>
                {p.type === 'workload' ? `${p.labels?.kind ?? 'workload'}/${p.name}` : p.name}
              </EntityLink>
            </Row>
          )) : (
            <><span className="field-hint">›</span><span className="mono" title="namespace">{namespace || '—'}</span></>
          )}
          <span className="field-hint">›</span>
          <Badge tone="info" title={detail?.entity.id ?? pod}>{pod}</Badge>
          {/* v0.10.929 (K5) — canlı pod normal hâl: nötr (EntityDetail emsali). */}
          {live === 'live' && <Badge>live</Badge>}
          {live === 'stale' && <Badge tone="warning" title={t('podpage.id.stale', lang)}>stale</Badge>}
          {live === 'gone' && <Badge tone="danger">{t('podpage.id.gone', lang)}</Badge>}
          {row?.phase && <span className={`badge ${podPhaseBadge(row.phase)}`} title={t('podpage.id.phase', lang)}>{row.phase}</span>}
          {detail?.entity.nameStable && <Badge tone="neutral" title={t('podpage.id.nameStable', lang)}>name-stable</Badge>}
        </Row>
        {detail && (
          <Row gap={2} wrap>
            <span className="field-hint">
              {live === 'gone'
                ? `${tf('podpage.id.lastSeen', lang, { t: fmtDateTime(new Date(detail.entity.lastSeen)) })} · ${tf('podpage.id.lifetime', lang, { from: fmtDateTime(new Date(detail.entity.validFrom)), to: detail.entity.validTo ? fmtDateTime(new Date(detail.entity.validTo)) : '—' })}`
                : `${tf('podpage.id.lifetime', lang, { from: fmtDateTime(new Date(detail.entity.validFrom)), to: t('podpage.open', lang) })} · ${tf('podpage.id.lastSeen', lang, { t: fmtDateTime(new Date(detail.entity.lastSeen)) })}`}
              {' · '}{tf('podpage.id.source', lang, { src: detail.entity.source })}{detail.entity.uid ? ` · uid ${detail.entity.uid}` : ''}
            </span>
            {!!at && detail.atMatch === false && (
              <Badge tone="warning" title={tf('podpage.id.atWanted', lang, { t: fmtDateTime(new Date(at)) })}>{t('podpage.id.atMiss', lang)}</Badge>
            )}
            {detail.lifetimes.length > 1 && <span className="field-hint">{tf('podpage.id.lifetimes', lang, { n: detail.lifetimes.length })}</span>}
          </Row>
        )}
      </Stack>
      {/* Gezinme = <Link> (Button polimorfik değil — AS-1); görünüm .pod-pivot sınıfında. */}
      <div className="pod-pivots">
        {pivots.map(p => <Link key={p.label} to={p.href} className="pod-pivot" title={p.title}>{p.label}</Link>)}
      </div>
    </div>
  );
}
