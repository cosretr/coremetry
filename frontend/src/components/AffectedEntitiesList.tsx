import { useState } from 'react';
import { Link } from 'react-router';
import { Spinner } from '@/components/Spinner';
import { DisclosureButton } from '@/components/ui/DisclosureButton';
import { useProblemAffected } from '@/lib/queries/problems';
import { serviceHref } from '@/lib/serviceHref';
import { useT } from '@/lib/i18n';
import type { AffectedEntity } from '@/lib/types';

// AffectedEntitiesList — v0.10.707 (Dynatrace paritesi #5, dilim 2). Problem
// çekmecesi ve detay sayfası aynı bileşeni çizer. Aç-üzerine-getir: bileşen
// mount olunca tek istek (60 s cache). Boş liste dürüst "—" — sessiz gizleme yok.
//
// v0.10.1495 (operatör, prod JVM GC problemi: "AFFECTED ENTITIES (37)" hata
// görmeyen onlarca servisi etkilenen gibi sayıyordu) — sayım yalnız bağı
// DOĞRULANMIŞ ve etkisi GÖZLENMİŞ varlıklar (sunucu sınıflar: Go affectedSet);
// her hap bağın türünü söyler (çağıran / problem pod'u / kanıt pod'u /
// cluster). Hata görmeyen çağıranlar ayrı, KAPALI "Bağlı, etki görülmedi"
// grubunda. Topoloji okunamadıysa / pod kapsamlıysa bunu söyleyen satır.
export function AffectedEntitiesList({ problemId, service, window: win }: {
  problemId: string;
  service?: string;
  window?: { fromNs: number; toNs: number };
}) {
  const t = useT();
  const q = useProblemAffected(problemId);
  const [linkedOpen, setLinkedOpen] = useState(false);
  const ents = q.data?.entities ?? [];
  const linked = q.data?.linked ?? [];
  const notes: string[] = [];
  if (q.data && service && q.data.topologyKnown === false) notes.push(t('affected.topoUnknown'));
  if (q.data?.podScoped) notes.push(t('affected.podScoped'));
  if (q.data?.clustersUnverified) notes.push(t('affected.clustersUnverified'));
  return (
    <div style={{ marginBottom: 12 }} data-affected>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 4, textTransform: 'uppercase', letterSpacing: 0.4 }}>
        {t('affected.title')}{q.data ? ` (${q.data.total})` : ''}
      </div>
      {q.isPending && <Spinner />}
      {q.isError && <div style={{ fontSize: 12, color: 'var(--err)' }}>{t('affected.error')}</div>}
      {q.data && ents.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text3)' }}>— {service ? t('affected.empty.service') : t('affected.empty.subject')}</div>
      )}
      {ents.length > 0 && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }} data-affected-verified>
          {ents.map(e => <EntityPill key={`${e.kind}|${e.id}`} e={e} service={service} window={win} />)}
        </div>
      )}
      {notes.map(n => (
        <div key={n} style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4 }} data-affected-note>{n}</div>
      ))}
      {linked.length > 0 && (
        <div style={{ marginTop: 8 }} data-affected-linked>
          <DisclosureButton anatomy="row" expanded={linkedOpen} onClick={() => setLinkedOpen(o => !o)}>
            <span style={{ fontSize: 11, color: 'var(--text3)' }}>{t('affected.linked')} ({q.data?.linkedTotal ?? linked.length})</span>
          </DisclosureButton>
          {linkedOpen && (
            <>
              <div style={{ fontSize: 11, color: 'var(--text3)', margin: '2px 0 4px' }}>{t('affected.linked.note')}</div>
              <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
                {linked.map(e => <EntityPill key={`${e.kind}|${e.id}`} e={e} service={service} window={win} />)}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function EntityPill({ e, service, window: win }: { e: AffectedEntity; service?: string; window?: { fromNs: number; toNs: number } }) {
  const t = useT();
  const rel = e.relation ? t(`affected.rel.${e.relation}`) : e.kind;
  const title = e.kind === 'service'
    ? `${e.id} · ${rel} · ${e.calls ?? 0} çağrı, ${e.errors ?? 0} hata (${(e.errorRate ?? 0).toFixed(1)}%)${e.hasOpenProblem ? ' · kendi problemi AÇIK' : ''}`
    : e.kind === 'pod' ? `${e.id} · ${rel}${e.count ? ` · hipotez kanıtında ${e.count} kez` : ''}` : `cluster ${e.id}`;
  const label = (
    <>
      {e.kind === 'service' && <span className="dot" style={{ background: e.hasOpenProblem ? 'var(--err)' : undefined }} />}
      <span style={{ color: 'var(--text3)', fontSize: 10, marginRight: 4 }}>{rel}</span>
      <span className="mono">{e.id}</span>
    </>
  );
  if (e.kind === 'service') {
    return <Link to={serviceHref(e.id, { range: win })} className="pb-pill" title={title} style={{ textDecoration: 'none', color: 'var(--accent2)' }}>{label} →</Link>;
  }
  if (e.kind === 'pod' && service) {
    return <Link to={serviceHref(service, { range: win, tab: 'infra', params: { jpod: e.id } })} className="pb-pill" title={title} style={{ textDecoration: 'none', color: 'var(--accent2)' }}>{label} →</Link>;
  }
  return <span className="pb-pill" title={title}>{label}</span>;
}
